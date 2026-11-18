package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonschemautil"
	"github.com/qlustered/qctl/internal/rule_versions"
)

// generatedByKey is the ownership block the server stamps into a produced
// field's field_schema (type, dataset_rule_id, rule_revision_id,
// rule_field_name). The server overwrites a client-supplied one, so qctl drops
// it from round-tripped output and from every field_schema it sends.
const generatedByKey = "generated_by"

// fieldSchemaTypeKey is the one field_schema key qctl validates client-side.
const fieldSchemaTypeKey = "type"

// ProducedFieldEntry is one `spec.produced_fields` declaration: a downstream
// table column this rule writes, which does not exist on the table yet.
//
// The three keys are the wire's own (ProducedFieldRequestSchema), with one
// difference: field_schema may be omitted, and is then filled from the rule
// revision's enriched_field_schemas -- the author's declaration for that
// output field, which is what the web wizard sends and the only place the
// answer exists. ResolveProducedFields does the filling, so everything
// downstream of it sees a complete entry.
type ProducedFieldEntry struct {
	RuleFieldName    string                 `yaml:"rule_field_name" json:"rule_field_name"`
	DatasetFieldName string                 `yaml:"dataset_field_name" json:"dataset_field_name"`
	FieldSchema      map[string]interface{} `yaml:"field_schema,omitempty" json:"field_schema,omitempty"`
}

// producedOutputColumns is the set of generic rule field names a produced
// field may declare: the revision's enriches_columns union corrects_columns,
// minus the "*" wildcard, which names no single column
// (cettings/.../produced_fields.py::_allowed_rule_output_columns).
func producedOutputColumns(rev *rule_versions.RuleRevisionFull) []string {
	seen := make(map[string]bool)
	var cols []string
	for _, group := range [][]string{rev.EnrichesColumns, rev.CorrectsColumns} {
		for _, col := range group {
			if col == "*" || seen[col] {
				continue
			}
			seen[col] = true
			cols = append(cols, col)
		}
	}
	sort.Strings(cols)
	return cols
}

// ResolveProducedFields validates a manifest's spec.produced_fields against
// the rule revision that governs it and fills in each entry's omitted
// field_schema, in place, so the request body, the confirmation block and
// `qctl diff -f` all read the same resolved declarations.
//
// rev is required whenever the manifest declares any field, and ignored
// otherwise -- a manifest that says nothing about produced fields never costs
// a rule-revision fetch. An explicit empty list names no rule output field, so
// it needs no revision context either; whether it is a request qctl will send
// is ReconcileProducedFieldsWithLive's question, not this one's.
//
// Every rejection OF THE MANIFEST is a bad-usage error (exit 2), raised
// before the request. The checks are the subset of the backend's that qctl can
// make from the revision alone: membership, duplicates, and the field type.
// Reserved names, Atlas normalized form and collisions with an existing column
// stay server-side rather than being half-copied here.
//
// The one arm that is not exit 2 is the missing-revision guard below, which no
// caller can reach: it reports a programming error in a future caller, not
// anything a manifest can say, so it stays a plain error (exit 1) rather than
// blaming the user's file for it.
func ResolveProducedFields(manifest *TableRuleApplyManifest, rev *rule_versions.RuleRevisionFull, source string) error {
	if manifest.Spec.ProducedFields == nil {
		return nil
	}
	entries := *manifest.Spec.ProducedFields
	if len(entries) == 0 {
		return nil
	}
	if rev == nil {
		return fmt.Errorf("internal error: spec.produced_fields in %s resolved without a rule revision", source)
	}

	allowed := producedOutputColumns(rev)
	allowedSet := make(map[string]bool, len(allowed))
	for _, col := range allowed {
		allowedSet[col] = true
	}

	seenRuleFields := make(map[string]bool, len(entries))
	seenDatasetFields := make(map[string]bool, len(entries))
	for i := range entries {
		entry := &entries[i]
		if err := checkProducedFieldNames(entry, i, allowed, allowedSet, source); err != nil {
			return err
		}
		if seenRuleFields[entry.RuleFieldName] {
			return qerrors.NewBadUsagef("spec.produced_fields declares rule_field_name %q twice in %s; one declaration per rule output field",
				entry.RuleFieldName, source)
		}
		seenRuleFields[entry.RuleFieldName] = true
		if seenDatasetFields[entry.DatasetFieldName] {
			return qerrors.NewBadUsagef("spec.produced_fields declares dataset_field_name %q twice in %s; two rule output fields cannot write the same column",
				entry.DatasetFieldName, source)
		}
		seenDatasetFields[entry.DatasetFieldName] = true

		if err := resolveProducedFieldSchema(entry, i, rev, source); err != nil {
			return err
		}
		if err := checkProducedFieldAgainstMapping(entry, manifest, source); err != nil {
			return err
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].RuleFieldName < entries[j].RuleFieldName })
	return nil
}

