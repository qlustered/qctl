package rule_merges //nolint:revive // Match the established package under test.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// Rule UUIDs whose SHORT-IDs read as the item they belong to.
const (
	srcCreated  = "11111111-1111-4111-8111-111111111111"
	srcReplaced = "22222222-2222-4222-8222-222222222222"
	srcKept     = "33333333-3333-4333-8333-333333333333"
	srcSkipped  = "44444444-4444-4444-8444-444444444444"
	srcLineage  = "55555555-5555-4555-8555-555555555555"
	srcAmbig    = "66666666-6666-4666-8666-666666666666"
	srcFallback = "77777777-7777-4777-8777-777777777777"
	srcDeleted  = "88888888-8888-4888-8888-888888888888"
	tgtReplaced = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	tgtKept     = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	tgtLineage  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	tgtCandA    = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	tgtCandB    = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	tgtFallback = "ffffffff-ffff-4fff-8fff-ffffffffffff"
)

// previewJSON is a previewed merge as the wire carries it: null snapshots,
// null change lists and a null conflict_choice included.
const previewJSON = `{
  "id": 42, "state": "previewed", "summary": null,
  "source_dataset_id": 7, "target_dataset_id": 3,
  "created_at": "2026-09-20T10:00:00Z",
  "items": [
    {"id": 101, "action": "created", "source_object_id": "` + srcCreated + `", "target_object_id": null,
     "conflict_choice": null, "note": null,
     "rule_snapshot": {"rule_revision_id": "r-1", "params": {}, "severity": "as_declared", "state": "enabled",
       "column_mapping": {"value": "id"}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": ["amount_flag", "amount_ok"]},
    {"id": 102, "action": "replaced", "source_object_id": "` + srcReplaced + `", "target_object_id": "` + tgtReplaced + `",
     "conflict_choice": null, "note": "The replacement is disabled; the source rule is disabled.",
     "rule_snapshot": {"rule_revision_id": "r-2", "params": {"max": 5}, "severity": "warning", "state": "disabled",
       "column_mapping": {"value": "amount"}, "produced_fields": []},
     "base_snapshot": {"rule_revision_id": "r-2", "params": {"max": 1}, "severity": "warning", "state": "enabled",
       "column_mapping": {"value": "amount"}, "produced_fields": []},
     "target_snapshot": {"rule_revision_id": "r-2", "params": {"max": 1}, "severity": "warning", "state": "enabled",
       "column_mapping": {"value": "amount"}, "produced_fields": []},
     "source_changes": ["params", "state"], "target_changes": [],
     "candidate_object_ids": [], "added_field_names": []},
    {"id": 103, "action": "kept", "source_object_id": "` + srcKept + `", "target_object_id": "` + tgtKept + `",
     "conflict_choice": null, "note": "It matches the target rule **“amount_range”** by rule family and columns.",
     "rule_snapshot": {"rule_revision_id": "r-3", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": null,
     "target_snapshot": {"rule_revision_id": "r-3", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "source_changes": null, "target_changes": null, "candidate_object_ids": [], "added_field_names": []},
    {"id": 104, "action": "skipped", "source_object_id": "` + srcSkipped + `", "target_object_id": null,
     "conflict_choice": null, "note": "The source rule is waiting for validation.",
     "rule_snapshot": {"rule_revision_id": "r-4", "params": {}, "severity": "warning", "state": "pending_validation",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": []},
    {"id": 105, "action": "conflict", "source_object_id": "` + srcLineage + `", "target_object_id": "` + tgtLineage + `",
     "conflict_choice": null, "note": "It changed in both tables.",
     "rule_snapshot": {"rule_revision_id": "r-5", "params": {"min": 0, "max": 100}, "severity": "blocker",
       "state": "enabled", "column_mapping": {"value": "risk"}, "produced_fields": []},
     "base_snapshot": {"rule_revision_id": "r-5", "params": {"min": 0, "max": 10}, "severity": "warning",
       "state": "enabled", "column_mapping": {"value": "risk"}, "produced_fields": []},
     "target_snapshot": {"rule_revision_id": "r-5", "params": {"min": 0, "max": 50}, "severity": "warning",
       "state": "error", "column_mapping": {"value": "risk"}, "produced_fields": []},
     "source_changes": ["params", "severity"], "target_changes": ["params", "state"],
     "candidate_object_ids": [], "added_field_names": ["risk_score"]},
    {"id": 106, "action": "conflict", "source_object_id": "` + srcAmbig + `", "target_object_id": null,
     "conflict_choice": null,
     "note": "Its match by rule family and columns is ambiguous; the candidates are **“dup_a”**, **“~*dup_b*~”**.",
     "rule_snapshot": {"rule_revision_id": "r-6", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {"value": "id"}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": ["` + tgtCandA + `", "` + tgtCandB + `"], "added_field_names": []},
    {"id": 107, "action": "conflict", "source_object_id": "` + srcFallback + `", "target_object_id": "` + tgtFallback + `",
     "conflict_choice": null, "note": "It matches the target rule **“amt_check”** by rule family and columns.",
     "rule_snapshot": {"rule_revision_id": "r-7", "params": {"z": 1, "a": [2, 1]}, "severity": "warning",
       "state": "disabled", "column_mapping": {"value": "amt", "key": "id"},
       "produced_fields": [{"rule_field_name": "out", "dataset_field_name": "amt_out", "field_schema": {"type": "string"}}]},
     "base_snapshot": null,
     "target_snapshot": {"rule_revision_id": "r-7", "params": {"a": [2, 1], "z": 1}, "severity": "warning",
       "state": "enabled", "column_mapping": {"key": "id", "value": "amount"},
       "produced_fields": [{"rule_field_name": "out", "dataset_field_name": "amt_out", "field_schema": {"type": "string"}}]},
     "source_changes": null, "target_changes": null, "candidate_object_ids": [], "added_field_names": []},
    {"id": 108, "action": "conflict", "source_object_id": "` + srcDeleted + `", "target_object_id": null,
     "conflict_choice": null, "note": "It was deleted in the target and changed in the source.",
     "rule_snapshot": {"rule_revision_id": "r-8b", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": {"rule_revision_id": "r-8a", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "target_snapshot": null, "source_changes": ["rule_revision_id"], "target_changes": null,
     "candidate_object_ids": [], "added_field_names": []}
  ]
}`

