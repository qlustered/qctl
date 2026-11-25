package apply

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
)

// codeRuleFieldMappingUnresolved is the instantiate 422 naming rule fields the
// server could not bind to a table column.
const codeRuleFieldMappingUnresolved = "RULE_FIELD_MAPPING_UNRESOLVED"

// instantiateBindings is what a create sends for its rule fields and the
// server's binding preview of the fields it leaves out.
type instantiateBindings struct {
	// explicit is the rule_column_mapping the create sends.
	explicit map[string]string
	// produced maps each spec.produced_fields rule field to its column, ""
	// when the manifest leaves the column to the server.
	produced map[string]string
	// preview is the revision's field_column_candidates by field, or nil when
	// the server sent none, sent an entry without a status, or previewHolds
	// is false.
	preview map[string]api.RuleFieldColumnCandidatesSchema
}

// newInstantiateBindings reads the binding preview off rev.
func newInstantiateBindings(manifest *dataset_rules.TableRuleApplyManifest, rev *rule_versions.RuleRevisionFull, explicit map[string]string) *instantiateBindings {
	b := &instantiateBindings{explicit: explicit, produced: map[string]string{}}
	if manifest.Spec.ProducedFields != nil {
		for _, field := range *manifest.Spec.ProducedFields {
			b.produced[field.RuleFieldName] = field.DatasetFieldName
		}
	}
	if rev.FieldColumnCandidates == nil || len(*rev.FieldColumnCandidates) == 0 {
		return b
	}
	preview := make(map[string]api.RuleFieldColumnCandidatesSchema, len(*rev.FieldColumnCandidates))
	for _, item := range *rev.FieldColumnCandidates {
		if item.Status == "" {
			return b
		}
		preview[item.RuleFieldName] = item
	}
	if b.previewHolds(preview) {
		b.preview = preview
	}
	return b
}

// previewHolds reports whether no explicit or produced field inherits a
// column in preview that the create leaves unclaimed. Such a column is open
// to the other fields at create time but not in the preview.
func (b *instantiateBindings) previewHolds(preview map[string]api.RuleFieldColumnCandidatesSchema) bool {
	claimed := map[string]bool{}
	for _, column := range b.explicit {
		claimed[column] = true
	}
	for _, column := range b.produced {
		claimed[column] = true
	}
	for field, item := range preview {
		if !b.chosen(field) || item.Source == nil || *item.Source != api.RuleFieldBindingSourceInherited || item.BoundTo == nil {
			continue
		}
		if !claimed[*item.BoundTo] {
			return false
		}
	}
	return true
}

// chosen reports whether the create maps or produces field itself.
func (b *instantiateBindings) chosen(field string) bool {
	_, mapped := b.explicit[field]
	_, produced := b.produced[field]
	return mapped || produced
}

// claimed reports whether the create maps or produces a field to column.
func (b *instantiateBindings) claimed(column string) bool {
	for _, c := range b.explicit {
		if c == column {
			return true
		}
	}
	for _, c := range b.produced {
		if c == column {
			return true
		}
	}
	return false
}

// predicted is the preview's binding of field, and false when the preview
// has none or the create claims the column it names.
func (b *instantiateBindings) predicted(field string) (api.RuleFieldColumnCandidatesSchema, bool) {
	item, ok := b.preview[field]
	if !ok || b.chosen(field) {
		return item, false
	}
	if item.Status == api.Bound && (item.BoundTo == nil || b.claimed(*item.BoundTo)) {
		return item, false
	}
	return item, true
}

// unbound is the required fields the create leaves to the server that the
// preview binds to no column.
func (b *instantiateBindings) unbound() []api.RuleFieldBindingSchema {
	var out []api.RuleFieldBindingSchema
	for _, field := range sortedKeys(b.preview) {
		item, ok := b.predicted(field)
		if ok && item.Required && item.Status == api.Unbound {
			out = append(out, previewAsBinding(item))
		}
	}
	return out
}

// auto is the column the preview binds each field the create leaves to the
// server, where the preview binds one.
func (b *instantiateBindings) auto() map[string]string {
	out := map[string]string{}
	for field := range b.preview {
		if item, ok := b.predicted(field); ok && item.Status == api.Bound && item.BoundTo != nil {
			out[field] = *item.BoundTo
		}
	}
	return out
}

