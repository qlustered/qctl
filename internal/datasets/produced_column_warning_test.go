package datasets

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// secondProducerID is a rule other than producerID.
const secondProducerID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"

// producedDetail has one column per reason ProducedColumnEdits must look
// past: premium carries the rule_output stamp, booking_no carries none, and
// note carries a generated_by of another type. discount is stamped by a
// second rule.
func producedDetail(t *testing.T) *TableSchemaDetail {
	t.Helper()
	other := fieldDetail(t, "String", true)
	notARuleOutput := map[string]interface{}{"type": "something_else", "dataset_rule_id": producerID}
	other.GeneratedBy = &notARuleOutput
	return &TableSchemaDetail{
		DatasetName:    "orders",
		MigrationState: api.CurrentOnly,
		Fields: map[string]FieldSchemaInfo{
			"premium":    {Current: stamped(t, "Decimal", producerID, "premium")},
			"discount":   {Current: stamped(t, "Decimal", secondProducerID, "discount")},
			"booking_no": {Current: fieldDetail(t, "String", false)},
			"note":       {Current: other},
		},
	}
}

// Each negative pins a different reason the answer must be empty, so a
// predicate that warned on every stamped column, or on every edit, fails here.
func TestProducedColumnEdits(t *testing.T) {
	prop := func(name, old, updated string) SchemaPropChange {
		return SchemaPropChange{Property: name, Old: old, New: updated}
	}
	cases := []struct {
		name    string
		changes []SchemaFieldChange
		want    []ProducedColumnEdit
	}{
		{
			"a retype of a produced column",
			[]SchemaFieldChange{{Field: "premium", Props: []SchemaPropChange{prop("type", "Decimal", "Integer")}}},
			[]ProducedColumnEdit{{Field: "premium", RuleID: producerID, Props: []string{"type"}}},
		},
		{
			"a resize of a produced column",
			[]SchemaFieldChange{{Field: "premium", Props: []SchemaPropChange{prop("args", "[12,2]", "[10,2]")}}},
			[]ProducedColumnEdit{{Field: "premium", RuleID: producerID, Props: []string{"args"}}},
		},
		{
			"a delete of a produced column",
			[]SchemaFieldChange{{Field: "premium", Props: []SchemaPropChange{prop("is_deleted", "-", "true")}}},
			[]ProducedColumnEdit{{Field: "premium", RuleID: producerID, Props: []string{"is_deleted"}}},
		},
		{
			"only the properties that matter are named",
			[]SchemaFieldChange{{Field: "premium", Props: []SchemaPropChange{
				prop("type", "Decimal", "String"), prop("args", "[12,2]", "[64]"), prop("is_nullable", "true", "false"),
			}}},
			[]ProducedColumnEdit{{Field: "premium", RuleID: producerID, Props: []string{"type", "args"}}},
		},
		{
			"each column names its own rule",
			[]SchemaFieldChange{
				{Field: "discount", Props: []SchemaPropChange{prop("type", "Decimal", "Integer")}},
				{Field: "premium", Props: []SchemaPropChange{prop("type", "Decimal", "Integer")}},
			},
			[]ProducedColumnEdit{
				{Field: "discount", RuleID: secondProducerID, Props: []string{"type"}},
				{Field: "premium", RuleID: producerID, Props: []string{"type"}},
			},
		},
		{
			"an un-delete restores the column rather than breaking it",
			[]SchemaFieldChange{{Field: "premium", Props: []SchemaPropChange{prop("is_deleted", "true", "false")}}},
			nil,
		},
		{
			"a property the enable check does not read",
			[]SchemaFieldChange{{Field: "premium", Props: []SchemaPropChange{prop("is_nullable", "true", "false")}}},
			nil,
		},
		{
			"a reset carries no property change",
			[]SchemaFieldChange{{Field: "premium", Cleared: true}},
			nil,
		},
		{
			"a column no rule produces",
			[]SchemaFieldChange{{Field: "booking_no", Props: []SchemaPropChange{prop("type", "String", "Text")}}},
			nil,
		},
		{
			"a generated_by that is not a rule_output",
			[]SchemaFieldChange{{Field: "note", Props: []SchemaPropChange{prop("type", "String", "Text")}}},
			nil,
		},
		{
			"a column the schema does not have",
			[]SchemaFieldChange{{Field: "shipped_at", Added: true, Props: []SchemaPropChange{prop("type", "", "DateTime")}}},
			nil,
		},
	}
	detail := producedDetail(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ProducedColumnEdits(detail, tc.changes)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ProducedColumnEdits = %#v, want %#v", got, tc.want)
			}
			if warning := FormatProducedColumnWarning(got, nil, 7); (warning != "") != (len(tc.want) > 0) {
				t.Errorf("FormatProducedColumnWarning = %q for edits %v", warning, got)
			}
		})
	}
}

