package datasets

import (
	"strings"
	"testing"
)

const keyStepRequiredJSON = `{
  "confirmed": false,
  "created_at": "2026-11-10T10:00:00Z",
  "updated_at": "2026-11-11T10:00:00Z",
  "dataset_id": 42,
  "dataset_name": "orders",
  "diff_summary": {"added_count": 3, "total_fields": 3},
  "fields": {},
  "migration_id": 7,
  "migration_model_version_id": 1,
  "settings_model_version_id": 1,
  "migration_state": "draft_only",
  "state": "draft",
  "key_step": "required",
  "key_columns": null,
  "key_column_candidates": {
    "rows_checked": 1200,
    "whole_file": true,
    "candidates": [
      {"columns": ["policy_id"], "duplicate_rows": 0, "identical_rows": 0, "blank_rows": 0},
      {"columns": ["policy_ref", "line_no"], "duplicate_rows": 6, "identical_rows": 4, "blank_rows": 3}
    ],
    "key_columns_counts": null
  }
}`

func TestFormatKeyColumnCandidates(t *testing.T) {
	detail := schemaDetailFromJSON(t, keyStepRequiredJSON)
	got := FormatKeyColumnCandidates(detail.KeyColumnCandidates)
	want := "Key candidates (1200 rows checked, the whole file):\n" +
		"  COLUMNS              DUPLICATE  IDENTICAL  BLANK  PROBLEM\n" +
		"  policy_id            0          0          0      0\n" +
		"  policy_ref, line_no  6          4          3      5\n" +
		"  PROBLEM = DUPLICATE - IDENTICAL + BLANK\n"
	if got != want {
		t.Errorf("FormatKeyColumnCandidates() =\n%s\nwant\n%s", got, want)
	}
}

func TestFormatKeyColumnCandidates_SampleIsALowerBound(t *testing.T) {
	c := &KeyColumnCandidates{
		RowsChecked: 5000,
		WholeFile:   false,
		Candidates:  []KeyColumnSetCounts{{Columns: []string{"id"}}},
	}
	got := FormatKeyColumnCandidates(c)
	if !strings.HasPrefix(got, "Key candidates (5000 rows checked, a sample; every count is a lower bound for the file):\n") {
		t.Errorf("a sample must say its counts are lower bounds; got:\n%s", got)
	}
}

func TestFormatKeyColumnCandidates_EmptyAndCurrentKeys(t *testing.T) {
	c := &KeyColumnCandidates{
		RowsChecked: 40,
		WholeFile:   true,
		Candidates:  []KeyColumnSetCounts{},
		KeyColumnsCounts: &KeyColumnSetCounts{
			Columns: []string{"a", "b"}, DuplicateRows: 4, IdenticalRows: 2, BlankRows: 1,
		},
	}
	want := "Key candidates (40 rows checked, the whole file): none suggested\n" +
		"Current key columns a, b: 4 duplicate, 2 identical, 1 blank, 3 problem\n"
	if got := FormatKeyColumnCandidates(c); got != want {
		t.Errorf("FormatKeyColumnCandidates() =\n%s\nwant\n%s", got, want)
	}
}

func TestFormatKeyColumnCandidates_Nil(t *testing.T) {
	if got := FormatKeyColumnCandidates(nil); got != "Key candidates: none (the draft has no key row counts)\n" {
		t.Errorf("FormatKeyColumnCandidates(nil) = %q", got)
	}
}

func TestPreviousKeyCountsAreShownWithoutSuggestedOrCurrentKeys(t *testing.T) {
	c := &KeyColumnCandidates{
		RowsChecked: 100, WholeFile: false,
		PreviousKeyColumnsCounts: &KeyColumnSetCounts{
			Columns: []string{"old_id"}, DuplicateRows: 6, IdenticalRows: 2, BlankRows: 3,
		},
	}
	got := FormatKeyColumnCandidates(c)
	for _, want := range []string{
		"a sample; every count is a lower bound for the file): none suggested",
		"Previous key columns old_id: 6 duplicate, 2 identical, 3 blank, 7 problem\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestPreviousKeyHeaderReuseAvailability(t *testing.T) {
	yes, no := true, false
	previous := []string{"policy_id", "line_no"}
	for _, tc := range []struct {
		name    string
		allowed *bool
		want    string
	}{
		{"reusable", &yes, "yes (choose them explicitly with --key-columns)"},
		{"refused", &no, "no (cannot choose them for this migration)"},
		{"older server", nil, "unknown (server did not report whether they can be reused)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := &TableSchemaDetail{PreviousKeyColumns: &previous, PreselectPreviousKeyColumns: tc.allowed}
			got := FormatSchemaHeader(detail)
			if !strings.Contains(got, "Previous key columns: policy_id, line_no\n") ||
				!strings.Contains(got, "Reuse previous keys: "+tc.want+"\n") || strings.Contains(got, "Key step:") {
				t.Errorf("unexpected previous-key header:\n%s", got)
			}
		})
	}
	for _, previous := range []*[]string{nil, {}} {
		detail := &TableSchemaDetail{PreviousKeyColumns: previous, PreselectPreviousKeyColumns: &no}
		if got := FormatSchemaHeader(detail); strings.Contains(got, "Previous key") || strings.Contains(got, "Reuse previous") {
			t.Errorf("no prior keys should add no header:\n%s", got)
		}
	}
}

