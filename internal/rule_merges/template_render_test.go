package rule_merges //nolint:revive // Match the established package under test.

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// Rule revision UUIDs of a template merge's releases.
const (
	revNew      = "a0000000-0000-4000-8000-000000000001"
	revOld      = "a0000000-0000-4000-8000-000000000002"
	revUnnamed  = "b1234567-0000-4000-8000-000000000003"
	tgtUnmanage = "99999999-9999-4999-8999-999999999999"
)

// templatePreviewJSON is a previewed rule template merge with an apply
// blocker and one item per kind of block: created with produced columns and
// an effect, an unmapped conflict with unbound and ambiguous fields, a
// replaced upgrade, a skipped release in review, an ambiguous match, an
// adopted rule that differs, and a table rule not in the template.
const templatePreviewJSON = `{
  "id": 61, "state": "previewed", "summary": null,
  "source_dataset_id": null, "template_name": "monthly-orders", "target_dataset_id": 3,
  "created_at": "2026-11-20T10:00:00Z",
  "apply_blockers": [{"code": "draft_migration", "message": "The table has a draft migration; review it first."}],
  "items": [
    {"id": 301, "action": "created", "reason": null, "template_key": "Order Total Check",
     "source_object_id": null, "target_object_id": null, "conflict_choice": null, "note": null,
     "rule_snapshot": {"rule_revision_id": "` + revNew + `", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {"amount": "total", "out": "net_total"},
       "produced_fields": [{"rule_field_name": "out", "dataset_field_name": "net_total", "field_schema": {"type": "number"}}]},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": ["net_total"],
     "field_bindings": [
       {"rule_field_name": "amount", "required": true, "status": "bound", "bound_to": "total", "source": "concept", "candidates": []}],
     "effects": ["Creates a draft migration for columns net_total."]},
    {"id": 302, "action": "conflict", "reason": "unmapped", "template_key": "customer-name-split-rule",
     "source_object_id": null, "target_object_id": null, "conflict_choice": null,
     "note": "Required rule fields are not mapped: Purchase Time.",
     "rule_snapshot": {"rule_revision_id": "` + revUnnamed + `", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {"name": "customer"}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": [],
     "field_bindings": [
       {"rule_field_name": "name", "required": true, "status": "bound", "bound_to": "customer", "source": "override", "candidates": []},
       {"rule_field_name": "Purchase Time", "required": true, "status": "unbound", "bound_to": null, "source": null,
        "candidates": [{"column": "purchased_at", "reason": "type ok; shares 'purchase'"}, {"column": "created_at", "reason": "type ok"}]},
       {"rule_field_name": "region", "required": false, "status": "ambiguous", "bound_to": null, "source": null,
        "candidates": [{"column": "region_a", "reason": "type ok; same normalized name"}, {"column": "region_b", "reason": "type ok; same normalized name"}]},
       {"rule_field_name": "zip", "required": false, "status": "unbound", "bound_to": null, "source": null, "candidates": []}],
     "effects": []},
    {"id": 303, "action": "replaced", "reason": null, "template_key": "Range Check",
     "source_object_id": null, "target_object_id": "` + tgtReplaced + `", "conflict_choice": null, "note": null,
     "rule_snapshot": {"rule_revision_id": "` + revNew + `", "params": {"max": 5}, "severity": "warning", "state": "enabled",
       "column_mapping": {"value": "amount"}, "produced_fields": []},
     "base_snapshot": null,
     "target_snapshot": {"rule_revision_id": "` + revOld + `", "params": {"max": 5}, "severity": "warning", "state": "enabled",
       "column_mapping": {"value": "amount"}, "produced_fields": []},
     "source_changes": null, "target_changes": null, "candidate_object_ids": [], "added_field_names": [],
     "field_bindings": [
       {"rule_field_name": "value", "required": true, "status": "bound", "bound_to": "amount", "source": "inherited", "candidates": []}],
     "effects": ["Upgrade: reprocesses 4 quarantined rows of **“range_amount”** when the replacement is enabled."]},
    {"id": 304, "action": "skipped", "reason": "in_review", "template_key": "pending-rule",
     "source_object_id": null, "target_object_id": null, "conflict_choice": null, "note": "The release is in review.",
     "rule_snapshot": {"rule_revision_id": "` + revNew + `", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": [], "field_bindings": [], "effects": []},
    {"id": 305, "action": "conflict", "reason": "ambiguous_match", "template_key": "dup-rule",
     "source_object_id": null, "target_object_id": null, "conflict_choice": null,
     "note": "Its match by rule family is ambiguous; the candidates are **“dup_a”**, **“dup_b”**.",
     "rule_snapshot": {"rule_revision_id": "` + revNew + `", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": ["` + tgtCandA + `", "` + tgtCandB + `"], "added_field_names": [],
     "field_bindings": [], "effects": []},
    {"id": 306, "action": "conflict", "reason": "adopted_differs", "template_key": "Amount Range",
     "source_object_id": null, "target_object_id": "` + tgtKept + `", "conflict_choice": null,
     "note": "It differs from the rule **“amount_range”** on the table.",
     "rule_snapshot": {"rule_revision_id": "` + revNew + `", "params": {"max": 10}, "severity": "blocker", "state": "enabled",
       "column_mapping": {"value": "amount"}, "produced_fields": []},
     "base_snapshot": null,
     "target_snapshot": {"rule_revision_id": "` + revNew + `", "params": {"max": 5}, "severity": "blocker", "state": "enabled",
       "column_mapping": {"value": "amount"}, "produced_fields": []},
     "source_changes": null, "target_changes": null, "candidate_object_ids": [], "added_field_names": [],
     "field_bindings": [
       {"rule_field_name": "value", "required": true, "status": "bound", "bound_to": "amount", "source": "live", "candidates": []}],
     "effects": ["Upgrade: reprocesses 0 quarantined rows of **“amount_range”** when the replacement is enabled."]},
    {"id": 307, "action": "skipped", "reason": "not_in_template", "template_key": null,
     "source_object_id": null, "target_object_id": "` + tgtUnmanage + `", "conflict_choice": null,
     "note": "It is not in the rule template.",
     "rule_snapshot": {"rule_revision_id": "` + revOld + `", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": null,
     "target_snapshot": {"rule_revision_id": "` + revOld + `", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "source_changes": null, "target_changes": null, "candidate_object_ids": [], "added_field_names": [],
     "field_bindings": [], "effects": []}
  ]
}`