// The stamp is read from the definition the overlay edits: the draft's, while
// a draft exists. A stamp only the current definition carries is not it.
func TestProducedColumnEdits_ReadsTheDefinitionTheOverlayEdits(t *testing.T) {
	retype := []SchemaFieldChange{{Field: "premium", Props: []SchemaPropChange{{Property: "type", Old: "Decimal", New: "Integer"}}}}
	cases := []struct {
		name string
		info FieldSchemaInfo
		want bool
	}{
		{"stamped in the draft", FieldSchemaInfo{Current: fieldDetail(t, "Decimal", true), New: stamped(t, "Decimal", producerID, "premium")}, true},
		{"stamped only in current", FieldSchemaInfo{Current: stamped(t, "Decimal", producerID, "premium"), New: fieldDetail(t, "Decimal", true)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detail := &TableSchemaDetail{
				MigrationState: api.DraftWithCurrent,
				Fields:         map[string]FieldSchemaInfo{"premium": tc.info},
			}
			if got := len(ProducedColumnEdits(detail, retype)) > 0; got != tc.want {
				t.Errorf("edit found = %v, want %v", got, tc.want)
			}
		})
	}
}

// The change list is what BuildSchemaOverrides produces, so the property names
// and the "true" ProducedColumnEdits matches on must be the ones it writes.
func TestProducedColumnEdits_FromBuildSchemaOverrides(t *testing.T) {
	detail := producedDetail(t)
	_, changes, err := BuildSchemaOverrides(detail, specFromYAML(t, "    premium:\n      type: Decimal\n      is_deleted: true\n"), "f.yaml")
	if err != nil {
		t.Fatalf("BuildSchemaOverrides: %v", err)
	}
	want := []ProducedColumnEdit{{Field: "premium", RuleID: producerID, Props: []string{"is_deleted"}}}
	if got := ProducedColumnEdits(detail, changes); !reflect.DeepEqual(got, want) {
		t.Errorf("ProducedColumnEdits = %#v, want %#v", got, want)
	}
}

// A rule the lookup named is printed by name and ID; one it did not is
// printed by ID alone. Both names are the server's and are sanitized.
func TestFormatProducedColumnWarning(t *testing.T) {
	edits := []ProducedColumnEdit{
		{Field: "premium" + esc, RuleID: producerID, Props: []string{"type", "args"}},
		{Field: "flag", RuleID: secondProducerID, Props: []string{"is_deleted"}},
	}
	out := FormatProducedColumnWarning(edits, map[string]string{producerID: "enrich" + esc}, 7)
	for _, want := range []string{
		`premium\x1b[31m (type, args): produced by rule enrich\x1b[31m (` + producerID + ")",
		"flag (is_deleted): produced by rule " + secondProducerID + "\n",
		"409",
		"moves a pending_validation rule to validation_failed",
		"does not read args",
		"`qctl describe table-rule <rule-id> --table-id 7`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning is missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("a raw ESC reached the terminal: %q", out)
	}
}
