package diff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_templates"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	tplTableID  = 42
	tplMergeID  = 61
	tplRevNew   = "a0000000-0000-4000-8000-000000000001"
	tplRevOld   = "a0000000-0000-4000-8000-000000000002"
	tplRevSplit = "a0000000-0000-4000-8000-000000000003"
	tplRuleOld  = "c0000000-0000-4000-8000-000000000001"
	tplRuleLeft = "c0000000-0000-4000-8000-000000000002"
)

// tplFile has two entries; the first sends row_filter as an explicit null.
const tplFile = `apiVersion: qluster.ai/v1
kind: TableRuleTemplate
metadata:
  name: monthly-orders
spec:
  rules:
    # The order total, with its own name.
    - rule: order-total-rule
      release: 2.0.0
      instance_name: Order Total Check
      row_filter: null
    - rule: customer-name-split-rule
      release: 1.3.0
`

// tplWire is tplFile as the preview request carries it.
const tplWire = `{"template":{"entries":[` +
	`{"instance_name":"Order Total Check","release":"2.0.0","row_filter":null,"rule":"order-total-rule"},` +
	`{"release":"1.3.0","rule":"customer-name-split-rule"}],"name":"monthly-orders"}}`

// tplItem is one item of a fixture merge body.
type tplItem struct {
	id                   int
	action, reason, key  string // "" reason is null
	target, note         string // "" is null
	revision, targetRev  string // targetRev "" is a null target_snapshot
	bindings             string // JSON array; "" is []
	addedFields, effects string // JSON arrays; "" is []
}

func tplNullable(s string) string {
	if s == "" {
		return "null"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func tplArray(s string) string {
	if s == "" {
		return "[]"
	}
	return s
}

func (it tplItem) json() string {
	snapshot := func(rev string) string {
		return fmt.Sprintf(`{"rule_revision_id":%q,"params":{},"severity":"warning","state":"enabled",`+
			`"column_mapping":{},"produced_fields":[]}`, rev)
	}
	target := "null"
	if it.targetRev != "" {
		target = snapshot(it.targetRev)
	}
	return fmt.Sprintf(`{"id":%d,"action":%q,"reason":%s,"template_key":%s,"source_object_id":null,`+
		`"target_object_id":%s,"conflict_choice":null,"note":%s,"rule_snapshot":%s,"base_snapshot":null,`+
		`"target_snapshot":%s,"source_changes":null,"target_changes":null,"candidate_object_ids":[],`+
		`"added_field_names":%s,"field_bindings":%s,"effects":%s}`,
		it.id, it.action, tplNullable(it.reason), tplNullable(it.key), tplNullable(it.target),
		tplNullable(it.note), snapshot(it.revision), target,
		tplArray(it.addedFields), tplArray(it.bindings), tplArray(it.effects))
}

// tplMergeBody is a previewed template merge of items, with a key no schema
// declares and an integer past 2^53.
func tplMergeBody(items ...tplItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = it.json()
	}
	return fmt.Sprintf(`{"id":%d,"state":"previewed","summary":null,"source_dataset_id":null,`+
		`"template_name":"monthly-orders","target_dataset_id":%d,"created_at":"2026-11-20T10:00:00Z",`+
		`"apply_blockers":[],"items":[%s],"x_unmodelled":{"b":1,"a":9007199254740993}}`,
		tplMergeID, tplTableID, strings.Join(parts, ","))
}

var (
	tplReplaced = tplItem{id: 301, action: "replaced", key: "Order Total Check", target: tplRuleOld,
		revision: tplRevNew, targetRev: tplRevOld,
		effects: `["Upgrade: reprocesses 4 quarantined rows when the replacement is enabled."]`}
	tplCreated = tplItem{id: 302, action: "created", key: "customer-name-split-rule", revision: tplRevSplit}
	tplKept    = tplItem{id: 301, action: "kept", key: "Order Total Check", target: tplRuleOld,
		revision: tplRevNew, targetRev: tplRevNew}
	tplInReview = tplItem{id: 302, action: "skipped", reason: "in_review", key: "customer-name-split-rule",
		note: "The release is in review.", revision: tplRevSplit}
	tplUnmapped = tplItem{id: 302, action: "conflict", reason: "unmapped", key: "customer-name-split-rule",
		note: "Required rule fields are not mapped: Purchase Time.", revision: tplRevSplit,
		bindings: `[{"rule_field_name":"Purchase Time","required":true,"status":"unbound","bound_to":null,"source":null,` +
			`"candidates":[{"column":"purchased_at","reason":"type ok"}]}]`}
	tplDiffers = tplItem{id: 301, action: "conflict", reason: "adopted_differs", key: "Order Total Check",
		target: tplRuleOld, note: "It differs from the rule on the table.", revision: tplRevNew, targetRev: tplRevOld}
	tplNotInTemplate = tplItem{id: 303, action: "kept", reason: "not_in_template", target: tplRuleLeft,
		revision: tplRevOld, targetRev: tplRevOld}
)

type tplEnv struct {
	mock        *testutil.MockAPIServer
	path        string
	previewBody string
	sent        []string
}

var tplOrgBase = "/api/orgs/" + testOrgID

// newTplEnv serves table orders (id 42), its two rules, the three rule
// revisions' releases, and a preview answering previewBody.
func newTplEnv(t *testing.T, previewBody string) *tplEnv {
	t.Helper()
	env, mock := newDiffTestEnv(t)
	e := &tplEnv{mock: mock, path: env.CreateFile("rule-template.yaml", tplFile), previewBody: previewBody}
	mock.RegisterHandler("GET", tplOrgBase+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "orders" {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows":0,"page":1,"results":[]}`))
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(fmt.Sprintf(
			`{"total_rows":1,"page":1,"results":[{"id":%d,"name":"orders","state":"active"}]}`, tplTableID)))
	})
	mock.RegisterHandler("GET", fmt.Sprintf("%s/datasets/%d/dataset-rules", tplOrgBase, tplTableID),
		func(w http.ResponseWriter, _ *http.Request) {
			rule := func(id, name string) map[string]any {
				return map[string]any{
					"id": id, "instance_name": name, "position": 1, "state": "enabled",
					"severity": "as_declared", "release": "1.0.0", "family_name": name,
					"family_id": tplRevOld, "rule_revision_id": tplRevOld, "has_multiple_releases": false,
					"created_at": "2026-09-14T00:00:00Z", "updated_at": "2026-09-14T00:00:00Z",
				}
			}
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{
				rule(tplRuleOld, "order_total_v1"), rule(tplRuleLeft, "legacy_check"),
			}, "total_rows": 2, "page": 1})
		})
	for id, release := range map[string]string{tplRevNew: "2.0.0", tplRevOld: "1.0.0", tplRevSplit: "1.3.0"} {
		mock.RegisterHandler("GET", tplOrgBase+"/rule-revisions/"+id+"/details", func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": id, "name": "x", "release": release})
		})
	}
	mock.RegisterHandler("POST", fmt.Sprintf("%s/datasets/%d/rule-merges", tplOrgBase, tplTableID),
		func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			e.sent = append(e.sent, strings.TrimSpace(string(b)))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, e.previewBody)
		})
	return e
}

