package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// marshalPatch marshals the map; encoding/json sorts map keys, so the
// output is deterministic and byte-comparable.
func marshalPatch(t *testing.T, patch map[string]interface{}) string {
	t.Helper()
	b, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	return string(b)
}

func TestValidateManifestAPIVersion(t *testing.T) {
	if err := ValidateManifestAPIVersion("qluster.ai/v1", "rule.yaml"); err != nil {
		t.Fatalf("current apiVersion should pass: %v", err)
	}

	err := ValidateManifestAPIVersion("v2-bad", "rule.yaml")
	if err == nil {
		t.Fatal("unsupported apiVersion should fail")
	}
	want := `unsupported apiVersion "v2-bad" in rule.yaml (expected: qluster.ai/v1)`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestBuildPatchMap_RenameOnly(t *testing.T) {
	m := &TableRuleApplyManifest{}
	name := "renamed_rule"
	m.Spec.InstanceName = &name

	got := marshalPatch(t, BuildPatchMap(m))
	want := `{"instance_name":"renamed_rule"}`
	if got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

func TestBuildPatchMap_ParamsOnly(t *testing.T) {
	m := &TableRuleApplyManifest{}
	params := map[string]interface{}{"threshold": 0.95}
	m.Spec.Params = &params

	got := marshalPatch(t, BuildPatchMap(m))
	want := `{"params":{"threshold":0.95}}`
	if got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

func TestBuildPatchMap_ColumnMappingOnly(t *testing.T) {
	m := &TableRuleApplyManifest{}
	m.Spec.ColumnMapping = map[string]string{"email": "email_col"}

	got := marshalPatch(t, BuildPatchMap(m))
	want := `{"column_mapping_dict":{"email":"email_col"}}`
	if got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

func TestBuildPatchMap_MultiField(t *testing.T) {
	m := &TableRuleApplyManifest{}
	name := "renamed_rule"
	state := DatasetRuleState("disabled")
	severity := RuleSeverity("blocker")
	m.Spec.InstanceName = &name
	m.Spec.State = &state
	m.Spec.Severity = &severity

	got := marshalPatch(t, BuildPatchMap(m))
	// json.Marshal sorts map keys alphabetically.
	want := `{"instance_name":"renamed_rule","severity":"blocker","state":"disabled"}`
	if got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

func TestBuildPatchMap_EmptyManifest(t *testing.T) {
	m := &TableRuleApplyManifest{}
	if got := BuildPatchMap(m); len(got) != 0 {
		t.Errorf("expected empty patch map, got %v", got)
	}
}

func TestBuildPatchMap_EmptyColumnMappingOmitted(t *testing.T) {
	m := &TableRuleApplyManifest{}
	m.Spec.ColumnMapping = map[string]string{}
	if got := BuildPatchMap(m); len(got) != 0 {
		t.Errorf("empty column_mapping must be omitted, got %v", got)
	}
}

// Both lists come out in patchDisplayOrder, not map-iteration order, so the
// rendered "applied now" / "staged" lines are stable across runs.
func TestSplitPatchOutcome_StableDisplayOrder(t *testing.T) {
	patch := map[string]interface{}{
		"instance_name":       "x",
		"column_mapping_dict": map[string]string{"a": "b"},
		"params":              map[string]interface{}{"t": 1},
		"state":               "disabled",
	}
	appliedNow, staged := SplitPatchOutcome(patch, false)
	wantApplied := []string{"instance_name"}
	wantStaged := []string{"state", "params"}
	if fmt.Sprintf("%v", appliedNow) != fmt.Sprintf("%v", wantApplied) {
		t.Errorf("appliedNow = %v, want %v", appliedNow, wantApplied)
	}
	if fmt.Sprintf("%v", staged) != fmt.Sprintf("%v", wantStaged) {
		t.Errorf("staged = %v, want %v", staged, wantStaged)
	}
}

func TestSplitPatchOutcome_OnlyPresentKeys(t *testing.T) {
	patch := map[string]interface{}{"params": map[string]interface{}{"t": 1}}
	appliedNow, staged := SplitPatchOutcome(patch, false)
	if len(appliedNow) != 0 {
		t.Errorf("appliedNow = %v, want empty", appliedNow)
	}
	if len(staged) != 1 || staged[0] != "params" {
		t.Errorf("staged = %v, want [params]", staged)
	}
}

func TestSplitPatchOutcome_PartitionsAllBuildPatchMapKeys(t *testing.T) {
	// A manifest that sets every patchable field, so BuildPatchMap emits
	// every key it can. SplitPatchOutcome must account for all of them: if a
	// future patch key is added to BuildPatchMap but not to
	// patchDisplayOrder, it would silently vanish from the staged block.
	// Counting keys catches exactly that regression.
	//
	// The candidate path is the one that partitions: it is the only 202 in
	// which every emitted key lands in exactly one list. On the
	// draft-migration path column_mapping is deliberately in neither, so
	// counting there would assert the opposite of what that path promises.
	instanceName := "x"
	state := DatasetRuleState("disabled")
	severity := RuleSeverity("blocker")
	params := map[string]interface{}{"t": 1}
	m := &TableRuleApplyManifest{}
	m.Spec.InstanceName = &instanceName
	m.Spec.State = &state
	m.Spec.Severity = &severity
	m.Spec.Params = &params
	m.Spec.ColumnMapping = map[string]string{"a": "b"}
	m.Spec.RuleRevisionID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0002"
	produced := []ProducedFieldEntry{{RuleFieldName: "out", DatasetFieldName: "out_col", FieldSchema: map[string]interface{}{"type": "String"}}}
	m.Spec.ProducedFields = &produced

	patch := BuildPatchMap(m)
	appliedNow, staged := SplitPatchOutcome(patch, true)
	if len(appliedNow)+len(staged) != len(patch) {
		t.Errorf("SplitPatchOutcome accounted for %d keys but BuildPatchMap emitted %d; every patch key must land in exactly one split list\n  appliedNow=%v\n  staged=%v\n  patch=%v",
			len(appliedNow)+len(staged), len(patch), appliedNow, staged, patch)
	}
}

func sampleDetailForDiff() *DatasetRuleDetail {
	return &DatasetRuleDetail{
		InstanceName:      "email_check",
		State:             "enabled",
		Severity:          "warning",
		Params:            map[string]interface{}{"threshold": 0.9},
		ColumnMappingDict: map[string]string{"email": "email_col"},
	}
}

func TestPatchFieldChanges_RenderedAndOrdered(t *testing.T) {
	patch := map[string]interface{}{
		"params":        map[string]interface{}{"threshold": 0.95},
		"instance_name": "renamed",
	}
	changes := PatchFieldChanges(sampleDetailForDiff(), patch)
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d: %v", len(changes), changes)
	}
	// Stable order: instance_name before params.
	if changes[0].Field != "instance_name" || changes[1].Field != "params" {
		t.Errorf("wrong order: %v", changes)
	}
	if changes[0].Old != `"email_check"` || changes[0].New != `"renamed"` || !changes[0].Changed {
		t.Errorf("instance_name change = %+v", changes[0])
	}
	if changes[1].Old != `{"threshold":0.9}` || changes[1].New != `{"threshold":0.95}` || !changes[1].Changed {
		t.Errorf("params change = %+v", changes[1])
	}
}

func TestPatchFieldChanges_NoOpKeyIsUnchanged(t *testing.T) {
	patch := map[string]interface{}{"instance_name": "email_check"}
	changes := PatchFieldChanges(sampleDetailForDiff(), patch)
	if len(changes) != 1 || changes[0].Changed {
		t.Errorf("same-value key must be Changed=false: %v", changes)
	}
}

func TestPatchFieldChanges_ColumnMappingDisplayName(t *testing.T) {
	patch := map[string]interface{}{
		"column_mapping_dict": map[string]string{"email": "other_col"},
	}
	changes := PatchFieldChanges(sampleDetailForDiff(), patch)
	if len(changes) != 1 || changes[0].Field != "column_mapping" {
		t.Errorf("expected display name column_mapping: %v", changes)
	}
	if !changes[0].Changed {
		t.Errorf("expected Changed=true: %+v", changes[0])
	}
}

func TestFormatFieldChanges(t *testing.T) {
	changes := []FieldChange{
		{Field: "instance_name", Old: `"a"`, New: `"b"`, Changed: true},
	}
	out := FormatFieldChanges(changes, "")
	want := `instance_name    : "a" -> "b"` + "\n"
	if out != want {
		t.Errorf("FormatFieldChanges = %q, want %q", out, want)
	}
	indented := FormatFieldChanges(changes, "  ")
	wantIndented := `  instance_name    : "a" -> "b"` + "\n"
	if indented != wantIndented {
		t.Errorf("FormatFieldChanges indented = %q, want %q", indented, wantIndented)
	}
}

// The field NAME is the uncovered half: Old and New arrive JSON-encoded, so a
// control character in a value is already escaped by the time it gets here,
// while a column name is interpolated raw. Both callers print the result
// straight to the terminal.
func TestFormatFieldChangesEscapesControlCharactersInTheFieldName(t *testing.T) {
	changes := []FieldChange{
		{Field: "name\x1b[31m", Old: `"a"`, New: `"b"`, Changed: true},
	}
	out := FormatFieldChanges(changes, "")
	if strings.Contains(out, "\x1b") {
		t.Errorf("a raw ESC reached the terminal: %q", out)
	}
	if !strings.Contains(out, `\x1b[31m`) {
		t.Errorf("the control must stay visible, got: %q", out)
	}
}

func TestBuildPatchMap_RuleRevisionOnly(t *testing.T) {
	m := &TableRuleApplyManifest{}
	m.Spec.RuleRevisionID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0002"

	got := marshalPatch(t, BuildPatchMap(m))
	want := `{"rule_revision_id":"aaaa0000-bbbb-cccc-dddd-eeeeeeee0002"}`
	if got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

// A patch manifest with no revision must not send the key at all — an
// explicit null would read as a broad reconfiguration.
func TestBuildPatchMap_OmitsRuleRevisionWhenUnset(t *testing.T) {
	m := &TableRuleApplyManifest{}
	name := "renamed"
	m.Spec.InstanceName = &name

	if got := marshalPatch(t, BuildPatchMap(m)); strings.Contains(got, "rule_revision_id") {
		t.Errorf("patch body must omit rule_revision_id: %s", got)
	}
}

func TestPatchFieldChanges_RendersRevisionRebind(t *testing.T) {
	detail := &DatasetRuleDetail{
		RuleRevision: api.RuleRevisionSmallSchema{ID: uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")},
	}
	patch := map[string]interface{}{"rule_revision_id": "aaaa0000-bbbb-cccc-dddd-eeeeeeee0002"}

	changes := PatchFieldChanges(detail, patch)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Field != "rule_revision_id" {
		t.Errorf("field = %q, want rule_revision_id", changes[0].Field)
	}
	if !changes[0].Changed {
		t.Errorf("a different revision must render as changed: %+v", changes[0])
	}
}

// Restating the revision the rule already runs is not a rebind. The backend
// agrees (_changed_baked_fields produces no trigger), so the preview must not
// promise a candidate.
func TestPatchFieldChanges_SameRevisionIsUnchanged(t *testing.T) {
	same := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	detail := &DatasetRuleDetail{RuleRevision: api.RuleRevisionSmallSchema{ID: same}}
	patch := map[string]interface{}{"rule_revision_id": same.String()}

	changes := PatchFieldChanges(detail, patch)
	if len(changes) != 1 || changes[0].Changed {
		t.Errorf("restating the live revision must not be a change: %+v", changes)
	}
}

// On the candidate path the backend leaves every field of the live rule
// except instance_name unchanged until the dry run passes. Claiming
// column_mapping was "applied now" names a change that did not happen — the
// bug this replaces.
func TestSplitPatchOutcome_CandidateStagesEverythingButInstanceName(t *testing.T) {
	patch := map[string]interface{}{
		"instance_name":       "renamed",
		"column_mapping_dict": map[string]string{"email": "email_col"},
		"rule_revision_id":    "aaaa0000-bbbb-cccc-dddd-eeeeeeee0002",
		"params":              map[string]interface{}{"threshold": 0.9},
	}

	appliedNow, staged := SplitPatchOutcome(patch, true)

	if len(appliedNow) != 1 || appliedNow[0] != "instance_name" {
		t.Errorf("applied now = %v, want [instance_name] only", appliedNow)
	}
	for _, want := range []string{"column_mapping", "rule_revision_id", "params"} {
		if !slices.Contains(staged, want) {
			t.Errorf("staged missing %q, got %v", want, staged)
		}
	}
}

// The draft-migration 202 stages params/state and applies the rest.
func TestSplitPatchOutcome_DraftMigrationStagesParamsAndState(t *testing.T) {
	patch := map[string]interface{}{
		"instance_name": "renamed",
		"severity":      "blocker",
		"params":        map[string]interface{}{"threshold": 0.9},
		"state":         "disabled",
	}

	appliedNow, staged := SplitPatchOutcome(patch, false)

	for _, want := range []string{"instance_name", "severity"} {
		if !slices.Contains(appliedNow, want) {
			t.Errorf("applied now missing %q, got %v", want, appliedNow)
		}
	}
	if !slices.Contains(staged, "params") || !slices.Contains(staged, "state") {
		t.Errorf("staged = %v, want params and state", staged)
	}
}

// column_mapping on the non-candidate path cannot represent a real change —
// a real one would have produced a candidate — so it is not claimed.
func TestSplitPatchOutcome_DraftMigrationDoesNotClaimColumnMapping(t *testing.T) {
	patch := map[string]interface{}{
		"column_mapping_dict": map[string]string{"email": "email_col"},
		"params":              map[string]interface{}{"threshold": 0.9},
	}

	appliedNow, _ := SplitPatchOutcome(patch, false)

	if slices.Contains(appliedNow, "column_mapping") {
		t.Errorf("column_mapping is a revalidation trigger and must not be reported as applied: %v", appliedNow)
	}
}

// The same reasoning as column_mapping, for the other trigger field qctl can
// send. REVALIDATION_TRIGGER_FIELDS is {rule_revision_id, column_mapping_dict,
// produced_fields} (atlas
// capi/capi/controllers/rule_revalidation_view_controller.py), and
// revalidation_triggers fires only on a field whose requested value DIFFERS
// from the live rule (via _changed_baked_fields against
// _base_configuration(candidate=nil)). So a manifest that restates
// the live revision alongside a params change produces no trigger, takes the
// direct path, and with a draft migration open comes back as a
// draft-migration 202 whose patch map still carries rule_revision_id --
// where claiming it "applied now" names a change that did not happen.
func TestSplitPatchOutcome_DraftMigrationDoesNotClaimRuleRevision(t *testing.T) {
	patch := map[string]interface{}{
		"rule_revision_id": "aaaa0000-bbbb-cccc-dddd-eeeeeeee0002",
		"params":           map[string]interface{}{"threshold": 0.9},
	}

	appliedNow, staged := SplitPatchOutcome(patch, false)

	if slices.Contains(appliedNow, "rule_revision_id") {
		t.Errorf("rule_revision_id is a revalidation trigger and must not be reported as applied: %v", appliedNow)
	}
	// Nor staged: a draft-migration 202 defers params/state only, so claiming
	// the revision is pending review would be the mirror-image false report.
	if slices.Contains(staged, "rule_revision_id") {
		t.Errorf("a draft-migration 202 stages nothing about the revision: %v", staged)
	}
}

func TestBuildPatchMap_ProducedFieldsOnly(t *testing.T) {
	m := &TableRuleApplyManifest{}
	produced := []ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}}
	m.Spec.ProducedFields = &produced

	got := marshalPatch(t, BuildPatchMap(m))
	want := `{"produced_fields":[{"rule_field_name":"first_name","dataset_field_name":"customer_first","field_schema":{"type":"String"}}]}`
	if got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

// An empty list is encoded as `[]`, not as a JSON null: the backend reads an
// absent key and a null alike as "leave the declarations alone", so a nil
// slice here would encode the request as its own opposite. Nothing sends this
// body today -- ReconcileProducedFieldsWithLive either drops the key or
// refuses the manifest before the request is built -- but BuildPatchMap is
// also what the manifest-sets-nothing check counts, so it has to see the key
// as set and encode it correctly if it ever is sent.
func TestBuildPatchMap_EmptyProducedFieldsEncodesAsAnEmptyList(t *testing.T) {
	for _, tc := range []struct {
		name     string
		produced []ProducedFieldEntry
	}{
		{"empty literal", []ProducedFieldEntry{}},
		{"nil slice behind the pointer", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &TableRuleApplyManifest{}
			produced := tc.produced
			m.Spec.ProducedFields = &produced

			got := marshalPatch(t, BuildPatchMap(m))
			want := `{"produced_fields":[]}`
			if got != want {
				t.Errorf("patch body = %s, want exactly %s", got, want)
			}
		})
	}
}

func TestBuildPatchMap_OmitsProducedFieldsWhenUnset(t *testing.T) {
	m := &TableRuleApplyManifest{}
	name := "renamed"
	m.Spec.InstanceName = &name

	if _, ok := BuildPatchMap(m)["produced_fields"]; ok {
		t.Error("a manifest that says nothing about produced fields must not send the key")
	}
}

// The live side is rendered through the manifest's shape, so re-sending what
// the rule already declares reads as unchanged rather than as a rewrite.
func TestPatchFieldChanges_RestatedProducedFieldsAreUnchanged(t *testing.T) {
	detail := sampleDetailForDiff()
	detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema: map[string]interface{}{
			"type":         "String",
			"generated_by": map[string]interface{}{"type": "rule_output"},
		},
	}}
	patch := map[string]interface{}{"produced_fields": []ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}}}

	changes := PatchFieldChanges(detail, patch)
	if len(changes) != 1 {
		t.Fatalf("expected one change row, got %d: %v", len(changes), changes)
	}
	if changes[0].Field != "produced_fields" {
		t.Errorf("field = %q, want produced_fields", changes[0].Field)
	}
	if changes[0].Changed {
		t.Errorf("restating the live declarations must read as unchanged: %+v", changes[0])
	}
}