// checkProducedFieldNames rejects a blank name and a rule_field_name the
// revision does not declare as an output.
func checkProducedFieldNames(entry *ProducedFieldEntry, i int, allowed []string, allowedSet map[string]bool, source string) error {
	if strings.TrimSpace(entry.RuleFieldName) == "" {
		return qerrors.NewBadUsagef("spec.produced_fields[%d].rule_field_name is required in %s", i, source)
	}
	if strings.TrimSpace(entry.DatasetFieldName) == "" {
		return qerrors.NewBadUsagef("spec.produced_fields[%d].dataset_field_name is required in %s (the table column the rule writes)", i, source)
	}
	if allowedSet[entry.RuleFieldName] {
		return nil
	}
	if len(allowed) == 0 {
		return qerrors.NewBadUsagef("spec.produced_fields names %q in %s, but this rule revision declares no enriches_columns or corrects_columns -- it writes no columns, so it can produce none",
			entry.RuleFieldName, source)
	}
	return qerrors.NewBadUsagef("spec.produced_fields rule_field_name %q in %s is not one of the rule revision's output fields (allowed: [%s], from its enriches_columns and corrects_columns)",
		entry.RuleFieldName, source, strings.Join(allowed, ", "))
}

// resolveProducedFieldSchema drops a generated_by the manifest states, fills
// an omitted field_schema from the revision's enriched_field_schemas, and
// validates the resulting type. A field_schema holding nothing but
// generated_by counts as omitted.
func resolveProducedFieldSchema(entry *ProducedFieldEntry, i int, rev *rule_versions.RuleRevisionFull, source string) error {
	entry.FieldSchema = stripGeneratedBy(entry.FieldSchema)
	if len(entry.FieldSchema) == 0 {
		declared := revisionFieldSchema(rev, entry.RuleFieldName)
		if declared == nil {
			return qerrors.NewBadUsagef("spec.produced_fields[%d].field_schema is required in %s: rule revision %s declares no schema for output field %q, so there is no default to take (state it as `field_schema: {type: <FieldType>}`)",
				i, source, rev.Release, entry.RuleFieldName)
		}
		entry.FieldSchema = declared
	}
	return validateFieldSchemaType(entry, i, source)
}

// revisionFieldSchema copies the revision's declared schema for one output
// field, minus any generated_by: the copy is what goes on the wire, and
// mutating the fetched revision's map would leak between entries.
func revisionFieldSchema(rev *rule_versions.RuleRevisionFull, ruleFieldName string) map[string]interface{} {
	if rev.EnrichedFieldSchemas == nil {
		return nil
	}
	declared, ok := (*rev.EnrichedFieldSchemas)[ruleFieldName]
	if !ok || len(declared) == 0 {
		return nil
	}
	return stripGeneratedBy(declared)
}