func (e *tplEnv) count(method, path string) int {
	n := 0
	for _, r := range e.mock.Requests() {
		if r == method+" "+path {
			n++
		}
	}
	return n
}

func execDiffArgs(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	rootCmd := setupDiffRoot()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"diff"}, args...))
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestDiffTemplateNothingToDoExitsZeroWithEmptyStdout(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplKept, tplInReview, tplNotInTemplate))
	stdout, stderr, err := execDiffArgs(t, "-f", e.path, "--table-id", "42")
	if code := qerrors.GetExitCode(err); code != 0 {
		t.Fatalf("exit = %d (%v), want 0; stderr:\n%s", code, err, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		"Nothing to do: 1 kept, 1 skipped (rule merge 61).",
		"customer-name-split-rule (in_review): wait for its review to be approved " +
			"('qctl get rule-reviews --rule customer-name-split-rule --release 1.3.0'); " +
			"if none is open, request one with 'qctl review request customer-name-split-rule --release 1.3.0'",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestDiffTemplateChangesExitOneWithThePlanOnStdout(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplReplaced, tplCreated, tplNotInTemplate))
	stdout, stderr, err := execDiffArgs(t, "-f", e.path, "--table", "orders")
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("exit = %d (%v), want 1; stderr:\n%s", code, err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{
		"Rule merge 61: rule template monthly-orders -> orders (id 42)",
		"Release:     1.0.0 -> 2.0.0",
		"Release:     1.3.0",
		"Table rule:  order_total_v1",
		"Upgrade: reprocesses 4 quarantined rows",
		"TABLE RULES NOT IN THE TEMPLATE (left alone)\n  legacy_check (c0000000)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	// tplRevOld is on two items, and is fetched once.
	for _, rev := range []string{tplRevNew, tplRevOld, tplRevSplit} {
		if n := e.count("GET", tplOrgBase+"/rule-revisions/"+rev+"/details"); n != 1 {
			t.Errorf("revision %s fetched %d times, want 1", rev, n)
		}
	}
}

// A table rule not in the template whose revision is built in is left out
// of the plan; its revision is read once.
func TestDiffTemplateLeavesOutBuiltInRulesNotInTheTemplate(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplCreated, tplNotInTemplate))
	e.mock.RegisterHandler("GET", tplOrgBase+"/rule-revisions/"+tplRevOld+"/details", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": tplRevOld, "name": "x", "release": "1.0.0", "is_builtin": true})
	})
	stdout, stderr, err := execDiffArgs(t, "-f", e.path, "--table-id", "42")
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("exit = %d (%v), want 1; stderr:\n%s", code, err, stderr)
	}
	for _, unwanted := range []string{"NOT IN THE TEMPLATE", "not in the template", "legacy_check"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("stdout shows the built-in rule (%q):\n%s", unwanted, stdout)
		}
	}
	if n := e.count("GET", tplOrgBase+"/rule-revisions/"+tplRevOld+"/details"); n != 1 {
		t.Errorf("the built-in rule's revision was read %d times, want 1", n)
	}
}