func TestPatchFieldChanges_ProducedFieldsChange(t *testing.T) {
	detail := sampleDetailForDiff()
	patch := map[string]interface{}{"produced_fields": []ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}}}

	changes := PatchFieldChanges(detail, patch)
	if len(changes) != 1 || !changes[0].Changed {
		t.Fatalf("adding a declaration to a rule that has none is a change: %v", changes)
	}
	if changes[0].Old != "[]" {
		t.Errorf("old = %s, want [] (a rule with no declarations, not null)", changes[0].Old)
	}
}

// Clearing a rule that declares nothing changes nothing, and `qctl diff -f`
// exits 0 on that only if the two sides render identically.
func TestPatchFieldChanges_ClearingNothingIsUnchanged(t *testing.T) {
	patch := map[string]interface{}{"produced_fields": []ProducedFieldEntry{}}

	changes := PatchFieldChanges(sampleDetailForDiff(), patch)
	if len(changes) != 1 || changes[0].Changed {
		t.Errorf("clearing an empty declaration set must read as unchanged: %v", changes)
	}
}

// produced_fields is a revalidation trigger, so a candidate 202 stages it and
// a draft-migration 202 claims it in neither list.
func TestSplitPatchOutcome_ProducedFieldsRideTheCandidate(t *testing.T) {
	patch := map[string]interface{}{"produced_fields": []ProducedFieldEntry{}}

	appliedNow, staged := SplitPatchOutcome(patch, true)
	if len(appliedNow) != 0 || !slices.Contains(staged, "produced_fields") {
		t.Errorf("candidate 202: appliedNow=%v staged=%v, want produced_fields staged", appliedNow, staged)
	}

	appliedNow, staged = SplitPatchOutcome(patch, false)
	if slices.Contains(appliedNow, "produced_fields") || slices.Contains(staged, "produced_fields") {
		t.Errorf("draft-migration 202: appliedNow=%v staged=%v, want produced_fields in neither", appliedNow, staged)
	}
}