// appliedJSON is the same kind of record after apply: a summary, a conflict
// resolved to a replacement with its choice, and a declined created item.
const appliedJSON = `{
  "id": 43, "state": "applied", "summary": "Rules: 1 replaced, 1 skipped, 1 kept.",
  "source_dataset_id": 7, "target_dataset_id": 3,
  "created_at": "2026-09-20T10:00:00Z",
  "items": [
    {"id": 201, "action": "replaced", "source_object_id": "` + srcLineage + `", "target_object_id": "` + tgtLineage + `",
     "conflict_choice": "take_source", "note": null,
     "rule_snapshot": {"rule_revision_id": "r-5", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": ["risk_score"]},
    {"id": 202, "action": "skipped", "source_object_id": "` + srcCreated + `", "target_object_id": null,
     "conflict_choice": "keep_target", "note": null,
     "rule_snapshot": {"rule_revision_id": "r-1", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": ["amount_flag"]},
    {"id": 203, "action": "kept", "source_object_id": "` + srcKept + `", "target_object_id": "` + tgtKept + `",
     "conflict_choice": null, "note": null,
     "rule_snapshot": {"rule_revision_id": "r-3", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": []}
  ]
}`

func decodeMerge(t *testing.T, raw string) *api.RuleMergeSchema {
	t.Helper()
	var m api.RuleMergeSchema
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return &m
}

func namerOf(names map[string]string) func(string) string {
	return func(id string) string { return names[id] }
}

// fixtureNames leaves srcSkipped and tgtCandB unnamed.
func fixtureNames() RenderNames {
	return RenderNames{
		SourceTable: "orders_copy",
		TargetTable: "orders",
		SourceRule: namerOf(map[string]string{
			srcCreated: "not_null_id", srcReplaced: "range_amount", srcKept: "amount_range_src",
			srcLineage: "risk_range", srcAmbig: "dup_check", srcFallback: "amt_check_src", srcDeleted: "gone_rule",
		}),
		TargetRule: namerOf(map[string]string{
			tgtReplaced: "range_amount", tgtKept: "amount_range", tgtLineage: "risk_range_t",
			tgtCandA: "dup_a", tgtFallback: "amt_check",
		}),
	}
}

func render(t *testing.T, m *api.RuleMergeSchema, names RenderNames) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderMerge(&buf, m, names); err != nil {
		t.Fatalf("RenderMerge: %v", err)
	}
	return buf.String()
}