// confirmationEntries lists each rule field of rev the create does not
// produce: explicit, the preview's binding, or left to the server.
func (b *instantiateBindings) confirmationEntries(rev *rule_versions.RuleRevisionFull) []bindingEntry {
	fields := ruleMappedColumns(rev)
	sort.Strings(fields)
	entries := make([]bindingEntry, 0, len(fields))
	for _, field := range fields {
		if column, ok := b.explicit[field]; ok {
			entries = append(entries, bindingEntry{field: field, column: column, note: "explicit"})
			continue
		}
		if _, ok := b.produced[field]; ok {
			continue
		}
		item, ok := b.predicted(field)
		switch {
		case !ok:
			entries = append(entries, bindingEntry{field: field, note: "bound by the server"})
		case item.Status == api.Bound && item.BoundTo != nil:
			entries = append(entries, bindingEntry{field: field, column: *item.BoundTo, note: sourceText(item.Source)})
		default:
			entries = append(entries, bindingEntry{field: field, note: string(item.Status)})
		}
	}
	return entries
}

// refuseUnbound is the exit-2 refusal for a create whose preview binds a
// required field it leaves to the server to no column, or nil.
func (b *instantiateBindings) refuseUnbound(doc manifestDoc, datasetID int) error {
	unbound := b.unbound()
	if len(unbound) == 0 {
		return nil
	}
	return qerrors.NewBadUsage(fmt.Sprintf("%s: no column of table %d matches a required rule field the manifest leaves out of column_mapping, so the server would refuse the create; nothing was sent\n\n%s",
		doc.Source, datasetID, renderUnresolvedBindings(unbound)))
}

// previewAsBinding is item as a RuleFieldBindingSchema.
func previewAsBinding(item api.RuleFieldColumnCandidatesSchema) api.RuleFieldBindingSchema {
	return api.RuleFieldBindingSchema{
		RuleFieldName: item.RuleFieldName,
		Required:      item.Required,
		Status:        item.Status,
		BoundTo:       item.BoundTo,
		Source:        item.Source,
		Candidates:    item.Candidates,
	}
}

// unresolvedBindingsOf is the ambiguous fields and the required unbound
// fields of a RULE_FIELD_MAPPING_UNRESOLVED error in err's chain, and false
// when err carries no such error or none of those fields.
func unresolvedBindingsOf(err error) ([]api.RuleFieldBindingSchema, bool) {
	var opErr *apierror.OperationalError
	if !stderrors.As(err, &opErr) || opErr.ErrorCode != codeRuleFieldMappingUnresolved || len(opErr.Context) == 0 {
		return nil, false
	}
	var ctx struct {
		FieldBindings []api.RuleFieldBindingSchema `json:"field_bindings"`
	}
	if json.Unmarshal(opErr.Context, &ctx) != nil {
		return nil, false
	}
	var out []api.RuleFieldBindingSchema
	for _, binding := range ctx.FieldBindings {
		if binding.Status == api.Ambiguous || (binding.Status == api.Unbound && binding.Required) {
			out = append(out, binding)
		}
	}
	return out, len(out) > 0
}

// renderUnresolvedBindings names each binding's field and status, then a
// column_mapping block to paste under spec: one key per field with no value,
// and the field's candidate columns as comments, best first.
func renderUnresolvedBindings(bindings []api.RuleFieldBindingSchema) string {
	var b strings.Builder
	b.WriteString("Unresolved rule fields:\n")
	for _, binding := range bindings {
		need := "optional"
		if binding.Required {
			need = "required"
		}
		fmt.Fprintf(&b, "  %s (%s): %s\n", oneLine(binding.RuleFieldName), need, oneLine(string(binding.Status)))
	}
	b.WriteString("\nAdd each to spec.column_mapping with a column of the table; the comments are the candidate columns, best first:\n\n")
	b.WriteString("  column_mapping:\n")

	keys := make([]string, len(bindings))
	width := 0
	for i, binding := range bindings {
		keys[i] = "    " + yamlKey(binding.RuleFieldName) + ":"
		if len(keys[i]) > width {
			width = len(keys[i])
		}
	}
	for i, binding := range bindings {
		comments := candidateComments(binding.Candidates)
		for j, comment := range comments {
			key := ""
			if j == 0 {
				key = keys[i]
			}
			fmt.Fprintf(&b, "%-*s  # %s\n", width, key, comment)
		}
	}
	return terminal.Sanitize(strings.TrimRight(b.String(), "\n"))
}

