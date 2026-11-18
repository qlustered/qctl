package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
)

// enrichingRevision is a rule that reads full_name and writes two new columns,
// declaring a schema for one of them and none for the other -- the split that
// separates "qctl fills field_schema in" from "qctl asks for it".
func enrichingRevision() *rule_versions.RuleRevisionFull {
	schemas := map[string]map[string]interface{}{
		"first_name": {"type": "String", "args": []interface{}{64}, "is_nullable": true},
	}
	return &rule_versions.RuleRevisionFull{
		ID:                   uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
		Release:              "1.0.0",
		InputColumns:         []string{"full_name"},
		EnrichesColumns:      []string{"first_name", "last_name"},
		CorrectsColumns:      []string{"full_name_fixed"},
		EnrichedFieldSchemas: &schemas,
	}
}

// producedManifest is an instantiate manifest declaring the given entries.
func producedManifest(entries []ProducedFieldEntry) *TableRuleApplyManifest {
	m := &TableRuleApplyManifest{APIVersion: "qluster.ai/v1", Kind: "TableRule"}
	m.Spec.DatasetID = 1
	m.Spec.RuleRevisionID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"
	m.Spec.ProducedFields = &entries
	return m
}

// assertBadUsage fails unless err is a client-side validation failure (exit 2)
// naming want. The exit code is asserted rather than the message alone,
// because a bare fmt.Errorf in a new validator degrades to exit 1 while still
// reading perfectly.
func assertBadUsage(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error mentioning %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestResolveProducedFields_FillsFieldSchemaFromTheRevision(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{RuleFieldName: "first_name", DatasetFieldName: "customer_first"}})

	if err := ResolveProducedFields(m, enrichingRevision(), "f.yaml"); err != nil {
		t.Fatalf("expected the revision's declaration to be taken, got: %v", err)
	}
	got := (*m.Spec.ProducedFields)[0].FieldSchema
	if got["type"] != "String" {
		t.Errorf("field_schema = %v, want the revision's String declaration", got)
	}
}

// The filled schema must be a copy: mutating it later (or resolving a second
// manifest) must not write through to the fetched revision.
func TestResolveProducedFields_DoesNotAliasTheRevisionsMap(t *testing.T) {
	rev := enrichingRevision()
	m := producedManifest([]ProducedFieldEntry{{RuleFieldName: "first_name", DatasetFieldName: "customer_first"}})
	if err := ResolveProducedFields(m, rev, "f.yaml"); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	(*m.Spec.ProducedFields)[0].FieldSchema["type"] = "Text"

	if (*rev.EnrichedFieldSchemas)["first_name"]["type"] != "String" {
		t.Error("resolving wrote through to the rule revision's own schema map")
	}
}

// generated_by is server-stamped and ignored on the way in, so a revision that
// carries one must not seed it back into the request.
func TestResolveProducedFields_DropsGeneratedByFromTheDefault(t *testing.T) {
	rev := enrichingRevision()
	(*rev.EnrichedFieldSchemas)["first_name"]["generated_by"] = testutil.RuleOutputStamp(
		"550e8400-e29b-41d4-a716-446655440000", rev.ID.String(), "first_name")
	m := producedManifest([]ProducedFieldEntry{{RuleFieldName: "first_name", DatasetFieldName: "customer_first"}})

	if err := ResolveProducedFields(m, rev, "f.yaml"); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if _, ok := (*m.Spec.ProducedFields)[0].FieldSchema["generated_by"]; ok {
		t.Error("generated_by survived into the request's field_schema")
	}
}

// A manifest can restate the stamp -- copied out of `describe table-rule -vv`,
// say. The server would overwrite it, but qctl does not lean on that: the
// stamp is dropped and the rest of the stated definition is kept.
func TestResolveProducedFields_DropsAGeneratedByTheManifestStates(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema: map[string]interface{}{
			"type": "Text",
			"generated_by": testutil.RuleOutputStamp(
				"550e8400-e29b-41d4-a716-446655440000", "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "first_name"),
		},
	}})

	if err := ResolveProducedFields(m, enrichingRevision(), "f.yaml"); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	got := (*m.Spec.ProducedFields)[0].FieldSchema
	if _, ok := got["generated_by"]; ok {
		t.Errorf("generated_by survived into the request's field_schema: %v", got)
	}
	if got["type"] != "Text" {
		t.Errorf("field_schema = %v, want the stated Text kept", got)
	}
}