// itemRow returns the items-table row of item id, split on the table's
// two-space column gaps.
func itemRow(t *testing.T, out, id string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, id+" ") {
			var cells []string
			for _, cell := range strings.Split(line, "  ") {
				if cell = strings.TrimSpace(cell); cell != "" {
					cells = append(cells, cell)
				}
			}
			return cells
		}
	}
	t.Fatalf("no row for item %s in:\n%s", id, out)
	return nil
}

// conflictBlock returns the CONFLICTS block of item id, up to the next block.
func conflictBlock(t *testing.T, out, id string) string {
	t.Helper()
	start := strings.Index(out, "\nItem "+id+": ")
	if start < 0 {
		t.Fatalf("no conflict block for item %s in:\n%s", id, out)
	}
	block := out[start+1:]
	if end := strings.Index(block, "\n\nItem "); end >= 0 {
		block = block[:end+1]
	}
	return block
}

func assertLines(t *testing.T, text string, want ...string) {
	t.Helper()
	lines := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		lines[line] = true
	}
	for _, w := range want {
		if !lines[w] {
			t.Errorf("missing line %q in:\n%s", w, text)
		}
	}
}

func TestRenderMergePreviewHeader(t *testing.T) {
	out := render(t, decodeMerge(t, previewJSON), fixtureNames())

	assertLines(t, out,
		"Rule merge 42: orders_copy (id 7) -> orders (id 3)",
		"State:    previewed",
	)
	if !strings.Contains(out, "\nCreated:  2026-09-20T10:00:00Z (") {
		t.Errorf("Created line missing its RFC3339 timestamp:\n%s", out)
	}
	if strings.Contains(out, "Summary:") {
		t.Errorf("a null summary must print no Summary line:\n%s", out)
	}
}

func TestRenderMergePlanCountsFollowTheEnumOrder(t *testing.T) {
	m := decodeMerge(t, previewJSON)
	out := render(t, m, fixtureNames())
	// skipped precedes kept in the backend's declaration order.
	if !strings.Contains(out, "Plan:     1 created, 1 replaced, 1 skipped, 1 kept, 4 conflict\n") {
		t.Errorf("plan counts not in created, replaced, skipped, kept, conflict, failed order:\n%s", out)
	}

	m.Items = append(m.Items, api.RuleMergeItemSchema{ID: 900, Action: "moved"}, api.RuleMergeItemSchema{ID: 901, Action: "failed"})
	out = render(t, m, fixtureNames())
	if !strings.Contains(out, "Plan:     1 created, 1 replaced, 1 skipped, 1 kept, 4 conflict, 1 failed, 1 moved\n") {
		t.Errorf("an action outside the enum must follow the known ones:\n%s", out)
	}
}

