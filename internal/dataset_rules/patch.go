package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/pkg/piimask"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/terminal"
)

// Wire keys of the dataset-rule PATCH body.
const (
	patchKeyInstanceName   = "instance_name"
	patchKeyState          = "state"
	patchKeySeverity       = "severity"
	patchKeyParams         = "params"
	patchKeyColumnMapping  = "column_mapping_dict"
	patchKeyRuleRevision   = "rule_revision_id"
	patchKeyProducedFields = "produced_fields"
	patchKeyRowFilter      = "row_filter"
)

// BuildPatchMap builds a merge patch from the fields set in the manifest.
func BuildPatchMap(m *TableRuleApplyManifest) map[string]interface{} {
	patch := map[string]interface{}{}
	if m.RowFilterSet || m.Spec.RowFilter != nil {
		patch[patchKeyRowFilter] = m.Spec.RowFilter
	}
	if m.Spec.InstanceName != nil {
		patch[patchKeyInstanceName] = *m.Spec.InstanceName
	}
	if m.Spec.State != nil {
		patch[patchKeyState] = *m.Spec.State
	}
	if m.Spec.Severity != nil {
		patch[patchKeySeverity] = *m.Spec.Severity
	}
	if m.Spec.Params != nil {
		patch[patchKeyParams] = *m.Spec.Params
	}
	// The rebind. Sending it starts a revalidation candidate and returns 202;
	// the live rule keeps its current revision until that candidate's dry run
	// passes. spec.rule / spec.release resolve into this same field before
	// the patch map is built (ResolveManifestRule).
	if m.Spec.RuleRevisionID != "" {
		patch[patchKeyRuleRevision] = m.Spec.RuleRevisionID
	}
	if len(m.Spec.ColumnMapping) > 0 {
		patch[patchKeyColumnMapping] = m.Spec.ColumnMapping
	}
	// Produced fields are the one key whose EMPTY value is meaningful to the
	// backend: it replaces the whole declaration set with what it is sent, so
	// `[]` there means "declare nothing" while a `null` means "unchanged". The
	// pointer is what tells those apart and the dereference is what keeps the
	// `[]`, so this function encodes the manifest faithfully either way.
	//
	// It does not follow that `produced_fields: []` reaches the wire.
	// ReconcileProducedFieldsWithLive runs between this map and the request,
	// and refuses a manifest that drops a live declaration -- the backend
	// accepts such a patch and then cannot promote it. What is left for the
	// empty list is the manifest-sets-nothing check, which calls this before
	// the reconcile and has to see the key as set.
	if m.Spec.ProducedFields != nil {
		entries := *m.Spec.ProducedFields
		if entries == nil {
			entries = []ProducedFieldEntry{}
		}
		patch[patchKeyProducedFields] = entries
	}
	return patch
}

// patchableManifestFields names, in manifest spelling, every field
// BuildPatchMap understands. It has exactly one definition because both
// `qctl apply -f` and `qctl diff -f` report it when a patch manifest sets
// nothing patchable, and two hand-written copies drifted apart once already.
const patchableManifestFields = "spec.instance_name, spec.state, spec.severity, spec.params, spec.column_mapping, spec.produced_fields, spec.row_filter, spec.rule_revision_id or spec.rule+spec.release"

// ValidateManifestAPIVersion returns the shared bad-usage error for a
// TableRule manifest written for a schema version this CLI does not support.
func ValidateManifestAPIVersion(got, source string) error {
	if got == pkgmanifest.APIVersionV1 {
		return nil
	}
	return qerrors.NewBadUsagef("unsupported apiVersion %q in %s (expected: %s)", got, source, pkgmanifest.APIVersionV1)
}