// A field_schema holding nothing but the stamp states no definition, so it is
// filled from the revision exactly as an omitted one is.
func TestResolveProducedFields_AGeneratedByAloneIsAnOmittedFieldSchema(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema: map[string]interface{}{
			"generated_by": testutil.RuleOutputStamp(
				"550e8400-e29b-41d4-a716-446655440000", "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "first_name"),
		},
	}})

	if err := ResolveProducedFields(m, enrichingRevision(), "f.yaml"); err != nil {
		t.Fatalf("expected the revision's declaration to be taken, got: %v", err)
	}
	got := (*m.Spec.ProducedFields)[0].FieldSchema
	if _, ok := got["generated_by"]; ok {
		t.Errorf("generated_by survived into the request's field_schema: %v", got)
	}
	if got["type"] != "String" {
		t.Errorf("field_schema = %v, want the revision's String declaration", got)
	}
}

func TestResolveProducedFields_KeepsAnExplicitFieldSchema(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "Text"},
	}})

	if err := ResolveProducedFields(m, enrichingRevision(), "f.yaml"); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got := (*m.Spec.ProducedFields)[0].FieldSchema["type"]; got != "Text" {
		t.Errorf("field_schema.type = %v, want the manifest's own Text", got)
	}
}

func TestResolveProducedFields_NoDeclaredSchemaIsRejected(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{RuleFieldName: "last_name", DatasetFieldName: "customer_last"}})

	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, "field_schema is required")
}

func TestResolveProducedFields_CorrectsColumnIsAnAllowedOutput(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "full_name_fixed",
		DatasetFieldName: "full_name_clean",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}})

	if err := ResolveProducedFields(m, enrichingRevision(), "f.yaml"); err != nil {
		t.Fatalf("a corrects_columns field must be produceable, got: %v", err)
	}
}

func TestResolveProducedFields_UnknownRuleFieldNamesTheAllowedSet(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "middle_name",
		DatasetFieldName: "customer_middle",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}})

	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, "allowed: [first_name, full_name_fixed, last_name]")
}

// An input column is not an output: mapping it is how you bind an existing
// column, and producing it would claim the rule creates what it reads.
func TestResolveProducedFields_InputColumnIsNotAnOutput(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "full_name",
		DatasetFieldName: "customer_name",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}})

	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, "is not one of the rule revision's output fields")
}

// The wildcard names no single column, so it can never be produced -- and the
// error must not offer it as an option.
func TestResolveProducedFields_WildcardIsNeverAllowed(t *testing.T) {
	rev := enrichingRevision()
	rev.EnrichesColumns = []string{"*"}
	rev.CorrectsColumns = nil
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "*",
		DatasetFieldName: "anything",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}})

	err := ResolveProducedFields(m, rev, "f.yaml")
	assertBadUsage(t, err, "writes no columns")
}

func TestResolveProducedFields_ValidatesColumnIsNotAnOutput(t *testing.T) {
	rev := enrichingRevision()
	rev.ValidatesColumns = []string{"checked"}
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "checked",
		DatasetFieldName: "checked_col",
		FieldSchema:      map[string]interface{}{"type": "Boolean"},
	}})

	err := ResolveProducedFields(m, rev, "f.yaml")
	assertBadUsage(t, err, "is not one of the rule revision's output fields")
}

func TestResolveProducedFields_DuplicateRuleFieldRejected(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "a", FieldSchema: map[string]interface{}{"type": "String"}},
		{RuleFieldName: "first_name", DatasetFieldName: "b", FieldSchema: map[string]interface{}{"type": "String"}},
	})

	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, `rule_field_name "first_name" twice`)
}