// A skipped item whose table rule, or its replacement, passed validation and
// waits only for a draft migration names that migration's review alert; one
// still validating keeps the plain next step.
func TestDiffTemplateNextStepNamesTheMigrationReview(t *testing.T) {
	const tplRuleNext = "c0000000-0000-4000-8000-000000000003"
	waiting := tplItem{id: 301, action: "skipped", reason: "waiting_validation", key: "Order Total Check",
		target: tplRuleOld, note: "Waiting for validation.", revision: tplRevNew, targetRev: tplRevNew}
	upgrading := waiting
	upgrading.reason, upgrading.note = "upgrade_in_progress", "Its upgrade waits for validation."
	rule := func(id, state string, extra map[string]any) map[string]any {
		r := map[string]any{
			"id": id, "instance_name": "Order Total Check", "position": 1, "state": state,
			"severity": "as_declared", "release": "2.0.0", "family_name": "x",
			"family_id": tplRevOld, "rule_revision_id": tplRevNew, "has_multiple_releases": false,
			"created_at": "2026-09-14T00:00:00Z", "updated_at": "2026-09-14T00:00:00Z",
		}
		for k, v := range extra {
			r[k] = v
		}
		return r
	}
	awaiting := map[string]any{"validation_state": "passed", "pending_migration_model_id": 9, "validation_blocks_enable": false}
	validating := map[string]any{"validation_state": "in_flight", "pending_migration_model_id": 9}
	successor := func(extra map[string]any) map[string]any {
		r := rule(tplRuleNext, "pending_validation", extra)
		r["upgraded_from_id"] = tplRuleOld
		return r
	}
	tests := []struct {
		name  string
		item  tplItem
		rules []map[string]any
		alert bool
		want  string
	}{
		{"waiting rule, alert found", waiting, []map[string]any{rule(tplRuleOld, "pending_validation", awaiting)}, true,
			"Order Total Check (waiting_validation): the table rule passed validation and waits for the review of " +
				"draft migration 9: 'qctl alert resolve 77 --action apply_migration"},
		{"waiting rule, no alert", waiting, []map[string]any{rule(tplRuleOld, "pending_validation", awaiting)}, false,
			"the table rule passed validation and waits for the review of draft migration 9; " +
				"'qctl get alerts --table-id 42 --issue-type needs_review_migration' finds its review alert"},
		{"upgrade waiting", upgrading, []map[string]any{rule(tplRuleOld, "enabled", nil), successor(awaiting)}, true,
			"Order Total Check (upgrade_in_progress): the table rule's upgrade passed validation and waits for the " +
				"review of draft migration 9: 'qctl alert resolve 77 --action apply_migration"},
		{"still validating", waiting, []map[string]any{rule(tplRuleOld, "pending_validation", validating)}, true,
			"Order Total Check (waiting_validation): the table rule waits for validation; run this again once it has finished"},
		{"upgrade still validating", upgrading, []map[string]any{rule(tplRuleOld, "enabled", nil), successor(validating)}, true,
			"Order Total Check (upgrade_in_progress): the table rule's upgrade waits for validation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTplEnv(t, tplMergeBody(tt.item))
			e.mock.RegisterHandler("GET", fmt.Sprintf("%s/datasets/%d/dataset-rules", tplOrgBase, tplTableID),
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": tt.rules, "total_rows": len(tt.rules), "page": 1})
				})
			e.mock.RegisterHandler("GET", tplOrgBase+"/alerts", func(w http.ResponseWriter, _ *http.Request) {
				results := []map[string]any{}
				if tt.alert {
					results = append(results, map[string]any{"id": 77, "dataset_id": tplTableID, "issue_type": "needs_review_migration"})
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "total_rows": len(results)})
			})
			e.mock.RegisterHandler("GET", tplOrgBase+"/alerts/77", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]any{
					"id": 77, "dataset_id": tplTableID, "issue_type": "needs_review_migration", "migration_model_id": 9,
				})
			})
			_, stderr, err := execDiffArgs(t, "-f", e.path, "--table-id", "42")
			if err != nil {
				t.Fatalf("diff: %v", err)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr lacks %q:\n%s", tt.want, stderr)
			}
		})
	}
}