// ValidatePatchManifest makes every verdict on a patch manifest that needs
// neither the server nor the rule revision to reach: the key that means
// nothing in patch mode, the key patch mode requires, the states the server
// sets and refuses to be told, and a spec.severity outside the enum.
//
// It has exactly one definition because `qctl apply -f` and `qctl diff -f`
// both call it, and a refusal only apply makes is worse than a missing check:
// diff prints a happy field-level preview for a manifest apply then rejects
// with exit 2, which is the one thing a preview must never do. That is what
// the first two checks here did on master, where they lived in apply alone. A new
// pre-request verdict on a patch manifest belongs here rather than in either
// command, so the two verbs cannot disagree about which manifests are legal.
func ValidatePatchManifest(manifest *TableRuleApplyManifest, source string) error {
	// spec.state on an instantiate manifest is silently dropped, which the
	// table-rule page records as a wart. A new field does not repeat it.
	if manifest.Spec.Column != "" {
		return qerrors.NewBadUsagef("spec.column is instantiate-only and has no effect in patch mode (metadata.id is set) in %s; use spec.column_mapping to change a binding", source)
	}
	if manifest.Spec.UpgradedFrom != "" {
		return qerrors.NewBadUsagef("spec.upgradedFrom is instantiate-only and has no effect in patch mode (metadata.id is set) in %s", source)
	}
	// diff sends no patch and could preview one without it. It is required
	// here anyway: diff previews what apply would do, and apply needs it in
	// the request path.
	if manifest.Spec.DatasetID == 0 {
		return qerrors.NewBadUsagef("spec.dataset_id is required for patch in %s", source)
	}
	// Mode-independent, so instantiate calls it separately: apply's
	// instantiate path never reaches this function.
	if err := ValidateManifestSeverity(manifest, source); err != nil {
		return err
	}
	if err := manifest.Spec.RowFilter.Validate(true, "spec.row_filter in "+source); err != nil {
		return err
	}
	if err := ValidateManifestParams(manifest, source); err != nil {
		return err
	}
	return refuseSystemSetState(manifest, source)
}

// ValidateManifestParams returns bad usage (exit 2) when spec.params holds
// masked text at any depth, naming each such path.
func ValidateManifestParams(manifest *TableRuleApplyManifest, source string) error {
	if manifest.Spec.Params == nil {
		return nil
	}
	paths := piimask.Paths("spec.params", *manifest.Spec.Params)
	if len(paths) == 0 {
		return nil
	}
	return qerrors.NewBadUsagef("%s holds masked text at %s: %s stands in for a value the server masks for you, "+
		"not the value itself, and applying it would store that text. Write the real values, or leave spec.params "+
		"out of the manifest",
		source, terminal.Sanitize(strings.Join(paths, ", ")), piimask.Mask)
}

// systemSetStates are the rule states the server sets itself and answers a
// requested one with a 422, even when the rule is already in that state
// (apply_dataset_rule_patch in capi/capi/controllers/dataset_rule_patch_controller.py
// refuses error and every state outside CLIENT_SETTABLE_RULE_STATES).
var systemSetStates = []api.DatasetRuleState{
	api.DatasetRuleStateError,
	api.DatasetRuleStatePendingValidation,
	api.DatasetRuleStateValidationFailed,
}

// refuseSystemSetState rejects a patch manifest whose spec.state is one of
// systemSetStates.
//
// This exists because of the round-trip, not because anyone types it: spec.state
// is not omitempty in the display manifest, so `qctl get table-rule -o yaml`
// always writes the rule's current state. Editing such a file to repair the
// rule -- a rebind of a crashed rule is exactly what `qctl alert resolve`
// advises on a severe_rule_bug -- then sends a key the caller never wrote, and
// the 422 blames a field they did not touch.
//
// Unexported so it cannot be called on its own: it is one of the checks
// ValidatePatchManifest makes, and a caller that reaches for this one alone is
// re-opening the drift that function exists to close.
func refuseSystemSetState(manifest *TableRuleApplyManifest, source string) error {
	if manifest.Spec.State == nil || !slices.Contains(systemSetStates, *manifest.Spec.State) {
		return nil
	}
	state := *manifest.Spec.State
	if state == api.DatasetRuleStateError {
		return qerrors.NewBadUsagef("spec.state: error in %s cannot be requested -- the server sets it when a rule crashes on a large share of a file. "+
			"Delete the key to patch this rule (a manifest round-tripped from `qctl get table-rule -o yaml` carries the rule's current state), "+
			"or set disabled or deleted to take it out of service", source)
	}
	return qerrors.NewBadUsagef("spec.state: %s in %s cannot be requested -- the server sets it when a rule's configuration needs validation or fails it. "+
		"Delete the key to patch this rule (a manifest round-tripped from `qctl get table-rule -o yaml` carries the rule's current state), "+
		"or set enabled, disabled or deleted", state, source)
}