func TestResolveProducedFields_DuplicateDatasetFieldRejected(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "same", FieldSchema: map[string]interface{}{"type": "String"}},
		{RuleFieldName: "last_name", DatasetFieldName: "same", FieldSchema: map[string]interface{}{"type": "String"}},
	})

	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, `dataset_field_name "same" twice`)
}

func TestResolveProducedFields_BlankNamesRejected(t *testing.T) {
	cases := []struct {
		name  string
		entry ProducedFieldEntry
		want  string
	}{
		{"no rule field", ProducedFieldEntry{DatasetFieldName: "x"}, "rule_field_name is required"},
		{"no dataset field", ProducedFieldEntry{RuleFieldName: "first_name"}, "dataset_field_name is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ResolveProducedFields(producedManifest([]ProducedFieldEntry{tc.entry}), enrichingRevision(), "f.yaml")
			assertBadUsage(t, err, tc.want)
		})
	}
}

func TestResolveProducedFields_UnknownFieldTypeRejected(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "Stringg"},
	}})

	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, `"Stringg" is not a column type`)
}

// A revision that declares a schema with no type cannot supply a default that
// the server would accept, so the local check has to fire on it too.
func TestResolveProducedFields_DefaultWithoutATypeIsRejected(t *testing.T) {
	rev := enrichingRevision()
	(*rev.EnrichedFieldSchemas)["last_name"] = map[string]interface{}{"is_nullable": true}
	m := producedManifest([]ProducedFieldEntry{{RuleFieldName: "last_name", DatasetFieldName: "customer_last"}})

	err := ResolveProducedFields(m, rev, "f.yaml")
	assertBadUsage(t, err, "field_schema.type is required")
}

func TestResolveProducedFields_NonStringTypeRejected(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": 7},
	}})

	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, "field_schema.type must be a string")
}

// A fresh instantiate cannot bind a column it is about to create.
func TestResolveProducedFields_InstantiateRefusesAnyMappingEntry(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}})
	m.Spec.ColumnMapping = map[string]string{"first_name": "customer_first"}

	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, "spec.column_mapping must not bind")
}

func TestResolveProducedFields_UpgradeAllowsMatchingPredecessorMapping(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}})
	m.Spec.UpgradedFrom = "550e8400-e29b-41d4-a716-446655440000"
	m.Spec.ColumnMapping = map[string]string{"first_name": "customer_first"}
	if err := ResolveProducedFields(m, enrichingRevision(), "f.yaml"); err != nil {
		t.Fatalf("matching upgrade output rejected: %v", err)
	}
	m.Spec.ColumnMapping["first_name"] = "other_column"
	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, "drop one of the two")
}

// On patch the same key is what a round-tripped manifest carries: the server
// auto-extends the live mapping with each produced binding, so restating it is
// normal and only a contradiction is an error.
func TestResolveProducedFields_PatchAllowsAnAgreeingMappingEntry(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}})
	m.Metadata.ID = "550e8400-e29b-41d4-a716-446655440000"
	m.Spec.ColumnMapping = map[string]string{"first_name": "customer_first"}

	if err := ResolveProducedFields(m, enrichingRevision(), "f.yaml"); err != nil {
		t.Fatalf("an agreeing mapping entry must be accepted on patch, got: %v", err)
	}
}

func TestResolveProducedFields_PatchRejectsADisagreeingMappingEntry(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}})
	m.Metadata.ID = "550e8400-e29b-41d4-a716-446655440000"
	m.Spec.ColumnMapping = map[string]string{"first_name": "somewhere_else"}

	err := ResolveProducedFields(m, enrichingRevision(), "f.yaml")
	assertBadUsage(t, err, "drop one of the two")
}