func TestDiffTemplateSendsTheTemplateAsWritten(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplKept))
	if _, _, err := execDiffArgs(t, "-f", e.path, "--table-id", "42"); err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(e.sent) != 1 || e.sent[0] != tplWire {
		t.Errorf("preview requests = %q, want one %q", e.sent, tplWire)
	}
}

func TestDiffTemplateUnmappedExitsTwoWithTheLinesToAdd(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplReplaced, tplUnmapped))
	stdout, _, err := execDiffArgs(t, "-f", e.path, "--table-id", "42")
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("exit = %d (%v), want 2", code, err)
	}
	if err.Error() != "" {
		t.Errorf("error message = %q, want silent", err.Error())
	}
	for _, want := range []string{
		"COLUMN MAPPINGS TO ADD\nAdd these keys to the entries of " + e.path,
		"--table-id 42 --write-mappings' inserts them with empty values.",
		"- rule: customer-name-split-rule\n  column_mapping:\n    \"Purchase Time\": # ",
		"purchased_at",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "UNRESOLVED CONFLICTS") {
		t.Errorf("an unmapped conflict printed a resolution document:\n%s", stdout)
	}
}

func TestDiffTemplateOtherConflictExitsTwoWithTheResolutionSkeleton(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplDiffers, tplCreated))
	stdout, _, err := execDiffArgs(t, "-f", e.path, "--table-id", "42")
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("exit = %d (%v), want 2", code, err)
	}
	for _, want := range []string{
		"UNRESOLVED CONFLICTS\n1 item needs a decision.",
		"--table-id 42 --resolutions <file>'",
		"kind: RuleMergeResolution",
		"    # matched table rule: order_total_v1\n",
		"    - rule: Order Total Check\n      choice:\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestDiffTemplateWriteMappingsWritesTheFile(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplKept, tplUnmapped))
	stdout, _, err := execDiffArgs(t, "-f", e.path, "--table-id", "42", "--write-mappings")
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("exit = %d (%v), want 2: the item is still a conflict", code, err)
	}
	if !strings.Contains(stdout, "ADDED TO "+e.path+"\nEach value is empty") {
		t.Errorf("stdout lacks the report of what was added:\n%s", stdout)
	}
	if strings.Contains(stdout, "COLUMN MAPPINGS TO ADD") {
		t.Errorf("stdout still prints the lines to add:\n%s", stdout)
	}
	data, readErr := os.ReadFile(e.path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), "    # The order total, with its own name.\n") ||
		!strings.Contains(string(data), "      column_mapping:\n        \"Purchase Time\": # ") {
		t.Errorf("file after --write-mappings:\n%s", data)
	}
	if _, parseErr := rule_templates.Parse(data, e.path); qerrors.GetExitCode(parseErr) != 2 {
		t.Errorf("Parse of the written file = %v, want an exit-2 refusal of the empty value", parseErr)
	}
}