// stripGeneratedBy returns a copy of a field schema without the server-stamped
// ownership block. Returns nil for an empty input so an absent schema stays
// absent rather than becoming an empty map.
func stripGeneratedBy(fieldSchema map[string]interface{}) map[string]interface{} {
	if len(fieldSchema) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(fieldSchema))
	for k, v := range fieldSchema {
		if k == generatedByKey {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateFieldSchemaType checks the one field_schema key the backend rejects
// outright: `type` must be present and a known column type. The rest of the
// definition (args, is_nullable, indexing) is passed through untouched.
//
// Entries are addressed by index, like every sibling message in this file:
// spec.produced_fields is a LIST, so `[0]` is the coordinate a reader can find
// in their own manifest, and the rule field is named in the prose instead.
func validateFieldSchemaType(entry *ProducedFieldEntry, i int, source string) error {
	raw, ok := entry.FieldSchema[fieldSchemaTypeKey]
	if !ok {
		return qerrors.NewBadUsagef("spec.produced_fields[%d].field_schema.type is required in %s for output field %q (one of: %s)",
			i, source, entry.RuleFieldName, strings.Join(api.FieldTypes, ", "))
	}
	typeName, ok := raw.(string)
	if !ok {
		return qerrors.NewBadUsagef("spec.produced_fields[%d].field_schema.type must be a string in %s for output field %q (one of: %s)",
			i, source, entry.RuleFieldName, strings.Join(api.FieldTypes, ", "))
	}
	for _, valid := range api.FieldTypes {
		if typeName == valid {
			return nil
		}
	}
	return qerrors.NewBadUsagef("spec.produced_fields[%d].field_schema.type %q is not a column type in %s (one of: %s)",
		i, typeName, source, strings.Join(api.FieldTypes, ", "))
}

// checkProducedFieldAgainstMapping rejects a spec.column_mapping entry that
// collides with a declaration.
//
// An upgrade may map a produced field to a matching predecessor column that
// already exists. Other instantiations cannot map a newly produced column.
func checkProducedFieldAgainstMapping(entry *ProducedFieldEntry, manifest *TableRuleApplyManifest, source string) error {
	column, mapped := manifest.Spec.ColumnMapping[entry.RuleFieldName]
	if !mapped {
		return nil
	}
	if manifest.IsPatch() || manifest.Spec.UpgradedFrom != "" {
		if column == entry.DatasetFieldName {
			return nil
		}
		return qerrors.NewBadUsagef("spec.column_mapping binds %q to column %q in %s, but spec.produced_fields declares it produces %q; drop one of the two",
			entry.RuleFieldName, column, source, entry.DatasetFieldName)
	}
	return qerrors.NewBadUsagef("spec.column_mapping must not bind %q in %s: spec.produced_fields declares it as a NEW column (%q), and a column_mapping value has to be a column the table already has -- drop the column_mapping entry",
		entry.RuleFieldName, source, entry.DatasetFieldName)
}

// producedFieldShape is one declaration reduced to what the backend compares:
// the column it writes and the schema it writes it with. field_schema is held
// as canonical JSON because encoding/json sorts map keys, which makes two
// equal schemas compare equal whatever order they were written in.
type producedFieldShape struct {
	datasetFieldName string
	fieldSchema      string
}

// producedFieldShapes keys declarations by rule_field_name, which is the
// backend's own key for them (_produced_fields_by_name, with generated_by
// already stripped on both sides).
func producedFieldShapes(entries []ProducedFieldEntry) map[string]producedFieldShape {
	out := make(map[string]producedFieldShape, len(entries))
	for _, e := range entries {
		encoded, err := json.Marshal(stripGeneratedBy(e.FieldSchema))
		if err != nil {
			// A field_schema that will not marshal cannot be compared, so it
			// is treated as unlike anything -- the safe direction, since the
			// caller's only use of "equal" is to send LESS.
			encoded = []byte(fmt.Sprintf("unencodable:%v", e.FieldSchema))
		}
		out[e.RuleFieldName] = producedFieldShape{datasetFieldName: e.DatasetFieldName, fieldSchema: string(encoded)}
	}
	return out
}

// ReconcileProducedFieldsWithLive decides what a PATCH manifest's resolved
// spec.produced_fields may actually send, by comparing it against the
// declarations the rule already carries. It runs after ResolveProducedFields,
// so both sides are in the manifest's own shape with field_schema filled in.
//
// The backend has three answers here and they are not symmetric, so qctl
// cannot just forward the key:
//
//   - EQUAL to the live declarations: the key is dropped from the body.
//   - ADDS a column the rule does not already write: sent unchanged. What
//     happens to it then depends on the rule's state, and this function does
//     not branch on it. On a rule the backend can open a candidate for, the
//     request becomes a revalidation candidate whose passing dry run
//     parks the column in a draft migration and raises the review alert. On a
//     PAUSED one it does not: revalidation_triggers returns nothing for a rule
//     in NON_PROMOTABLE_RULE_STATES, so the edit lands directly and the
//     request itself parks the column through ensure_draft and raises the
//     review alert when the draft differs from the live schema for this rule.
//     Either way applying that migration is what puts the column
//     in the schema -- but it enables only a pending_validation rule, never a
//     paused one (enable_rules_for_applied_produced_fields).
//   - DROPS or RETYPES a declaration without adding a column: sent unchanged
//     for a paused rule, whose direct path replaces the row set wholesale and
//     prunes the draft. _promotion_schema_complaints is never reached there.
//     Every other patchable state is refused here, exit 2, before the request.
//     A drop of an active column read by a linked table is refused at PATCH
//     time. Otherwise the backend can accept the candidate (202) and pass its
//     dry run, then refuse promotion -- _promotion_schema_complaints reports
//     "drops the output fields" / "redeclares the output fields" and
//     complete_revalidation_dry_run leaves it pending. atlas pins the latter
//     in TestAClearedProducedFieldIsNotPromoted. qctl refuses first.
//
// Whether a request needs a schema change is the backend's `absent` test over
// the TABLE's model_schema, which qctl does not fetch. It is approximated by
// "names a column this rule does not already write": for the rule's own
// columns the two agree, because a declaration that landed put its column in
// the schema. Where the approximation is wrong -- an existing column owned by
// nobody, or by another rule -- the request is one the backend refuses anyway,
// with a message of its own.
func ReconcileProducedFieldsWithLive(manifest *TableRuleApplyManifest, detail *DatasetRuleDetail, source string) error {
	if manifest.Spec.ProducedFields == nil {
		return nil
	}
	declared := producedFieldShapes(*manifest.Spec.ProducedFields)
	live := producedFieldShapes(ProducedFieldsFromResponse(detail.ProducedFields))

	var dropped, retyped []string
	for name, shape := range declared {
		liveShape, ok := live[name]
		if !ok || liveShape.datasetFieldName != shape.datasetFieldName {
			// A new output field, or the same one renamed onto another
			// column: either way a column the rule does not write today, so
			// the candidate is staged with a migration and promoted with
			// schema_change_applied -- which is the arm that tolerates the
			// drops and retypes riding along with it. A paused rule reaches
			// the same place by the direct path, where replace_produced_fields
			// writes the whole set at once. On either path the server refuses
			// only a drop a linked table reads, with
			// ERR_FIELD_IN_USE_BY_COMPOSITE. Nothing collected so far
			// matters once one of these is found, which is why the whole
			// request is released here rather than at the end.
			return nil
		}
		if liveShape.fieldSchema != shape.fieldSchema {
			retyped = append(retyped, name)
		}
	}
	for name := range live {
		if _, ok := declared[name]; !ok {
			dropped = append(dropped, name)
		}
	}
	if len(dropped) == 0 && len(retyped) == 0 {
		// An exact restatement. Sending nothing is what makes the documented
		// round-trip inert; see the EQUAL case above for what sending it costs.
		manifest.Spec.ProducedFields = nil
		return nil
	}
	if detail.State == api.DatasetRuleStateDisabled {
		// Atlas cannot open a revalidation candidate for a paused rule. It
		// takes the direct replacement path instead, where drops, explicit
		// clears and retypes all land on the live rule without a promotion
		// gate. Keep the resolved key intact so apply sends it and diff shows
		// the exact request body apply will send.
		return nil
	}
	return producedFieldDeadEndError(dropped, retyped, source)
}

// producedFieldDeadEndError explains why a drop or retype without a new column
// cannot land through the candidate path.
func producedFieldDeadEndError(dropped, retyped []string, source string) error {
	var complaints []string
	for _, group := range []struct {
		verb  string
		names []string
	}{{"drops", dropped}, {"redeclares", retyped}} {
		if len(group.names) > 0 {
			sort.Strings(group.names)
			complaints = append(complaints, fmt.Sprintf("%s %s", group.verb, strings.Join(group.names, ", ")))
		}
	}
	return qerrors.NewBadUsagef("spec.produced_fields in %s %s without adding a column, which the backend cannot land: "+
		"a drop of an active column read by a linked table is refused at PATCH time; otherwise a candidate can pass its dry run but fail promotion and remain pending. "+
		"Omit spec.produced_fields to leave the rule's declarations as they are -- clearing or retyping one is not something a manifest can do today",
		source, strings.Join(complaints, " and "))
}

// PatchRevisionID names the rule revision that governs a patch manifest's
// params and produced-field declarations: the rebind target when the manifest
// rebinds, else the rule's current revision. A manifest that rebinds describes
// the revision it is moving TO, so a param or an output field the target added
// must not be judged against the revision being left.
//
// `qctl apply -f` and `qctl diff -f` both call it, which is what keeps the
// preview and the request built from the same revision.
func PatchRevisionID(manifest *TableRuleApplyManifest, detail *DatasetRuleDetail) string {
	if manifest.Spec.RuleRevisionID != "" {
		return manifest.Spec.RuleRevisionID
	}
	return detail.RuleRevision.ID.String()
}

// NeedsProducedFieldRevision reports whether spec.produced_fields has to be
// resolved against a rule revision. Only a non-empty list does: an absent key
// and the empty-list clear both name no rule output field, so neither has a
// field_schema to fill in or a membership to check.
//
// It remains narrower than NeedsRuleRevision because an explicit empty-list
// clear names no output field to resolve, while params still need the
// revision's schema.
func NeedsProducedFieldRevision(manifest *TableRuleApplyManifest) bool {
	return manifest.Spec.ProducedFields != nil && len(*manifest.Spec.ProducedFields) > 0
}

// NeedsRuleRevision reports whether a patch manifest has to be judged against
// a rule revision at all: only spec.params and spec.produced_fields are, and
// fetching one for a manifest that sets neither is a wasted round trip.
func NeedsRuleRevision(manifest *TableRuleApplyManifest) bool {
	return manifest.Spec.Params != nil || NeedsProducedFieldRevision(manifest)
}

// PreparePatchRevisionFields performs every patch-manifest preparation step
// whose answer comes from the governing rule revision. Both `qctl apply -f`
// and `qctl diff -f` call it after fetching that revision, so the preview
// cannot accept params the apply path will reject or resolve a produced field
// differently from the request.
func PreparePatchRevisionFields(manifest *TableRuleApplyManifest, rev *rule_versions.RuleRevisionFull, source string) error {
	if NeedsRuleRevision(manifest) && rev == nil {
		return fmt.Errorf("internal error: revision-dependent fields in %s prepared without a rule revision", source)
	}
	if err := ResolveProducedFields(manifest, rev, source); err != nil {
		return err
	}
	if manifest.Spec.Params == nil {
		return nil
	}
	return jsonschemautil.ValidateParams(*manifest.Spec.Params, rev.ParamSchema)
}

// ProducedFieldsFromResponse renders a rule's live produced-field
// declarations in the manifest's own shape: the three request keys, sorted by
// rule_field_name, with the server-owned ones dropped -- the row's id and
// timestamps, and the generated_by block the server stamps into field_schema
// and ignores on the way in.
//
// It is what makes `qctl get table-rule -o yaml` round-trip, and what
// `qctl diff -f` compares a manifest against: both sides of that comparison
// have to be the same shape or every diff would report a change.
//
// The empty result is an empty slice, never nil: it is compared against a
// manifest's `produced_fields: []`, and a nil would render as JSON `null` and
// report declaring nothing against a rule that declares nothing as a change.
func ProducedFieldsFromResponse(fields *[]api.ProducedFieldResponseSchema) []ProducedFieldEntry {
	if fields == nil {
		return []ProducedFieldEntry{}
	}
	out := make([]ProducedFieldEntry, 0, len(*fields))
	for _, f := range *fields {
		out = append(out, ProducedFieldEntry{
			RuleFieldName:    f.RuleFieldName,
			DatasetFieldName: f.DatasetFieldName,
			FieldSchema:      stripGeneratedBy(f.FieldSchema),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RuleFieldName < out[j].RuleFieldName })
	return out
}

// ProducedFieldRequests converts resolved declarations into the instantiate
// body's own type. A manifest that declares nothing sends nothing: the
// pointer stays nil and the wire carries `produced_fields: null`, which the
// backend reads as "this rule adds no columns" -- the request every
// validation-rule instantiate has always sent.
func ProducedFieldRequests(entries *[]ProducedFieldEntry) *[]api.ProducedFieldRequestSchema {
	if entries == nil {
		return nil
	}
	out := make([]api.ProducedFieldRequestSchema, 0, len(*entries))
	for _, e := range *entries {
		out = append(out, api.ProducedFieldRequestSchema{
			RuleFieldName:    e.RuleFieldName,
			DatasetFieldName: e.DatasetFieldName,
			FieldSchema:      e.FieldSchema,
		})
	}
	return &out
}

// FormatProducedFields renders declarations as "rule_field -> column (Type)"
// pairs for the confirmation summary, in the order they will be sent.
func FormatProducedFields(entries []ProducedFieldEntry) string {
	if len(entries) == 0 {
		return "(none)"
	}
	pairs := make([]string, 0, len(entries))
	for _, e := range entries {
		pair := fmt.Sprintf("%s -> %s", e.RuleFieldName, e.DatasetFieldName)
		if t, ok := e.FieldSchema[fieldSchemaTypeKey].(string); ok {
			pair += fmt.Sprintf(" (%s)", t)
		}
		pairs = append(pairs, pair)
	}
	return strings.Join(pairs, ", ")
}