// Sorting is what makes the patch body and the diff stable: the manifest's
// order is the author's, and comparing it against a sorted live set would
// report a change on a reordered file.
func TestResolveProducedFields_SortsByRuleFieldName(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{
		{RuleFieldName: "last_name", DatasetFieldName: "b", FieldSchema: map[string]interface{}{"type": "String"}},
		{RuleFieldName: "first_name", DatasetFieldName: "a", FieldSchema: map[string]interface{}{"type": "String"}},
	})

	if err := ResolveProducedFields(m, enrichingRevision(), "f.yaml"); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got := (*m.Spec.ProducedFields)[0].RuleFieldName; got != "first_name" {
		t.Errorf("first entry = %q, want first_name", got)
	}
}

// A manifest that says nothing about produced fields must never need a rule
// revision -- passing nil is how the caller says it fetched none.
func TestResolveProducedFields_AbsentNeedsNoRevision(t *testing.T) {
	m := &TableRuleApplyManifest{}
	if err := ResolveProducedFields(m, nil, "f.yaml"); err != nil {
		t.Errorf("an absent spec.produced_fields must resolve to nothing, got: %v", err)
	}
}

// The clear is an instruction about declarations that already exist; it names
// no rule field, so it needs no revision either.
func TestResolveProducedFields_ClearNeedsNoRevision(t *testing.T) {
	m := producedManifest([]ProducedFieldEntry{})
	if err := ResolveProducedFields(m, nil, "f.yaml"); err != nil {
		t.Errorf("`produced_fields: []` must resolve to nothing, got: %v", err)
	}
}