// candidateComments is one comment per candidate, "column -- reason", or a
// single comment saying there is none.
func candidateComments(candidates []api.RuleFieldColumnCandidateSchema) []string {
	if len(candidates) == 0 {
		return []string{"no candidate: no unclaimed column has a type this field accepts"}
	}
	width := 0
	for _, candidate := range candidates {
		if n := len(oneLine(candidate.Column)); n > width {
			width = n
		}
	}
	out := make([]string, len(candidates))
	for i, candidate := range candidates {
		out[i] = fmt.Sprintf("%-*s  (%s)", width, oneLine(candidate.Column), oneLine(candidate.Reason))
	}
	return out
}

// oneLine is s sanitized, with line breaks and tabs as spaces, so it stays
// inside a YAML comment.
func oneLine(s string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(terminal.Sanitize(s))
}

// yamlKey is s as a YAML mapping key: plain when YAML reads it back as the
// same string, double-quoted otherwise.
func yamlKey(s string) string {
	if out, err := jsonnum.MarshalYAML(s); err == nil {
		if plain := strings.TrimSuffix(string(out), "\n"); plain == s && !strings.ContainsAny(s, "#\n") {
			return plain
		}
	}
	return strconv.Quote(s)
}

// bindingEntry is one rule field's binding as a line renders it.
type bindingEntry struct {
	field, column, note string
}

// formatBindingEntries renders entries as "field -> column [types] (note)",
// or "field (note)" for an entry with no column. accepted annotates a column
// with the field's accepted types.
func formatBindingEntries(entries []bindingEntry, accepted map[string]string) string {
	if len(entries) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		part := e.field
		if e.column != "" {
			part += " -> " + e.column
			if types := accepted[e.field]; types != "" {
				part += " [" + types + "]"
			}
		}
		if e.note != "" {
			part += " (" + e.note + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

// appliedBindingEntries lists a create's bindings by field: each field of
// sent as explicit, and each of bindings with its source. With bindings nil,
// each field of mapping outside sent and produced is listed without a source.
func appliedBindingEntries(sent map[string]string, bindings *[]api.RuleFieldBindingSchema, mapping map[string]string, produced map[string]bool) []bindingEntry {
	entries := make([]bindingEntry, 0, len(sent))
	for field, column := range sent {
		entries = append(entries, bindingEntry{field: field, column: column, note: "explicit"})
	}
	if bindings != nil {
		for _, binding := range *bindings {
			if _, ok := sent[binding.RuleFieldName]; ok {
				continue
			}
			if binding.Status == api.Bound && binding.BoundTo != nil {
				entries = append(entries, bindingEntry{field: binding.RuleFieldName, column: *binding.BoundTo, note: sourceText(binding.Source)})
			} else {
				entries = append(entries, bindingEntry{field: binding.RuleFieldName, note: string(binding.Status)})
			}
		}
	} else {
		for field, column := range mapping {
			if _, ok := sent[field]; !ok && !produced[field] {
				entries = append(entries, bindingEntry{field: field, column: column})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].field < entries[j].field })
	return entries
}

// producedFieldNames is the rule field names of a response's produced fields.
func producedFieldNames(fields *[]api.ProducedFieldResponseSchema) map[string]bool {
	names := map[string]bool{}
	for _, field := range dataset_rules.ProducedFieldsFromResponse(fields) {
		names[field.RuleFieldName] = true
	}
	return names
}

// sourceText is a binding source's wire value, or "bound" for none.
func sourceText(source *api.RuleFieldBindingSource) string {
	if source == nil || *source == "" {
		return "bound"
	}
	return string(*source)
}

// sortedKeys is m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