func TestRenderMergeItemsTable(t *testing.T) {
	out := render(t, decodeMerge(t, previewJSON), fixtureNames())

	header := itemRow(t, out, "ITEM")
	wantHeader := []string{"ITEM", "ACTION", "SOURCE RULE", "TARGET RULE", "CHOICE", "NOTE"}
	if strings.Join(header, "|") != strings.Join(wantHeader, "|") {
		t.Errorf("header = %q, want %q", header, wantHeader)
	}

	tests := []struct {
		id   string
		want []string
	}{
		{"101", []string{"101", "created", "not_null_id", "-", "-", "adds fields: amount_flag, amount_ok"}},
		{"102", []string{"102", "replaced", "range_amount", "range_amount", "-",
			"The replacement is disabled; the source rule is disabled."}},
		{"103", []string{"103", "kept", "amount_range_src", "amount_range", "-",
			"It matches the target rule “amount_range” by rule family and columns."}},
		{"104", []string{"104", "skipped", "44444444", "-", "-", "The source rule is waiting for validation."}},
		{"105", []string{"105", "conflict", "risk_range", "risk_range_t", "-", "It changed in both tables."}},
		{"106", []string{"106", "conflict", "dup_check", "ambiguous (2 candidates)", "-",
			"Its match by rule family and columns is ambiguous; the candidates are “dup_a”, “dup_b”."}},
		{"108", []string{"108", "conflict", "gone_rule", "-", "-",
			"It was deleted in the target and changed in the source."}},
	}
	for _, tt := range tests {
		got := itemRow(t, out, tt.id)
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("row %s = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestRenderMergeNoteMarkersRenderPlain(t *testing.T) {
	out := render(t, decodeMerge(t, previewJSON), fixtureNames())
	for _, marker := range []string{"**", "~*", "*~"} {
		if strings.Contains(out, marker) {
			t.Errorf("value marker %q survived into the text view:\n%s", marker, out)
		}
	}
}

func TestRenderMergeAddedFieldsFollowTheNote(t *testing.T) {
	m := decodeMerge(t, previewJSON)
	note := "Waiting for validation."
	m.Items[0].Note = &note
	out := render(t, m, fixtureNames())
	row := itemRow(t, out, "101")
	if got := row[len(row)-1]; got != "Waiting for validation. (adds fields: amount_flag, amount_ok)" {
		t.Errorf("NOTE = %q", got)
	}
}

func TestRenderMergeLineageConflict(t *testing.T) {
	block := conflictBlock(t, render(t, decodeMerge(t, previewJSON), fixtureNames()), "105")

	assertLines(t, block,
		"Item 105: risk_range (55555555) vs risk_range_t (cccccccc)",
		"  Note:            It changed in both tables.",
		"  Adds fields:     risk_score",
		"  Source changes:  params, severity",
		"  Target changes:  params, state",
		"  Configuration:",
		"    params:",
		`      base:    {"max":10,"min":0}`,
		`      source:  {"max":100,"min":0}`,
		`      target:  {"max":50,"min":0}`,
		"    severity:",
		"      base:    warning",
		"      source:  blocker",
		"      target:  warning",
		"    state:",
		"      base:    enabled",
		"      source:  enabled",
		"      target:  error",
	)
	for _, unchanged := range []string{"rule_revision_id:", "column_mapping:", "produced_fields:", "Base:"} {
		if strings.Contains(block, unchanged) {
			t.Errorf("block shows %q, which does not differ:\n%s", unchanged, block)
		}
	}
}

func TestRenderMergeAmbiguousConflict(t *testing.T) {
	block := conflictBlock(t, render(t, decodeMerge(t, previewJSON), fixtureNames()), "106")

	assertLines(t, block,
		"Item 106: dup_check (66666666) vs one of 2 candidates",
		"  Candidates:",
		"    - dup_a (dddddddd)",
		"    - eeeeeeee",
		"  Base:            none",
	)
	if strings.Contains(block, "Configuration") {
		t.Errorf("an ambiguous conflict without a base has nothing to compare:\n%s", block)
	}
}

func TestRenderMergeFallbackConflictWithoutBase(t *testing.T) {
	block := conflictBlock(t, render(t, decodeMerge(t, previewJSON), fixtureNames()), "107")

	assertLines(t, block,
		"Item 107: amt_check_src (77777777) vs amt_check (ffffffff)",
		"  Note:            It matches the target rule “amt_check” by rule family and columns.",
		"  Base:            none",
		"    state:",
		"      source:  disabled",
		"      target:  enabled",
		"    column_mapping:",
		`      source:  {"key":"id","value":"amt"}`,
		`      target:  {"key":"id","value":"amount"}`,
	)
	for _, absent := range []string{"base:", "params:", "produced_fields:", "Source changes", "Target changes"} {
		if strings.Contains(block, absent) {
			t.Errorf("block shows %q:\n%s", absent, block)
		}
	}
}

func TestRenderMergeConflictDeletedInTarget(t *testing.T) {
	block := conflictBlock(t, render(t, decodeMerge(t, previewJSON), fixtureNames()), "108")

	assertLines(t, block,
		"Item 108: gone_rule (88888888) vs no target rule",
		"  Source changes:  rule_revision_id",
		"  Target changes:  (no target rule)",
		"    rule_revision_id:",
		"      base:    r-8a",
		"      source:  r-8b",
	)
	if strings.Contains(block, "target: ") {
		t.Errorf("no target snapshot, so no target value:\n%s", block)
	}
}

func TestRenderMergeConflictsSectionStatesParamUnits(t *testing.T) {
	out := render(t, decodeMerge(t, previewJSON), fixtureNames())
	if n := strings.Count(out, "\nCONFLICTS\n"+mergeParamsUnitsLine+"\n"); n != 1 {
		t.Errorf("CONFLICTS heading with the units line appears %d times, want 1:\n%s", n, out)
	}
	for _, id := range []string{"101", "102", "103", "104"} {
		if strings.Contains(out, "\nItem "+id+": ") {
			t.Errorf("item %s is not a conflict but has a block", id)
		}
	}
}

func TestRenderMergeApplied(t *testing.T) {
	out := render(t, decodeMerge(t, appliedJSON), fixtureNames())

	assertLines(t, out,
		"Rule merge 43: orders_copy (id 7) -> orders (id 3)",
		"State:    applied",
		"Summary:  Rules: 1 replaced, 1 skipped, 1 kept.",
		"Items:    1 replaced, 1 skipped, 1 kept",
	)
	if strings.Contains(out, "Plan:") {
		t.Errorf("an applied merge's counts are not a plan:\n%s", out)
	}
	if got := itemRow(t, out, "201"); strings.Join(got, "|") !=
		"201|replaced|risk_range|risk_range_t|take_source|adds fields: risk_score" {
		t.Errorf("row 201 = %q", got)
	}
	// A declined created item is skipped and adds nothing.
	if got := itemRow(t, out, "202"); strings.Join(got, "|") != "202|skipped|not_null_id|-|keep_target|-" {
		t.Errorf("row 202 = %q", got)
	}
	if strings.Contains(out, "CONFLICTS") {
		t.Errorf("no conflict item, so no CONFLICTS section:\n%s", out)
	}
}

func TestRenderMergeWithoutNamesShowsIDs(t *testing.T) {
	out := render(t, decodeMerge(t, previewJSON), RenderNames{})

	assertLines(t, out, "Rule merge 42: id 7 -> id 3")
	if got := itemRow(t, out, "102"); got[2] != "22222222" || got[3] != "aaaaaaaa" {
		t.Errorf("row 102 rule cells = %q, want SHORT-IDs", got[2:4])
	}
	assertLines(t, conflictBlock(t, out, "105"), "Item 105: 55555555 vs cccccccc")
}

func TestRenderMergeWithoutItems(t *testing.T) {
	m := decodeMerge(t, previewJSON)
	m.Items = nil
	out := render(t, m, fixtureNames())

	assertLines(t, out, "Plan:     no rules")
	if strings.Contains(out, "ITEM") || strings.Contains(out, "CONFLICTS") {
		t.Errorf("no items, so no table and no conflicts:\n%s", out)
	}
}

func TestRenderMergeNeutralizesTerminalControls(t *testing.T) {
	m := decodeMerge(t, previewJSON)
	hostileNote := "bad\x1b[2Jnote **“\x1b]0;pwned\x07”**"
	m.Items[1].Note = &hostileNote
	m.Items[4].AddedFieldNames = []string{"field\x1b[1m"}
	m.Items[4].RuleSnapshot.Params = map[string]interface{}{"max": "\x1b[31m\u202e"}
	names := fixtureNames()
	names.SourceTable = "tbl\x1b]0;title\x07"
	names.SourceRule = func(string) string { return "\x1b[31mevil\x1b[0m\nsecond\tline" }

	out := render(t, m, names)

	for _, control := range []string{"\x1b", "\x07", "\u202e"} {
		if strings.Contains(out, control) {
			t.Errorf("output carries raw control %q:\n%q", control, out)
		}
	}
	for _, visible := range []string{`\x1b[31mevil\x1b[0m second line`, `tbl\x1b]0;title\x07 (id 7)`, `\x1b[2J`, `field\x1b[1m`, `\u202e`} {
		if !strings.Contains(out, visible) {
			t.Errorf("output lacks the escaped form %q:\n%s", visible, out)
		}
	}
	// A line break in a name must not split the item's row.
	if got := itemRow(t, out, "101"); got[2] != `\x1b[31mevil\x1b[0m second line` {
		t.Errorf("row 101 source cell = %q", got[2])
	}
}

func TestRenderMergeIsDeterministic(t *testing.T) {
	m := decodeMerge(t, previewJSON)
	params := map[string]interface{}{}
	for _, k := range []string{"k", "c", "x", "a", "q", "m", "b", "z", "e", "t"} {
		params[k] = map[string]interface{}{"y": 1, "b": 2, "m": 3}
	}
	m.Items[4].RuleSnapshot.Params = params

	first := render(t, m, fixtureNames())
	for range 20 {
		if again := render(t, m, fixtureNames()); again != first {
			t.Fatalf("render differs between runs:\n%s\n---\n%s", first, again)
		}
	}
	if !strings.Contains(first, `source:  {"a":{"b":2,"m":3,"y":1},"b":`) {
		t.Errorf("params not rendered as JSON with sorted keys:\n%s", first)
	}
}

func TestRenderMergeNilMerge(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderMerge(&buf, nil, RenderNames{}); err == nil {
		t.Fatal("RenderMerge(nil) returned no error")
	}
}
