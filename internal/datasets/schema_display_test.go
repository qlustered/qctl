package datasets

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func fieldDetailFromJSON(t *testing.T, s string) *FieldSchemaDetail {
	t.Helper()
	var d FieldSchemaDetail
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("bad field fixture: %v", err)
	}
	return &d
}

func schemaDetailFromJSON(t *testing.T, s string) *TableSchemaDetail {
	t.Helper()
	var d TableSchemaDetail
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("bad schema fixture: %v", err)
	}
	return &d
}

func TestFormatFieldType(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"plain type", `{"type": "integer"}`, "integer"},
		{"type with one arg", `{"type": "varchar", "args": [255]}`, "varchar(255)"},
		{"type with two args", `{"type": "numeric", "args": [10, 2]}`, "numeric(10,2)"},
		{"union fallback non-string", `{"type": 42}`, "42"},
		{"empty type", `{}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatFieldType(fieldDetailFromJSON(t, tt.json))
			if got != tt.want {
				t.Errorf("FormatFieldType() = %q, want %q", got, tt.want)
			}
		})
	}
	if got := FormatFieldType(nil); got != "" {
		t.Errorf("FormatFieldType(nil) = %q, want empty", got)
	}
}

func TestSchemaFieldsToDisplay_DraftWithCurrent(t *testing.T) {
	detail := schemaDetailFromJSON(t, schemaDetailDraftJSON)
	rows := SchemaFieldsToDisplay(detail)

	// Sorted by field name, ascending.
	wantOrder := []string{"amount", "id", "legacy_code", "name"}
	if len(rows) != len(wantOrder) {
		t.Fatalf("got %d rows, want %d", len(rows), len(wantOrder))
	}
	for i, name := range wantOrder {
		if rows[i].Field != name {
			t.Errorf("rows[%d].Field = %q, want %q", i, rows[i].Field, name)
		}
	}

	byName := map[string]SchemaFieldDisplay{}
	for _, r := range rows {
		byName[r.Field] = r
	}

	// Draft exists: rows show the new definition.
	if byName["name"].Type != "varchar(255)" {
		t.Errorf("name.Type = %q, want varchar(255)", byName["name"].Type)
	}
	// Removed fields fall back to the current definition.
	if byName["legacy_code"].Type != "varchar(10)" {
		t.Errorf("legacy_code.Type = %q, want varchar(10)", byName["legacy_code"].Type)
	}
	if byName["legacy_code"].Action != "removed" {
		t.Errorf("legacy_code.Action = %q, want removed", byName["legacy_code"].Action)
	}
	// Fields without an action are "unchanged".
	if byName["id"].Action != "unchanged" {
		t.Errorf("id.Action = %q, want unchanged", byName["id"].Action)
	}
	if byName["amount"].Action != "added" {
		t.Errorf("amount.Action = %q, want added", byName["amount"].Action)
	}
	// Verbose fields.
	if byName["id"].Indexed != "true" {
		t.Errorf("id.Indexed = %q, want true", byName["id"].Indexed)
	}
	if byName["legacy_code"].OriginalName != "Legacy Code" {
		t.Errorf("legacy_code.OriginalName = %q, want %q", byName["legacy_code"].OriginalName, "Legacy Code")
	}
	if byName["amount"].Flags != "dollar" {
		t.Errorf("amount.Flags = %q, want dollar", byName["amount"].Flags)
	}
	if byName["id"].Flags != "-" {
		t.Errorf("id.Flags = %q, want -", byName["id"].Flags)
	}
}

// From atlas f14f1f145 (#1041) a pending delete is a soft delete: the draft
// keeps the column with is_deleted set, and migration-details reports it as
// removed carrying BOTH current and new. The row must therefore show the
// draft's definition -- its forced-nullable true and its deleted flag -- not
// the current one. A field the draft no longer carries at all still falls back
// to current, which is the other arm of displayDefinition.
func TestSchemaFieldsToDisplay_SoftDeleteShowsTheDraftDefinition(t *testing.T) {
	detail := schemaDetailFromJSON(t, `{
	  "dataset_id": 42,
	  "dataset_name": "orders",
	  "diff_summary": {"removed_count": 2, "total_fields": 2},
	  "fields": {
	    "soft_deleted": {
	      "action": "removed",
	      "current": {"type": "varchar", "args": [10], "is_nullable": false, "is_deleted": false},
	      "new": {"type": "varchar", "args": [10], "is_nullable": true, "is_deleted": true},
	      "diffs": [
	        {"property": "is_nullable", "old_value": false, "new_value": true},
	        {"property": "is_deleted", "old_value": false, "new_value": true}
	      ]
	    },
	    "gone_from_draft": {
	      "action": "removed",
	      "current": {"type": "varchar", "args": [10], "is_nullable": false}
	    }
	  },
	  "migration_state": "draft_with_current",
	  "state": "draft"
	}`)

	byName := map[string]SchemaFieldDisplay{}
	for _, r := range SchemaFieldsToDisplay(detail) {
		byName[r.Field] = r
	}

	soft := byName["soft_deleted"]
	if soft.Action != "removed" {
		t.Errorf("soft_deleted.Action = %q, want removed", soft.Action)
	}
	if soft.Nullable != "true" {
		t.Errorf("soft_deleted.Nullable = %q, want true (the draft's, not the current false)", soft.Nullable)
	}
	if soft.Flags != "deleted" {
		t.Errorf("soft_deleted.Flags = %q, want deleted", soft.Flags)
	}

	gone := byName["gone_from_draft"]
	if gone.Action != "removed" {
		t.Errorf("gone_from_draft.Action = %q, want removed", gone.Action)
	}
	if gone.Nullable != "false" {
		t.Errorf("gone_from_draft.Nullable = %q, want false (falls back to current)", gone.Nullable)
	}
	if gone.Flags != "-" {
		t.Errorf("gone_from_draft.Flags = %q, want -", gone.Flags)
	}
}

func TestSchemaFieldsToDisplay_CurrentOnlyUsesCurrent(t *testing.T) {
	detail := schemaDetailFromJSON(t, `{
	  "confirmed": true,
	  "created_at": "2026-07-01T10:00:00Z",
	  "updated_at": "2026-07-02T10:00:00Z",
	  "dataset_id": 42,
	  "dataset_name": "orders",
	  "diff_summary": {"total_fields": 1},
	  "fields": {
	    "name": {"current": {"type": "varchar", "args": [255], "is_nullable": false}}
	  },
	  "migration_id": 5,
	  "migration_model_version_id": 9,
	  "settings_model_version_id": 12,
	  "migration_state": "current_only",
	  "state": "current"
	}`)
	rows := SchemaFieldsToDisplay(detail)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Type != "varchar(255)" {
		t.Errorf("Type = %q, want varchar(255)", rows[0].Type)
	}
	if rows[0].Nullable != "false" {
		t.Errorf("Nullable = %q, want false", rows[0].Nullable)
	}
}

func TestSchemaFieldsToDisplay_RuleOutputFollowsDisplayedDefinition(t *testing.T) {
	stamp := `{"type":"rule_output","dataset_rule_id":"21deef3f-4165-4d2b-b65c-c12eaf5bd67c","rule_revision_id":"71deef3f-4165-4d2b-b65c-c12eaf5bd67c","rule_field_name":"amount"}`
	detail := schemaDetailFromJSON(t, `{
	  "fields": {
	    "draft_owner": {"current": {"type": "Decimal"}, "new": {"type": "Decimal", "is_dollar": true, "generated_by": `+stamp+`}},
	    "current_owner": {"current": {"type": "Decimal", "generated_by": `+stamp+`}, "new": {"type": "Decimal"}},
	    "fallback_owner": {"current": {"type": "Decimal", "generated_by": `+stamp+`}},
	    "other_origin": {"new": {"type": "Decimal", "generated_by": {"type": "inferred", "dataset_rule_id": "21deef3f-4165-4d2b-b65c-c12eaf5bd67c"}}},
	    "unowned": {"new": {"type": "Decimal"}}
	  },
	  "migration_state": "draft_with_current"
	}`)
	want := map[string]string{
		"draft_owner":    "dollar, rule_output",
		"current_owner":  "-",
		"fallback_owner": "rule_output",
		"other_origin":   "-",
		"unowned":        "-",
	}
	for _, row := range SchemaFieldsToDisplay(detail) {
		if row.Flags != want[row.Field] {
			t.Errorf("%s flags = %q, want %q", row.Field, row.Flags, want[row.Field])
		}
	}
	detail.MigrationState = "current_only"
	for _, row := range SchemaFieldsToDisplay(detail) {
		if row.Field == "current_owner" && row.Flags != "rule_output" {
			t.Errorf("current_owner flags with no draft = %q, want rule_output", row.Flags)
		}
	}
}

// display_decimals is an int the backend leaves null for "the kind's default",
// so the grid must keep a stated 0 apart from an unset value -- the same
// distinction the manifest keeps with its *int.
func TestSchemaFieldsToDisplay_DisplayDecimals(t *testing.T) {
	detail := schemaDetailFromJSON(t, `{
	  "dataset_id": 42,
	  "dataset_name": "orders",
	  "fields": {
	    "whole":   {"current": {"type": "Decimal", "is_dollar": true, "display_decimals": 0}},
	    "precise": {"current": {"type": "Decimal", "is_percent": true, "display_decimals": 3}},
	    "default": {"current": {"type": "Decimal", "is_dollar": true}},
	    "bare":    {}
	  },
	  "migration_state": "current_only",
	  "state": "current"
	}`)
	got := map[string]string{}
	for _, r := range SchemaFieldsToDisplay(detail) {
		got[r.Field] = r.DisplayDecimals
	}
	want := map[string]string{"whole": "0", "precise": "3", "default": "-", "bare": "-"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DisplayDecimals by field = %v, want %v", got, want)
	}
}

func TestSchemaGridColumns(t *testing.T) {
	draft := schemaDetailFromJSON(t, schemaDetailDraftJSON)
	currentOnly := &TableSchemaDetail{MigrationState: "current_only"}

	if got := SchemaGridColumns(currentOnly, 0); got != "field,type,nullable" {
		t.Errorf("current_only cols = %q", got)
	}
	if got := SchemaGridColumns(draft, 0); got != "field,type,nullable,action" {
		t.Errorf("draft cols = %q", got)
	}
	if got := SchemaGridColumns(draft, 1); got != "field,type,nullable,action,indexed,original_name,datetime_formats,display_decimals,flags" {
		t.Errorf("draft -v cols = %q", got)
	}
	if got := SchemaGridColumns(currentOnly, 1); got != "field,type,nullable,indexed,original_name,datetime_formats,display_decimals,flags" {
		t.Errorf("current_only -v cols = %q", got)
	}
}

func TestSchemaStructuredPayload_NotableValues(t *testing.T) {
	detail := schemaDetailFromJSON(t, schemaDetailDraftJSON)

	stripped, err := SchemaStructuredPayload(detail, false)
	if err != nil {
		t.Fatalf("SchemaStructuredPayload() error = %v", err)
	}
	fields := stripped.(map[string]interface{})["fields"].(map[string]interface{})
	amount := fields["amount"].(map[string]interface{})
	if _, ok := amount["notable_values"]; ok {
		t.Error("notable_values present in default payload; want stripped")
	}

	full, err := SchemaStructuredPayload(detail, true)
	if err != nil {
		t.Fatalf("SchemaStructuredPayload() error = %v", err)
	}
	fields = full.(map[string]interface{})["fields"].(map[string]interface{})
	amount = fields["amount"].(map[string]interface{})
	if _, ok := amount["notable_values"]; !ok {
		t.Error("notable_values missing from verbose payload")
	}
}

func TestSchemaStructuredPayload_YAMLSafe(t *testing.T) {
	detail := schemaDetailFromJSON(t, schemaDetailDraftJSON)
	payload, err := SchemaStructuredPayload(detail, true)
	if err != nil {
		t.Fatalf("SchemaStructuredPayload() error = %v", err)
	}
	out, err := yaml.Marshal(payload)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	s := string(out)
	// The union types must render as their string content, never byte arrays.
	if !strings.Contains(s, "type: varchar") {
		t.Errorf("yaml output missing 'type: varchar':\n%s", s)
	}
	if strings.Contains(s, "- 118") { // 118 = 'v' — a byte-array rendering of "varchar"
		t.Errorf("yaml output contains byte arrays:\n%s", s)
	}
}

func TestFormatSchemaHeader(t *testing.T) {
	detail := schemaDetailFromJSON(t, schemaDetailDraftJSON)
	out := FormatSchemaHeader(detail)
	for _, want := range []string{
		"Table:        orders (id: 42)",
		"Schema state: draft_with_current",
		"State:        draft (not confirmed)",
		"Fields:       4 total (+1 added, ~1 modified, -1 removed)",
		"Created:",
		"Updated:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q; got:\n%s", want, out)
		}
	}
}

func TestFormatSchemaHeader_CurrentOnlyOmitsDiffCounts(t *testing.T) {
	detail := schemaDetailFromJSON(t, `{
	  "confirmed": true,
	  "created_at": "2026-07-01T10:00:00Z",
	  "updated_at": "2026-07-02T10:00:00Z",
	  "dataset_id": 42,
	  "dataset_name": "orders",
	  "diff_summary": {"total_fields": 2},
	  "fields": {},
	  "migration_id": 5,
	  "migration_model_version_id": 9,
	  "settings_model_version_id": 12,
	  "migration_state": "current_only",
	  "state": "current"
	}`)
	out := FormatSchemaHeader(detail)
	if !strings.Contains(out, "Fields:       2 total\n") {
		t.Errorf("header should show bare total; got:\n%s", out)
	}
	if !strings.Contains(out, "State:        current (confirmed)") {
		t.Errorf("header missing confirmed state; got:\n%s", out)
	}
}

func TestFormatSchemaChanges(t *testing.T) {
	detail := schemaDetailFromJSON(t, schemaDetailDraftJSON)
	out := FormatSchemaChanges(detail)
	for _, want := range []string{
		"Field: amount (added)",
		"Field: legacy_code (removed)",
		"Field: name (modified)",
		"current: varchar(100) nullable=false",
		"new:     varchar(255) nullable=false",
		"args: [100] → [255]",
		"migration_id:               7",
		"migration_model_version_id: 11",
		"settings_model_version_id:  13",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("changes missing %q; got:\n%s", want, out)
		}
	}
	// Unchanged fields are not listed.
	if strings.Contains(out, "Field: id") {
		t.Errorf("changes should skip unchanged fields; got:\n%s", out)
	}
}