// ReconcilePatchWithLive drops spec.instance_name when it equals the live
// rule's name and runs ReconcileProducedFieldsWithLive. It returns the names
// of the fields it dropped as restatements of the live rule.
func ReconcilePatchWithLive(manifest *TableRuleApplyManifest, detail *DatasetRuleDetail, source string) ([]string, error) {
	var restated []string
	if manifest.Spec.InstanceName != nil && *manifest.Spec.InstanceName == detail.InstanceName {
		// Not sent, because atlas does not read a restated name as a no-op.
		// The rename-collision 422 excludes the rule's own row, but on the
		// revalidation-candidate path the key's presence alone is a rename
		// (_start_revalidation_candidate, capi/capi/views/rule_view.py): it
		// writes the live rule and stamps the caller as its last editor, and
		// SplitPatchOutcome would then report instance_name as applied now.
		manifest.Spec.InstanceName = nil
		restated = append(restated, "spec."+patchKeyInstanceName)
	}
	declared := manifest.Spec.ProducedFields != nil
	if err := ReconcileProducedFieldsWithLive(manifest, detail, source); err != nil {
		return nil, err
	}
	if declared && manifest.Spec.ProducedFields == nil {
		restated = append(restated, "spec."+patchKeyProducedFields)
	}
	return restated, nil
}

// NoPatchableFieldsError is the bad-usage error (exit 2) for a patch manifest
// whose spec sets none of the patchable fields. source names the manifest.
func NoPatchableFieldsError(source string) error {
	return qerrors.NewBadUsagef("no patchable fields found in %s (supported: %s)", source, patchableManifestFields)
}

// stagedByDraftMigration are the keys a draft-migration 202 defers to a rule
// suggestion. Everything else in such a request reached the live rule.
var stagedByDraftMigration = []string{patchKeyParams, patchKeyState}

// revalidationTriggerKeys trigger on every promotable rule.
var revalidationTriggerKeys = []string{patchKeyRuleRevision, patchKeyColumnMapping, patchKeyProducedFields}

// candidateImmediateKeys are the unconditional live writes on a candidate.
var candidateImmediateKeys = []string{patchKeyInstanceName}

// displayKey maps a wire patch key to its manifest field name.
func displayKey(key string) string {
	if key == patchKeyColumnMapping {
		return "column_mapping"
	}
	return key
}

// SplitPatchOutcome reports applied and staged keys in manifest order.
// live identifies description-only filter edits applied on a candidate.
func SplitPatchOutcome(patch map[string]interface{}, isCandidate bool, live ...*DatasetRuleDetail) (appliedNow, staged []string) {
	immediate := presentKeysExcept(patch, stagedByDraftMigration)
	stagedKeys := stagedByDraftMigration
	if isCandidate {
		immediate = slices.Clone(candidateImmediateKeys)
		if len(live) > 0 && live[0] != nil {
			if filter, ok := patch[patchKeyRowFilter].(*row_filters.Filter); ok && row_filters.SameRows(filter, row_filters.FromAPI(live[0].RowFilter)) {
				immediate = append(immediate, patchKeyRowFilter)
			}
		}
		stagedKeys = presentKeysExcept(patch, immediate)
	}
	for _, k := range patchDisplayOrder {
		if _, ok := patch[k]; !ok {
			continue
		}
		if slices.Contains(immediate, k) {
			// A revalidation trigger on the draft-migration path cannot carry
			// a real change: the backend fires a trigger only for a field
			// whose requested value differs from the live rule, so a trigger
			// that reached this path was a restatement. It is therefore
			// neither applied nor staged — reporting it either way would name
			// a change that did not happen.
			if !isCandidate && slices.Contains(revalidationTriggerKeys, k) {
				continue
			}
			appliedNow = append(appliedNow, displayKey(k))
			continue
		}
		if slices.Contains(stagedKeys, k) {
			staged = append(staged, displayKey(k))
		}
	}
	return appliedNow, staged
}