// A required unbound field the item's rule revision enriches gets the hint to
// declare it under produced_fields, in the lines to add and in the comments
// --write-mappings writes.
func TestDiffTemplateUnmappedEnrichedFieldHintsProducedFields(t *testing.T) {
	const hint = "; or drop this key and list the field under produced_fields to add it as a new column " +
		"(dataset_field_name names the column)"
	e := newTplEnv(t, tplMergeBody(tplKept, tplUnmapped))
	e.mock.RegisterHandler("GET", tplOrgBase+"/rule-revisions/"+tplRevSplit+"/details", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"id": tplRevSplit, "name": "x", "release": "1.3.0", "enriches_columns": []string{"Purchase Time"},
		})
	})
	stdout, _, err := execDiffArgs(t, "-f", e.path, "--table-id", "42")
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("exit = %d (%v), want 2", code, err)
	}
	if want := "\"Purchase Time\": # choose one: purchased_at (type ok)" + hint + "\n"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}

	if _, _, err := execDiffArgs(t, "-f", e.path, "--table-id", "42", "--write-mappings"); qerrors.GetExitCode(err) != 2 {
		t.Fatalf("--write-mappings exit = %d (%v), want 2", qerrors.GetExitCode(err), err)
	}
	data, readErr := os.ReadFile(e.path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if want := "        \"Purchase Time\": # choose one: purchased_at (type ok)" + hint + "\n"; !strings.Contains(string(data), want) {
		t.Errorf("file after --write-mappings lacks %q:\n%s", want, data)
	}
}

// --write-mappings on a JSON template is refused before any request, so no
// preview is stored.
func TestDiffTemplateWriteMappingsRefusesJSONBeforeThePreview(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplUnmapped))
	path := filepath.Join(filepath.Dir(e.path), "rule-template.json")
	if err := os.WriteFile(path, []byte(`{"apiVersion":"qluster.ai/v1","kind":"TableRuleTemplate",`+
		`"metadata":{"name":"monthly-orders"},"spec":{"rules":[{"rule":"order-total-rule","release":"2.0.0"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := execDiffArgs(t, "-f", path, "--table-id", "42", "--write-mappings")
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("exit = %d (%v), want 2", code, err)
	}
	if n := len(e.mock.Requests()); n != 0 {
		t.Errorf("%d requests sent before the refusal: %q", n, e.mock.Requests())
	}
}

// With no unmapped item, --write-mappings leaves the file unopened: a file
// WriteMappings would refuse (here, a trailing comment-only document) keeps
// diff's own exit code.
func TestDiffTemplateWriteMappingsWithNothingToAddKeepsTheExitCode(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplReplaced))
	content := tplFile + "---\n# notes\n"
	if err := os.WriteFile(e.path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execDiffArgs(t, "-f", e.path, "--table-id", "42", "--write-mappings")
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("exit = %d (%v), want 1: a rule is replaced", code, err)
	}
	if !strings.Contains(stdout, "No column_mapping keys to add to "+e.path) {
		t.Errorf("stdout lacks the nothing-added line:\n%s", stdout)
	}
	if data, _ := os.ReadFile(e.path); string(data) != content {
		t.Errorf("the file changed:\n%s", data)
	}
}

func TestDiffTemplateJSONPrintsTheBodyAsReceived(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplReplaced))
	stdout, stderr, err := execDiffArgs(t, "-f", e.path, "--table-id", "42", "-o", "json")
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("exit = %d (%v), want 1", code, err)
	}
	for _, want := range []string{`"x_unmodelled": {`, `"a": 9007199254740993`, `"template_name": "monthly-orders"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "Rule merge 61") || strings.Contains(stdout, "Rule merge 61") {
		t.Errorf("the plan belongs on stderr under -o json; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

func TestDiffTemplateNeedsATarget(t *testing.T) {
	e := newTplEnv(t, tplMergeBody(tplKept))
	_, _, err := execDiffArgs(t, "-f", e.path)
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("exit = %d (%v), want 2", code, err)
	}
	if n := len(e.mock.Requests()); n != 0 {
		t.Errorf("%d requests sent before the refusal: %q", n, e.mock.Requests())
	}
}

func TestDiffRefusesTemplateOptionsForAnotherKind(t *testing.T) {
	for _, args := range [][]string{
		{"--table-id", "42"},
		{"--table", "orders"},
		{"--write-mappings"},
		{"-o", "json"},
		{"-o", "yaml"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env, mock := newDiffTestEnv(t)
			path := env.CreateFile("table-kind.yaml",
				"apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Car Policy, description: null, standard_fields: []}\n")
			_, _, err := execDiffArgs(t, append([]string{"-f", path}, args...)...)
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Fatalf("exit = %d (%v), want 2", code, err)
			}
			if n := len(mock.Requests()); n != 0 {
				t.Errorf("%d requests sent before the refusal: %q", n, mock.Requests())
			}
		})
	}
}