// templatePreviewWant is templatePreviewJSON rendered with templateNames,
// minus the Created line.
const templatePreviewWant = `Rule merge 61: rule template monthly-orders -> orders (id 3)
State:    previewed
Plan:     1 created, 1 replaced, 1 skipped, 3 conflict; 1 table rule not in the template

APPLY BLOCKERS
  draft_migration: The table has a draft migration; review it first.

ITEMS

Item 301: Order Total Check
  Action:      created
  Release:     2.0.0
  Fields:
    amount -> total (concept)
  Produces:    net_total (new)
  Effects:
    - Creates a draft migration for columns net_total.

Item 302: customer-name-split-rule
  Action:      conflict (unmapped)
  Release:     revision b1234567
  Fields:
    name -> customer (override)
    Purchase Time: unbound, required
      candidates: purchased_at (type ok; shares 'purchase'), created_at (type ok)
    region: ambiguous, optional
      candidates: region_a (type ok; same normalized name), region_b (type ok; same normalized name)
    zip: unbound, optional
      candidates: none
  Note:        Required rule fields are not mapped: Purchase Time.

Item 303: Range Check
  Action:      replaced
  Table rule:  range_amount (aaaaaaaa)
  Release:     1.0.0 -> 2.0.0
  Fields:
    value -> amount (inherited)
  Effects:
    - Upgrade: reprocesses 4 quarantined rows of “range_amount” when the replacement is enabled.

Item 304: pending-rule
  Action:      skipped (in_review)
  Release:     2.0.0
  Note:        The release is in review.

Item 305: dup-rule
  Action:      conflict (ambiguous_match)
  Table rule:  ambiguous
  Candidates:  dup_a (dddddddd), eeeeeeee
  Release:     2.0.0
  Note:        Its match by rule family is ambiguous; the candidates are “dup_a”, “dup_b”.

Item 306: Amount Range
  Action:      conflict (adopted_differs)
  Table rule:  amount_range (bbbbbbbb)
  Release:     2.0.0
  Fields:
    value -> amount (live)
  Effects:
    - Upgrade: reprocesses 0 quarantined rows of “amount_range” when the replacement is enabled.
  Note:        It differs from the rule “amount_range” on the table.
  Differs from the table rule (params as stored):
    params:
      template: {"max":10}
      table:    {"max":5}

TABLE RULES NOT IN THE TEMPLATE (left alone)
  legacy_check (99999999)
`