// presentKeysExcept is every key the patch actually sets that is not in
// exclude, in patchDisplayOrder. A key absent from patchDisplayOrder is
// absent from the result, which is what makes the partition tripwire in
// patch_test.go bite when a new BuildPatchMap key is not ordered.
func presentKeysExcept(patch map[string]interface{}, exclude []string) []string {
	out := make([]string, 0, len(patch))
	for _, k := range patchDisplayOrder {
		if _, ok := patch[k]; !ok {
			continue
		}
		if !slices.Contains(exclude, k) {
			out = append(out, k)
		}
	}
	return out
}

// FieldChange is one patch key rendered against the live rule.
type FieldChange struct {
	Field   string // manifest display name (column_mapping, not column_mapping_dict)
	Old     string // compact JSON of the live value
	New     string // compact JSON of the patch value
	Changed bool   // Old != New after JSON normalization
}

// patchDisplayOrder fixes the rendering order of patch keys.
var patchDisplayOrder = []string{
	patchKeyInstanceName, patchKeyState, patchKeyRuleRevision, patchKeySeverity, patchKeyParams, patchKeyColumnMapping, patchKeyProducedFields, patchKeyRowFilter,
}

// PatchFieldChanges renders each patch key old -> new against the live
// detail. Equality is judged on the compact-JSON forms, which normalizes
// map ordering and numeric types.
func PatchFieldChanges(detail *DatasetRuleDetail, patch map[string]interface{}) []FieldChange {
	live := map[string]interface{}{
		patchKeyInstanceName:  detail.InstanceName,
		patchKeyState:         detail.State,
		patchKeySeverity:      detail.Severity,
		patchKeyParams:        detail.Params,
		patchKeyColumnMapping: detail.ColumnMappingDict,
		patchKeyRuleRevision:  detail.RuleRevision.ID.String(),
		patchKeyRowFilter:     row_filters.FromAPI(detail.RowFilter),
		// Rendered through the manifest's own shape, not the response's: the
		// live rows carry an id, timestamps and a server-stamped generated_by
		// that no request can express, and comparing those against a
		// manifest's three keys would report a change on every unedited
		// round-trip.
		patchKeyProducedFields: ProducedFieldsFromResponse(detail.ProducedFields),
	}
	var changes []FieldChange
	for _, key := range patchDisplayOrder {
		newVal, ok := patch[key]
		if !ok {
			continue
		}
		oldJSON := compactJSON(live[key])
		newJSON := compactJSON(newVal)
		changes = append(changes, FieldChange{
			Field:   displayKey(key),
			Old:     oldJSON,
			New:     newJSON,
			Changed: oldJSON != newJSON,
		})
	}
	return changes
}

// compactJSON renders v as one-line JSON ("<unencodable>" on error).
func compactJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<unencodable>"
	}
	return string(b)
}

// FormatFieldChanges renders "field : old -> new" lines for every entry,
// each prefixed with indent. This is the single source of truth for the
// diff format, shared by `qctl diff` and apply's confirmation block.
func FormatFieldChanges(changes []FieldChange, indent string) string {
	var b strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&b, "%s%-16s : %s -> %s\n", indent, c.Field, c.Old, c.New)
	}
	// Both callers print this straight to the terminal. Old and New arrive
	// JSON-encoded from compactJSON, so the escaping there already covers them;
	// Field is a raw server-supplied column name and is not covered by anything
	// else, which is why the whole block is sanitized rather than one argument.
	return terminal.Sanitize(b.String())
}