// The server sets error, pending_validation and validation_failed itself and
// answers a requested one with a 422, so each is refused before the request
// with a message naming the key to delete.
func TestValidatePatchManifest_RefusesEverySystemSetState(t *testing.T) {
	for _, tc := range []struct {
		state api.DatasetRuleState
		hint  string
	}{
		{api.DatasetRuleStateError, "set disabled or deleted"},
		{api.DatasetRuleStatePendingValidation, "set enabled, disabled or deleted"},
		{api.DatasetRuleStateValidationFailed, "set enabled, disabled or deleted"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			m := &TableRuleApplyManifest{}
			m.Spec.DatasetID = 1
			state := tc.state
			m.Spec.State = &state

			err := ValidatePatchManifest(m, "rule.yaml")
			if err == nil {
				t.Fatalf("spec.state: %s must be refused", tc.state)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			for _, want := range []string{"spec.state: " + string(tc.state) + " in rule.yaml cannot be requested", "Delete the key", tc.hint} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error must contain %q, got: %v", want, err)
				}
			}
		})
	}
}

// The refusal must not widen past the system-set states: these are the three
// a client may request.
func TestValidatePatchManifest_AcceptsEveryClientSettableState(t *testing.T) {
	for _, state := range []api.DatasetRuleState{
		api.DatasetRuleStateEnabled,
		api.DatasetRuleStateDisabled,
		api.DatasetRuleStateDeleted,
	} {
		t.Run(string(state), func(t *testing.T) {
			m := &TableRuleApplyManifest{}
			m.Spec.DatasetID = 1
			s := state
			m.Spec.State = &s
			if err := ValidatePatchManifest(m, "rule.yaml"); err != nil {
				t.Errorf("spec.state: %s must pass, got: %v", state, err)
			}
		})
	}
}