func templateNames() RenderNames {
	return RenderNames{
		TargetTable: "orders",
		TargetRule: namerOf(map[string]string{
			tgtReplaced: "range_amount", tgtKept: "amount_range", tgtCandA: "dup_a", tgtUnmanage: "legacy_check",
		}),
		Release: namerOf(map[string]string{revNew: "2.0.0", revOld: "1.0.0"}),
	}
}

var createdLine = regexp.MustCompile(`(?m)^Created: .*\n`)

func renderTemplate(t *testing.T, raw string, names RenderNames) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderTemplateMerge(&buf, decodeMerge(t, raw), names); err != nil {
		t.Fatalf("RenderTemplateMerge: %v", err)
	}
	return createdLine.ReplaceAllString(buf.String(), "")
}

func TestRenderTemplateMergePreview(t *testing.T) {
	if got := renderTemplate(t, templatePreviewJSON, templateNames()); got != templatePreviewWant {
		t.Errorf("render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, templatePreviewWant)
	}
}

// templateAppliedJSON is an applied rule template merge: a created item now
// naming the rule it created, and a conflict declined with keep_target.
const templateAppliedJSON = `{
  "id": 62, "state": "applied", "summary": "Rules: 1 created, 1 skipped.",
  "source_dataset_id": null, "template_name": "monthly-orders", "target_dataset_id": 3,
  "created_at": "2026-11-20T10:00:00Z", "apply_blockers": [],
  "items": [
    {"id": 401, "action": "created", "reason": null, "template_key": "Order Total Check",
     "source_object_id": null, "target_object_id": "` + tgtReplaced + `", "conflict_choice": null, "note": null,
     "rule_snapshot": {"rule_revision_id": "` + revNew + `", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {"amount": "total"}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": [],
     "field_bindings": [
       {"rule_field_name": "amount", "required": true, "status": "bound", "bound_to": "total", "source": "original_name", "candidates": []}],
     "effects": []},
    {"id": 402, "action": "skipped", "reason": "family_mismatch", "template_key": "Amount Range",
     "source_object_id": null, "target_object_id": "` + tgtKept + `", "conflict_choice": "keep_target",
     "note": "The rule **“amount_range”** on the table is of another rule family.",
     "rule_snapshot": {"rule_revision_id": "` + revNew + `", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "base_snapshot": null,
     "target_snapshot": {"rule_revision_id": "` + revOld + `", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {}, "produced_fields": []},
     "source_changes": null, "target_changes": null, "candidate_object_ids": [], "added_field_names": [],
     "field_bindings": [], "effects": []}
  ]
}`

const templateAppliedWant = `Rule merge 62: rule template monthly-orders -> orders (id 3)
State:    applied
Summary:  Rules: 1 created, 1 skipped.
Items:    1 created, 1 skipped

ITEMS

Item 401: Order Total Check
  Action:      created
  Table rule:  range_amount (aaaaaaaa)
  Release:     2.0.0
  Fields:
    amount -> total (original_name)

Item 402: Amount Range
  Action:      skipped (family_mismatch)
  Choice:      keep_target
  Table rule:  amount_range (bbbbbbbb)
  Release:     1.0.0 -> 2.0.0
  Note:        The rule “amount_range” on the table is of another rule family.
`

func TestRenderTemplateMergeApplied(t *testing.T) {
	if got := renderTemplate(t, templateAppliedJSON, templateNames()); got != templateAppliedWant {
		t.Errorf("render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, templateAppliedWant)
	}
}

// A table rule of a revision names.Builtin reports is left out of the header
// count and the list of table rules not in the template.
func TestRenderTemplateMergeLeavesOutBuiltInRules(t *testing.T) {
	names := templateNames()
	names.Builtin = func(id string) bool { return id == revOld }
	got := renderTemplate(t, templatePreviewJSON, names)
	want := strings.Replace(templatePreviewWant, "; 1 table rule not in the template", "", 1)
	want = strings.Replace(want, "\nTABLE RULES NOT IN THE TEMPLATE (left alone)\n  legacy_check (99999999)\n", "", 1)
	if want == templatePreviewWant {
		t.Fatal("the fixture no longer lists a table rule not in the template")
	}
	if got != want {
		t.Errorf("render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// describe rule-merge goes through RenderMerge, which hands a template merge
// to RenderTemplateMerge.
func TestRenderMergeDelegatesATemplateMerge(t *testing.T) {
	for _, raw := range []string{templatePreviewJSON, templateAppliedJSON} {
		m := decodeMerge(t, raw)
		var viaMerge, direct bytes.Buffer
		if err := RenderMerge(&viaMerge, m, templateNames()); err != nil {
			t.Fatalf("RenderMerge: %v", err)
		}
		if err := RenderTemplateMerge(&direct, m, templateNames()); err != nil {
			t.Fatalf("RenderTemplateMerge: %v", err)
		}
		if viaMerge.String() != direct.String() {
			t.Errorf("RenderMerge differs from RenderTemplateMerge:\n%s\n---\n%s", viaMerge.String(), direct.String())
		}
	}
}

// Without names, rules show their SHORT-IDs and releases their revision's.
func TestRenderTemplateMergeWithoutNames(t *testing.T) {
	out := renderTemplate(t, templatePreviewJSON, RenderNames{})
	assertLines(t, out,
		"Rule merge 61: rule template monthly-orders -> id 3",
		"  Table rule:  aaaaaaaa",
		"  Release:     revision a0000000 -> revision a0000000",
		"  Candidates:  dddddddd, eeeeeeee",
		"  99999999",
	)
}

func TestRenderTemplateMergeNeutralizesTerminalControls(t *testing.T) {
	m := decodeMerge(t, templatePreviewJSON)
	m.ApplyBlockers[0].Message = "locked\x1b[31m\nperiod"
	key := "evil\x1b]0;title\x07key"
	m.Items[0].TemplateKey = &key
	m.Items[1].FieldBindings[1].Candidates[0].Reason = "type ok\u202e; x"
	var buf bytes.Buffer
	if err := RenderTemplateMerge(&buf, m, templateNames()); err != nil {
		t.Fatalf("RenderTemplateMerge: %v", err)
	}
	out := buf.String()
	if regexp.MustCompile("[\x1b\x07\u202e]").MatchString(out) {
		t.Errorf("terminal controls survive:\n%q", out)
	}
	assertLines(t, out, `  draft_migration: locked\x1b[31m period`)
}

func TestRenderTemplateMergeNilMerge(t *testing.T) {
	if err := RenderTemplateMerge(&bytes.Buffer{}, nil, RenderNames{}); err == nil {
		t.Error("want an error for a nil merge")
	}
}