func TestFormatKeyColumnCandidates_SanitizesColumnNames(t *testing.T) {
	c := &KeyColumnCandidates{RowsChecked: 1, WholeFile: true,
		Candidates: []KeyColumnSetCounts{{Columns: []string{"a\x1b[31m\tb"}}}}
	got := FormatKeyColumnCandidates(c)
	if strings.Contains(got, "\x1b") || !strings.Contains(got, "a\\x1b[31m b") {
		t.Errorf("column names must be sanitized and kept on one cell; got %q", got)
	}
}

func TestFormatSchemaHeader_KeyStep(t *testing.T) {
	required := schemaDetailFromJSON(t, keyStepRequiredJSON)
	out := FormatSchemaHeader(required)
	for _, want := range []string{
		"Key step:     required (apply_migration must choose key columns, or none)\n",
		"Key columns:  none\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q; got:\n%s", want, out)
		}
	}

	optional := schemaDetailFromJSON(t, strings.NewReplacer(`"key_step": "required"`, `"key_step": "optional"`,
		`"key_columns": null`, `"key_columns": ["policy_id", "line_no"]`).Replace(keyStepRequiredJSON))
	out = FormatSchemaHeader(optional)
	for _, want := range []string{
		"Key step:     optional (apply_migration may replace these key columns; leaving them out keeps them)\n",
		"Key columns:  policy_id, line_no\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q; got:\n%s", want, out)
		}
	}
}

func TestFormatSchemaHeader_NullKeyStep(t *testing.T) {
	locked := schemaDetailFromJSON(t, strings.NewReplacer(`"key_step": "required"`, `"key_step": null`,
		`"key_columns": null`, `"key_columns": ["policy_id"]`).Replace(keyStepRequiredJSON))
	out := FormatSchemaHeader(locked)
	if strings.Contains(out, "Key step:") || !strings.Contains(out, "Key columns:  policy_id\n") {
		t.Errorf("a null key step prints the stored keys and no step; got:\n%s", out)
	}
	if got := FormatSchemaKeyCandidates(locked); got != "" {
		t.Errorf("a null key step prints no candidates; got:\n%s", got)
	}

	// A server that sends neither field prints neither line.
	unkeyed := schemaDetailFromJSON(t, strings.NewReplacer(`"key_step": "required",`, ``,
		`"key_columns": null,`, ``).Replace(keyStepRequiredJSON))
	if out := FormatSchemaHeader(unkeyed); strings.Contains(out, "Key ") {
		t.Errorf("no key fields, no key lines; got:\n%s", out)
	}
}

func TestSchemaStructuredPayload_KeyFields(t *testing.T) {
	payload, err := SchemaStructuredPayload(schemaDetailFromJSON(t, keyStepRequiredJSON), false)
	if err != nil {
		t.Fatal(err)
	}
	m := payload.(map[string]interface{})
	if m["key_step"] != "required" {
		t.Errorf("key_step = %v", m["key_step"])
	}
	if v, ok := m["key_columns"]; !ok || v != nil {
		t.Errorf("key_columns = %v (present %v); want null", v, ok)
	}
	candidates, ok := m["key_column_candidates"].(map[string]interface{})
	if !ok {
		t.Fatalf("key_column_candidates = %v", m["key_column_candidates"])
	}
	if candidates["whole_file"] != true || len(candidates["candidates"].([]interface{})) != 2 {
		t.Errorf("key_column_candidates = %v", candidates)
	}
	first := candidates["candidates"].([]interface{})[1].(map[string]interface{})
	if cols := first["columns"].([]interface{}); len(cols) != 2 || cols[0] != "policy_ref" {
		t.Errorf("candidate columns = %v", cols)
	}
	for _, key := range []string{"duplicate_rows", "identical_rows", "blank_rows"} {
		if _, ok := first[key]; !ok {
			t.Errorf("candidate missing %s: %v", key, first)
		}
	}
	if v, ok := candidates["key_columns_counts"]; !ok || v != nil {
		t.Errorf("key_columns_counts = %v (present %v); want null", v, ok)
	}
}