func TestNeedsRuleRevision(t *testing.T) {
	empty := []ProducedFieldEntry{}
	declared := []ProducedFieldEntry{{RuleFieldName: "first_name", DatasetFieldName: "customer_first"}}
	params := map[string]interface{}{"threshold": 1}
	cases := []struct {
		name string
		set  func(m *TableRuleApplyManifest)
		want bool
	}{
		{"nothing", func(*TableRuleApplyManifest) {}, false},
		{"a rename", func(m *TableRuleApplyManifest) { n := "x"; m.Spec.InstanceName = &n }, false},
		{"params", func(m *TableRuleApplyManifest) { m.Spec.Params = &params }, true},
		{"produced fields", func(m *TableRuleApplyManifest) { m.Spec.ProducedFields = &declared }, true},
		// The clear names no rule output field, so there is nothing for a
		// revision to answer about it.
		{"the produced-field clear", func(m *TableRuleApplyManifest) { m.Spec.ProducedFields = &empty }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &TableRuleApplyManifest{}
			tc.set(m)
			if got := NeedsRuleRevision(m); got != tc.want {
				t.Errorf("NeedsRuleRevision = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPatchRevisionID_PrefersTheRebindTarget(t *testing.T) {
	detail := &DatasetRuleDetail{RuleRevision: api.RuleRevisionSmallSchema{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111")}}
	m := &TableRuleApplyManifest{}

	if got := PatchRevisionID(m, detail); got != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("without a rebind the live revision governs, got %q", got)
	}
	m.Spec.RuleRevisionID = "22222222-2222-2222-2222-222222222222"
	if got := PatchRevisionID(m, detail); got != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("with a rebind the target governs, got %q", got)
	}
}

func TestProducedFieldsFromResponse_DropsServerOwnedKeysAndSorts(t *testing.T) {
	fields := []api.ProducedFieldResponseSchema{
		{
			ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
			RuleFieldName:    "last_name",
			DatasetFieldName: "customer_last",
			FieldSchema: map[string]interface{}{
				"type":         "String",
				"generated_by": map[string]interface{}{"type": "rule_output"},
			},
		},
		{
			ID:               uuid.MustParse("44444444-4444-4444-4444-444444444444"),
			RuleFieldName:    "first_name",
			DatasetFieldName: "customer_first",
			FieldSchema:      map[string]interface{}{"type": "String"},
		},
	}

	got, err := json.Marshal(ProducedFieldsFromResponse(&fields))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"rule_field_name":"first_name","dataset_field_name":"customer_first","field_schema":{"type":"String"}},` +
		`{"rule_field_name":"last_name","dataset_field_name":"customer_last","field_schema":{"type":"String"}}]`
	if string(got) != want {
		t.Errorf("round-trip shape =\n%s\nwant\n%s", got, want)
	}
}

// A rule with no produced fields renders as `[]`, not `null`: the live side of
// the diff is compared against a manifest's `produced_fields: []`, and a null
// would report clearing nothing as a change.
func TestProducedFieldsFromResponse_EmptyIsNotNull(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields *[]api.ProducedFieldResponseSchema
	}{
		{"absent", nil},
		{"empty", &[]api.ProducedFieldResponseSchema{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(ProducedFieldsFromResponse(tc.fields))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != "[]" {
				t.Errorf("rendered %s, want []", got)
			}
		})
	}
}

func TestFormatProducedFields(t *testing.T) {
	if got := FormatProducedFields(nil); got != "(none)" {
		t.Errorf("no declarations = %q, want (none)", got)
	}
	entries := []ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "String"}},
		{RuleFieldName: "last_name", DatasetFieldName: "customer_last"},
	}
	want := "first_name -> customer_first (String), last_name -> customer_last"
	if got := FormatProducedFields(entries); got != want {
		t.Errorf("FormatProducedFields = %q, want %q", got, want)
	}
}

// liveDetail is a rule whose server-side declarations are the given entries.
// The response shape carries the server-owned keys a real answer carries --
// ids and timestamps -- because the reconcile has to see past them.
func liveDetail(entries ...api.ProducedFieldResponseSchema) *DatasetRuleDetail {
	d := &DatasetRuleDetail{}
	if entries != nil {
		d.ProducedFields = &entries
	}
	return d
}

func liveField(ruleField, datasetField string, schema map[string]interface{}) api.ProducedFieldResponseSchema {
	return api.ProducedFieldResponseSchema{
		ID:               uuid.MustParse("dddd0000-bbbb-cccc-dddd-eeeeeeee0001"),
		RuleFieldName:    ruleField,
		DatasetFieldName: datasetField,
		FieldSchema:      schema,
	}
}

// patchManifest is a patch manifest (metadata.id set) declaring the given
// entries, which is the only mode the reconcile runs in.
func patchManifest(entries *[]ProducedFieldEntry) *TableRuleApplyManifest {
	m := &TableRuleApplyManifest{APIVersion: "qluster.ai/v1", Kind: "TableRule"}
	m.Metadata.ID = "cccc0000-bbbb-cccc-dddd-eeeeeeee0001"
	m.Spec.DatasetID = 1
	m.Spec.ProducedFields = entries
	return m
}

// A manifest that says nothing about produced fields is not an instruction
// about them, so the reconcile leaves the absent key absent.
func TestReconcileProducedFields_AbsentIsLeftAlone(t *testing.T) {
	m := patchManifest(nil)
	live := liveDetail(liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}))

	if err := ReconcileProducedFieldsWithLive(m, live, "f.yaml"); err != nil {
		t.Fatalf("an absent key must reconcile to nothing, got: %v", err)
	}
	if m.Spec.ProducedFields != nil {
		t.Errorf("spec.produced_fields = %+v, want it left absent", *m.Spec.ProducedFields)
	}
}

// The restatement `get table-rule -o yaml | apply -f` sends is dropped from the
// body: forwarding it would re-merge into the table's draft migration and open
// one when none exists, with no review alert, which is a side effect for a
// request the user reads as a no-op.
func TestReconcileProducedFields_ExactRestatementIsDropped(t *testing.T) {
	m := patchManifest(&[]ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "String"}},
	})
	live := liveDetail(liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}))

	if err := ReconcileProducedFieldsWithLive(m, live, "f.yaml"); err != nil {
		t.Fatalf("restating the live declarations must be accepted, got: %v", err)
	}
	if m.Spec.ProducedFields != nil {
		t.Errorf("spec.produced_fields = %+v, want the key dropped from the body", *m.Spec.ProducedFields)
	}
}

// The server stamps a generated_by block into field_schema that no manifest
// carries, so comparing the raw schemas would read every round-trip as a
// retype and refuse it. Round-tripping through `get -o yaml` is the documented
// way to edit a rule, so this is the common case rather than a corner.
func TestReconcileProducedFields_ServerStampedGeneratedByIsNotARetype(t *testing.T) {
	m := patchManifest(&[]ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "String"}},
	})
	live := liveDetail(liveField("first_name", "customer_first", map[string]interface{}{
		"type":         "String",
		"generated_by": map[string]interface{}{"rule_field_name": "first_name"},
	}))

	if err := ReconcileProducedFieldsWithLive(m, live, "f.yaml"); err != nil {
		t.Fatalf("a server-stamped generated_by must not read as a retype, got: %v", err)
	}
	if m.Spec.ProducedFields != nil {
		t.Errorf("spec.produced_fields = %+v, want the key dropped: only generated_by differs", *m.Spec.ProducedFields)
	}
}

// Declaring a column the rule does not write yet is the one change the backend
// can land, so it is sent exactly as written.
func TestReconcileProducedFields_AddIsSentUnchanged(t *testing.T) {
	declared := []ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "String"}},
		{RuleFieldName: "last_name", DatasetFieldName: "customer_last", FieldSchema: map[string]interface{}{"type": "String"}},
	}
	m := patchManifest(&declared)
	live := liveDetail(liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}))

	if err := ReconcileProducedFieldsWithLive(m, live, "f.yaml"); err != nil {
		t.Fatalf("adding an output field must be accepted, got: %v", err)
	}
	if m.Spec.ProducedFields == nil || len(*m.Spec.ProducedFields) != 2 {
		t.Errorf("spec.produced_fields = %v, want both entries sent unchanged", m.Spec.ProducedFields)
	}
}

// Pointing an output field at a column the table does not have is an add by
// another name: the migration carries the new column, and that promotion arm
// tolerates the old one being dropped along with it.
func TestReconcileProducedFields_RenameOntoAnotherColumnIsSent(t *testing.T) {
	declared := []ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "given_name", FieldSchema: map[string]interface{}{"type": "String"}},
	}
	m := patchManifest(&declared)
	live := liveDetail(liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}))

	if err := ReconcileProducedFieldsWithLive(m, live, "f.yaml"); err != nil {
		t.Fatalf("renaming an output field onto another column must be accepted, got: %v", err)
	}
	if m.Spec.ProducedFields == nil || (*m.Spec.ProducedFields)[0].DatasetFieldName != "given_name" {
		t.Errorf("spec.produced_fields = %v, want the rename sent unchanged", m.Spec.ProducedFields)
	}
}

// Dropping one of two declarations adds no column, so the candidate is
// promoted through the arm that refuses a drop -- accepted at 202, passing its
// dry run, then stranded pending forever. Refused here instead.
func TestReconcileProducedFields_DropIsRefused(t *testing.T) {
	declared := []ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "String"}},
	}
	m := patchManifest(&declared)
	live := liveDetail(
		liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}),
		liveField("last_name", "customer_last", map[string]interface{}{"type": "String"}),
	)

	err := ReconcileProducedFieldsWithLive(m, live, "f.yaml")
	assertBadUsage(t, err, "drops last_name")
}

// `produced_fields: []` against a rule that declares something is the same
// drop, written the shortest way. It is the spelling the branch first shipped
// as a supported clear, so it is pinned separately from the partial drop.
func TestReconcileProducedFields_ClearOfALiveDeclarationIsRefused(t *testing.T) {
	m := patchManifest(&[]ProducedFieldEntry{})
	live := liveDetail(liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}))

	err := ReconcileProducedFieldsWithLive(m, live, "f.yaml")
	assertBadUsage(t, err, "drops first_name")
}

// `produced_fields: []` against a rule that declares nothing changes nothing,
// so it is dropped rather than refused: there is no drop to refuse.
func TestReconcileProducedFields_ClearOfNothingIsDropped(t *testing.T) {
	m := patchManifest(&[]ProducedFieldEntry{})

	if err := ReconcileProducedFieldsWithLive(m, liveDetail(), "f.yaml"); err != nil {
		t.Fatalf("clearing what was never declared must be accepted, got: %v", err)
	}
	if m.Spec.ProducedFields != nil {
		t.Errorf("spec.produced_fields = %+v, want the key dropped from the body", *m.Spec.ProducedFields)
	}
}

// Retyping a column the rule already writes names no new column either, so it
// dies at promotion the same way a drop does -- pinned backend-side by
// TestAReDeclaredProducedFieldIsNotPromoted.
func TestReconcileProducedFields_RetypeIsRefused(t *testing.T) {
	declared := []ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "Integer"}},
	}
	m := patchManifest(&declared)
	live := liveDetail(liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}))

	err := ReconcileProducedFieldsWithLive(m, live, "f.yaml")
	assertBadUsage(t, err, "redeclares first_name")
}

// A paused rule cannot open a revalidation candidate, so Atlas replaces its
// declarations directly and never reaches the promotion gate that strands
// these three shapes for every promotable state.
func TestReconcileProducedFields_PausedRuleSendsReplacementUnchanged(t *testing.T) {
	tests := []struct {
		name     string
		declared []ProducedFieldEntry
		live     []api.ProducedFieldResponseSchema
	}{
		{
			name: "drop",
			declared: []ProducedFieldEntry{
				{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "String"}},
			},
			live: []api.ProducedFieldResponseSchema{
				liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}),
				liveField("last_name", "customer_last", map[string]interface{}{"type": "String"}),
			},
		},
		{
			name:     "clear",
			declared: []ProducedFieldEntry{},
			live: []api.ProducedFieldResponseSchema{
				liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}),
			},
		},
		{
			name: "retype",
			declared: []ProducedFieldEntry{
				{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "Integer"}},
			},
			live: []api.ProducedFieldResponseSchema{
				liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := patchManifest(&tc.declared)
			live := liveDetail(tc.live...)
			live.State = api.DatasetRuleStateDisabled
			want, err := json.Marshal(m.Spec.ProducedFields)
			if err != nil {
				t.Fatalf("marshal input: %v", err)
			}

			if err := ReconcileProducedFieldsWithLive(m, live, "f.yaml"); err != nil {
				t.Fatalf("paused-rule replacement must be accepted, got: %v", err)
			}
			got, err := json.Marshal(m.Spec.ProducedFields)
			if err != nil {
				t.Fatalf("marshal output: %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("spec.produced_fields = %s, want it sent unchanged as %s", got, want)
			}
		})
	}
}

// A retype riding along with an add is sent: the add is what stages the
// migration, and promoting a migration is the arm that tolerates the rest.
// Refusing the whole request here would block the only path that works.
func TestReconcileProducedFields_RetypeRidingWithAnAddIsSent(t *testing.T) {
	declared := []ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "Integer"}},
		{RuleFieldName: "last_name", DatasetFieldName: "customer_last", FieldSchema: map[string]interface{}{"type": "String"}},
	}
	m := patchManifest(&declared)
	live := liveDetail(liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}))

	if err := ReconcileProducedFieldsWithLive(m, live, "f.yaml"); err != nil {
		t.Fatalf("a retype that rides with an add must be sent, got: %v", err)
	}
	if m.Spec.ProducedFields == nil || len(*m.Spec.ProducedFields) != 2 {
		t.Errorf("spec.produced_fields = %v, want both entries sent unchanged", m.Spec.ProducedFields)
	}
}

// A drop and a retype in one manifest are reported together, so the caller
// fixes the manifest once rather than one field per run.
func TestReconcileProducedFields_DropAndRetypeAreReportedTogether(t *testing.T) {
	declared := []ProducedFieldEntry{
		{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: map[string]interface{}{"type": "Integer"}},
	}
	m := patchManifest(&declared)
	live := liveDetail(
		liveField("first_name", "customer_first", map[string]interface{}{"type": "String"}),
		liveField("last_name", "customer_last", map[string]interface{}{"type": "String"}),
	)

	err := ReconcileProducedFieldsWithLive(m, live, "f.yaml")
	assertBadUsage(t, err, "drops last_name")
	assertBadUsage(t, err, "redeclares first_name")
}
