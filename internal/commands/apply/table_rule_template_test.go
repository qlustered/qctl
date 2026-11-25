package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	rtTableID  = 42
	rtMergeID  = 61
	rtRevNew   = "a0000000-0000-4000-8000-000000000001"
	rtRevOld   = "a0000000-0000-4000-8000-000000000002"
	rtRevSplit = "a0000000-0000-4000-8000-000000000003"
	rtRuleOld  = "c0000000-0000-4000-8000-000000000001"
	rtRuleNew  = "d0000000-0000-4000-8000-000000000001"
	rtRuleNew2 = "d0000000-0000-4000-8000-000000000002"
)

const rtFile = `apiVersion: qluster.ai/v1
kind: TableRuleTemplate
metadata:
  name: monthly-orders
spec:
  rules:
    - rule: order-total-rule
      release: 2.0.0
      instance_name: Order Total Check
    - rule: customer-name-split-rule
      release: 1.3.0
`

var (
	rtOrgBase     = "/api/orgs/" + testOrgID
	rtPreviewPath = fmt.Sprintf("%s/datasets/%d/rule-merges", rtOrgBase, rtTableID)
	rtMergePath   = fmt.Sprintf("%s/%d", rtPreviewPath, rtMergeID)
	rtApplyPath   = rtMergePath + "/apply"
	rtRulesPath   = fmt.Sprintf("%s/datasets/%d/dataset-rules", rtOrgBase, rtTableID)
	rtAlertsPath  = rtOrgBase + "/alerts"
)

// rtItem is one item of a fixture merge body.
type rtItem struct {
	id                  int
	action, reason, key string // "" reason is null
	target, note        string // "" is null
	revision, targetRev string // targetRev "" is a null target_snapshot
	choice              string // "" is null
	bindings, effects   string // JSON arrays; "" is []
	produced            string // the rule snapshot's produced_fields, a JSON array; "" is []
}

func rtNullable(s string) string {
	if s == "" {
		return "null"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func rtArray(s string) string {
	if s == "" {
		return "[]"
	}
	return s
}

func (it rtItem) json() string {
	snapshot := func(rev, produced string) string {
		return fmt.Sprintf(`{"rule_revision_id":%q,"params":{},"severity":"warning","state":"enabled",`+
			`"column_mapping":{},"produced_fields":%s}`, rev, rtArray(produced))
	}
	target := "null"
	if it.targetRev != "" {
		target = snapshot(it.targetRev, "")
	}
	return fmt.Sprintf(`{"id":%d,"action":%q,"reason":%s,"template_key":%s,"source_object_id":null,`+
		`"target_object_id":%s,"conflict_choice":%s,"note":%s,"rule_snapshot":%s,"base_snapshot":null,`+
		`"target_snapshot":%s,"source_changes":null,"target_changes":null,"candidate_object_ids":[],`+
		`"added_field_names":[],"field_bindings":%s,"effects":%s}`,
		it.id, it.action, rtNullable(it.reason), rtNullable(it.key), rtNullable(it.target), rtNullable(it.choice),
		rtNullable(it.note), snapshot(it.revision, it.produced), target, rtArray(it.bindings), rtArray(it.effects))
}

// rtBody is template merge 61 in state, with summary (a JSON literal),
// blockers (a JSON array, "" is []) and items.
func rtBody(state, summary, blockers string, items ...rtItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = it.json()
	}
	return fmt.Sprintf(`{"id":%d,"state":%q,"summary":%s,"source_dataset_id":null,`+
		`"template_name":"monthly-orders","target_dataset_id":%d,"created_at":"2026-11-20T10:00:00Z",`+
		`"apply_blockers":%s,"items":[%s]}`,
		rtMergeID, state, summary, rtTableID, rtArray(blockers), strings.Join(parts, ","))
}

var (
	rtReplaced = rtItem{id: 301, action: "replaced", key: "Order Total Check", target: rtRuleOld,
		revision: rtRevNew, targetRev: rtRevOld,
		effects: `["Upgrade: reprocesses 4 quarantined rows of order_total_v1 when the replacement is enabled."]`}
	rtCreated = rtItem{id: 302, action: "created", key: "customer-name-split-rule", revision: rtRevSplit}
	rtKept    = rtItem{id: 301, action: "kept", key: "Order Total Check", target: rtRuleOld,
		revision: rtRevNew, targetRev: rtRevNew}
	rtInReview = rtItem{id: 302, action: "skipped", reason: "in_review", key: "customer-name-split-rule",
		note: "The release is in review.", revision: rtRevSplit}
	rtUnmapped = rtItem{id: 302, action: "conflict", reason: "unmapped", key: "customer-name-split-rule",
		note: "Required rule fields are not mapped: Purchase Time.", revision: rtRevSplit,
		bindings: `[{"rule_field_name":"Purchase Time","required":true,"status":"unbound","bound_to":null,"source":null,` +
			`"candidates":[{"column":"purchased_at","reason":"type ok"}]}]`}
	rtDiffers = rtItem{id: 301, action: "conflict", reason: "adopted_differs", key: "Order Total Check",
		target: rtRuleOld, note: "It differs from the rule on the table.", revision: rtRevNew, targetRev: rtRevOld}
	rtNotInTemplate = rtItem{id: 303, action: "kept", reason: "not_in_template", target: rtRuleOld,
		revision: rtRevOld, targetRev: rtRevOld}
)

// rtApplied is the plan rtReplaced + rtCreated after the apply: each item
// carries its new rule, the created one with note.
func rtApplied(note string) []rtItem {
	replaced, created := rtReplaced, rtCreated
	replaced.target, created.target, created.note = rtRuleNew, rtRuleNew2, note
	return []rtItem{replaced, created}
}

type rtEnv struct {
	te          *testutil.TestEnv
	mock        *testutil.MockAPIServer
	path        string
	previewBody string
	applyStatus int
	applyBody   string
	applySent   []string
	getBodies   []string
	// rules is the table's rule list, as each listing answers it.
	rules []map[string]any
}

func rtRule(id, name, state string) map[string]any {
	return map[string]any{
		"id": id, "instance_name": name, "position": 1, "state": state,
		"severity": "as_declared", "release": "1.0.0", "family_name": name,
		"family_id": rtRevOld, "rule_revision_id": rtRevOld, "has_multiple_releases": false,
		"created_at": "2026-09-14T00:00:00Z", "updated_at": "2026-09-14T00:00:00Z",
	}
}

// newRTEnv serves table orders (id 42), its rules, the rule revisions'
// releases, the preview, the apply and each read of merge 61 in turn (the
// last repeating), with the wait clock stubbed.
func newRTEnv(t *testing.T, previewBody string) *rtEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.URL())
	te.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	te.SetupCredential(key, testOrgID, "token")

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	rule_merges.SetWaitClock(func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	t.Cleanup(rule_merges.ResetWaitClock)

	e := &rtEnv{
		te: te, mock: mock, path: te.CreateFile("rule-template.yaml", rtFile), previewBody: previewBody,
		applyStatus: http.StatusAccepted,
		rules:       []map[string]any{rtRule(rtRuleOld, "order_total_v1", "enabled")},
	}
	mock.RegisterHandler("GET", rtOrgBase+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "orders" {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows":0,"page":1,"results":[]}`))
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(fmt.Sprintf(
			`{"total_rows":1,"page":1,"results":[{"id":%d,"name":"orders","state":"active"}]}`, rtTableID)))
	})
	mock.RegisterHandler("GET", rtRulesPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": e.rules, "total_rows": len(e.rules), "page": 1})
	})
	for id, release := range map[string]string{rtRevNew: "2.0.0", rtRevOld: "1.0.0", rtRevSplit: "1.3.0"} {
		mock.RegisterHandler("GET", rtOrgBase+"/rule-revisions/"+id+"/details", func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": id, "name": "x", "release": release})
		})
	}
	mock.RegisterHandler("POST", rtPreviewPath, func(w http.ResponseWriter, _ *http.Request) {
		rtWrite(w, http.StatusOK, e.previewBody)
	})
	mock.RegisterHandler("POST", rtApplyPath, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e.applySent = append(e.applySent, string(b))
		rtWrite(w, e.applyStatus, e.applyBody)
	})
	gets := 0
	mock.RegisterHandler("GET", rtMergePath, func(w http.ResponseWriter, _ *http.Request) {
		body := e.getBodies[min(gets, len(e.getBodies)-1)]
		gets++
		rtWrite(w, http.StatusOK, body)
	})
	return e
}

func rtWrite(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (e *rtEnv) count(method, path string) int {
	n := 0
	for _, r := range e.mock.Requests() {
		if r == method+" "+path {
			n++
		}
	}
	return n
}

func (e *rtEnv) run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return execApplyRoot(t, append([]string{"apply", "-f", e.path}, args...)...)
}

func rtWantContains(t *testing.T, where, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q:\n%s", where, want, text)
		}
	}
}

func TestApplyTemplateNothingToDoSendsNoApply(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtInReview, rtNotInTemplate))
	stdout, _, err := e.run(t, "--table-id", "42", "--yes")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if n := e.count("POST", rtApplyPath); n != 0 {
		t.Errorf("%d apply requests, want none", n)
	}
	rtWantContains(t, "stdout", stdout, "Nothing to do: 1 skipped (rule merge 61).",
		"customer-name-split-rule (in_review): wait for its review to be approved")
	if strings.Contains(stdout, "ineage") {
		t.Errorf("stdout mentions lineage with no kept entry:\n%s", stdout)
	}
}

// rtEditedKept is a kept item whose table rule was edited since the merge
// that recorded its lineage.
var rtEditedKept = rtItem{id: 304, action: "kept", reason: "edited_on_table", key: "Customer Split",
	target: rtRuleNew2, note: "Edited on the table.", revision: rtRevSplit, targetRev: rtRevSplit}

// A nothing-to-do run holding a kept entry paired with a table rule applies
// the preview, which records the entry's lineage; the 202 is already
// applied, so --wait reads nothing back.
func TestApplyTemplateNothingToDoRecordsLineage(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtKept, rtInReview, rtNotInTemplate))
	e.applyBody = rtBody("applied", `"Rules: 1 kept, 2 skipped."`, "", rtKept, rtInReview, rtNotInTemplate)
	stdout, _, err := e.run(t, "--table-id", "42", "--yes", "--wait")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(e.applySent) != 1 || strings.TrimSpace(e.applySent[0]) != "{}" {
		t.Errorf("apply requests = %q, want one with no resolutions", e.applySent)
	}
	if n := e.count("GET", rtMergePath); n != 0 {
		t.Errorf("%d reads of the merge, want none: the 202 was already applied", n)
	}
	rtWantContains(t, "stdout", stdout, "Nothing to do: 1 kept, 1 skipped (rule merge 61).",
		"Recorded the template's lineage for Order Total Check (rule merge 61: applied): "+
			"a later change to its entry plans replaced, not conflict adopted_differs.")
}

// The lineage apply changes no rule, so an interactive run sends it without
// asking: the prompt input holds no answer.
func TestApplyTemplateLineageApplyAsksNothing(t *testing.T) {
	cmdutil.SetReader(strings.NewReader(""))
	t.Cleanup(cmdutil.ResetReader)
	e := newRTEnv(t, rtBody("previewed", "null", "", rtKept))
	e.applyBody = rtBody("applied", `"Rules: 1 kept."`, "", rtKept)
	stdout, _, err := e.run(t, "--table-id", "42")
	if err != nil {
		t.Fatalf("interactive apply: %v (exit %d)", err, qerrors.GetExitCode(err))
	}
	if len(e.applySent) != 1 {
		t.Errorf("apply requests = %q, want one", e.applySent)
	}
	rtWantContains(t, "stdout", stdout, "Recorded the template's lineage for Order Total Check")
}

// Under -o json stdout is the applied merge the lineage apply returned.
func TestApplyTemplateLineageApplyOutputIsTheAppliedMerge(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtKept))
	e.applyBody = rtBody("applied", `"Rules: 1 kept."`, "", rtKept)
	stdout, stderr, err := e.run(t, "--table-id", "42", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("apply -o json: %v", err)
	}
	var doc struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc.State != "applied" {
		t.Errorf("stdout is not the applied merge: %v\n%s", err, stdout)
	}
	rtWantContains(t, "stderr", stderr, "Recorded the template's lineage for Order Total Check")
}

// A kept entry edited on the table would have its edit recorded as the
// template's base, so no lineage apply is sent while one is in the merge.
func TestApplyTemplateLineageSkippedWhileAKeptEntryWasEditedOnTheTable(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtKept, rtEditedKept))
	stdout, _, err := e.run(t, "--table-id", "42", "--yes")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if n := e.count("POST", rtApplyPath); n != 0 {
		t.Errorf("%d apply requests, want none", n)
	}
	rtWantContains(t, "stdout", stdout, "Nothing to do: 2 kept (rule merge 61).",
		"Lineage not recorded: Customer Split was edited on the table, and applying this merge would record "+
			"the edit as the template's base", "plans conflict adopted_differs")
}

// A kept entry of reason edited_on_table already has lineage: with no other
// kept entry there is nothing to record and nothing to say.
func TestApplyTemplateNoLineageApplyWhenEveryKeptEntryHasLineage(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtEditedKept))
	stdout, _, err := e.run(t, "--table-id", "42", "--yes")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if n := e.count("POST", rtApplyPath); n != 0 {
		t.Errorf("%d apply requests, want none", n)
	}
	if strings.Contains(stdout, "ineage") {
		t.Errorf("stdout mentions lineage:\n%s", stdout)
	}
}

// An apply blocker on the preview refuses the lineage apply before it is
// sent: a note, and still exit 0.
func TestApplyTemplateLineageApplyBlockedByThePreview(t *testing.T) {
	blockers := `[{"code":"merge_applying","message":"Rule merge 60 into this table is applying; apply this one once it finishes."}]`
	e := newRTEnv(t, rtBody("previewed", "null", blockers, rtKept))
	stdout, _, err := e.run(t, "--table-id", "42", "--yes")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if n := e.count("POST", rtApplyPath); n != 0 {
		t.Errorf("%d apply requests, want none", n)
	}
	rtWantContains(t, "stdout", stdout, "Nothing to do: 1 kept (rule merge 61).",
		"Lineage not recorded: the server refuses to apply this merge now (APPLY BLOCKERS):",
		"qctl describe rule-merge 60 --table-id 42")
}

// The server refusing the lineage apply leaves the run nothing to do: the
// refusal is a note, and the exit stays 0.
func TestApplyTemplateLineageApplyRefusedIsStillNothingToDo(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"change review policy": {http.StatusForbidden,
			`{"detail":{"msg":"This table's change review policy does not let the agent role resolve this change.",` +
				`"error_code":"CHANGE_REVIEW_POLICY_DENIED",` +
				`"context":{"operation":"rule_merge_apply","verb":"resolve","denied_keys":[["rules"]]}}}`,
			"The policy does not let your role resolve rule_merge_apply key rules."},
		"period locked": {http.StatusConflict,
			`{"detail":{"msg":"Period 2026-07 is locked. Unlock it before changing its data.","error_code":"PERIOD_LOCKED"}}`,
			"Period 2026-07 is locked."},
		"another merge applying": {http.StatusConflict,
			`{"detail":{"msg":"Rule merge 60 into this table is applying; apply this one once it finishes."}}`,
			"qctl describe rule-merge 60 --table-id 42"},
		"stale preview": {http.StatusConflict,
			`{"detail":{"msg":"The table's rules changed since the preview; preview the rule template again."}}`,
			"Run 'qctl apply -f "},
	} {
		t.Run(name, func(t *testing.T) {
			e := newRTEnv(t, rtBody("previewed", "null", "", rtKept))
			e.applyStatus, e.applyBody = tc.status, tc.body
			stdout, _, err := e.run(t, "--table-id", "42", "--yes", "--wait")
			if err != nil {
				t.Fatalf("apply: exit %d: %v", qerrors.GetExitCode(err), err)
			}
			if n := e.count("POST", rtApplyPath); n != 1 {
				t.Errorf("%d apply requests, want 1", n)
			}
			if n := e.count("GET", rtMergePath); n != 0 {
				t.Errorf("%d reads of the merge, want none", n)
			}
			rtWantContains(t, "stdout", stdout, "Nothing to do: 1 kept (rule merge 61).",
				"Lineage not recorded: the server refused the apply.", tc.want)
		})
	}
}

// A lineage apply that fails for any other reason fails the run.
func TestApplyTemplateLineageApplyServerErrorFails(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtKept))
	e.applyStatus, e.applyBody = http.StatusInternalServerError, `{"detail":"boom"}`
	_, _, err := e.run(t, "--table-id", "42", "--yes")
	requireExit(t, err, 1)
}

func TestApplyTemplateCreatedAppliedWithYes(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtReplaced, rtCreated))
	e.applyBody = rtBody("applied", `"Rules: 1 created, 1 replaced."`, "", rtApplied("")...)
	e.rules = append(e.rules, rtRule(rtRuleNew, "Order Total Check", "enabled"), rtRule(rtRuleNew2, "customer-name-split-rule", "enabled"))
	stdout, stderr, err := e.run(t, "--table", "orders", "--yes")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(e.applySent) != 1 || strings.TrimSpace(e.applySent[0]) != "{}" {
		t.Errorf("apply requests = %q, want one with no resolutions", e.applySent)
	}
	if n := e.count("GET", rtMergePath); n != 0 {
		t.Errorf("%d reads of the merge, want none: the 202 was already applied", n)
	}
	rtWantContains(t, "stderr", stderr, "Warning: applying replaces table rules",
		"Order Total Check: Upgrade: reprocesses 4 quarantined rows of order_total_v1 when the replacement is enabled.")
	rtWantContains(t, "stdout", stdout, "rule-merge/61 applied", "SUMMARY (rule merge 61: applied)",
		"Order Total Check (d0000000)", "there is nothing to run for that")
}

func TestApplyTemplateWaitSettlesApplied(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtReplaced, rtCreated))
	e.applyBody = rtBody("applying", "null", "", rtApplied("Waiting for validation.")...)
	e.getBodies = []string{e.applyBody, rtBody("applied", `"Rules: 1 created, 1 replaced."`, "", rtApplied("")...)}
	stdout, stderr, err := e.run(t, "--table-id", "42", "--yes", "--wait")
	if err != nil {
		t.Fatalf("apply --wait: %v", err)
	}
	if n := e.count("GET", rtMergePath); n != 2 {
		t.Errorf("%d reads of the merge, want 2", n)
	}
	rtWantContains(t, "stderr", stderr, "waiting for rule merge 61 to finish applying")
	rtWantContains(t, "stdout", stdout, "rule-merge/61 applying", "SUMMARY (rule merge 61: applied)")
}

func TestApplyTemplateWaitPartiallyAppliedExitsOne(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtReplaced, rtCreated))
	e.applyBody = rtBody("applying", "null", "", rtApplied("Waiting for validation.")...)
	failed := rtApplied("The new rule failed validation.")
	failed[1].action = "failed"
	e.getBodies = []string{rtBody("partially_applied", `"Rules: 1 replaced, 1 failed."`, "", failed...)}
	_, _, err := e.run(t, "--table-id", "42", "--yes", "--wait")
	requireExit(t, err, 1)
	rtWantContains(t, "error", err.Error(), "partially_applied", "Rules: 1 replaced, 1 failed.",
		"Next: qctl describe rule-merge 61 --table-id 42")
}

func TestApplyTemplateWaitTimeoutExits124(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtCreated))
	created := rtCreated
	created.target, created.note = rtRuleNew2, "Waiting for validation."
	e.applyBody = rtBody("applying", "null", "", created)
	e.getBodies = []string{e.applyBody}
	e.rules = append(e.rules, rtRule(rtRuleNew2, "customer-name-split-rule", "pending_validation"))
	stdout, _, err := e.run(t, "--table-id", "42", "--yes", "--wait", "--wait-timeout", "10", "-o", "name")
	requireExit(t, err, qerrors.ExitTimeout)
	if !errors.Is(err, rule_merges.ErrWaitTimeout) {
		t.Errorf("error is not the wait timeout: %v", err)
	}
	if stdout != "rule-merge/61\n" {
		t.Errorf("stdout = %q, want the merge's name", stdout)
	}
}

// When every rule the merge still waits on has passed validation and waits
// only for its draft migration, --wait stops, names the review alert and
// exits 0 while the merge is still applying.
func TestApplyTemplateWaitStopsAtAMigrationReview(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtCreated))
	created := rtCreated
	created.target, created.note = rtRuleNew2, "Waiting for review of the draft migration that adds its columns."
	e.applyBody = rtBody("applying", "null", "", created)
	e.getBodies = []string{e.applyBody}
	waiting := rtRule(rtRuleNew2, "customer-name-split-rule", "pending_validation")
	waiting["validation_state"] = "passed"
	waiting["pending_migration_model_id"] = 9
	waiting["validation_blocks_enable"] = false
	e.rules = append(e.rules, waiting)
	serveRTMigrationAlert(e, 77, 9)

	stdout, stderr, err := e.run(t, "--table-id", "42", "--yes", "--wait")
	if err != nil {
		t.Fatalf("apply --wait: %v", err)
	}
	rtWantContains(t, "stdout", stdout, "SUMMARY (rule merge 61: applying)",
		"draft migration 9: alert 77 (needs_review_migration); waiting: customer-name-split-rule",
		"qctl alert resolve 77 --action apply_migration", "qctl describe rule-merge 61 --table-id 42")
	rtWantContains(t, "stderr", stderr, "--wait: stopped waiting: rule merge 61 is still applying")
}

// The same wait runs into its timeout while the rule's validation has not
// passed, though the draft already exists.
func TestApplyTemplateWaitKeepsWaitingBeforeValidationPasses(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtCreated))
	created := rtCreated
	created.target, created.note = rtRuleNew2, "Waiting for review of the draft migration that adds its columns."
	e.applyBody = rtBody("applying", "null", "", created)
	e.getBodies = []string{e.applyBody}
	waiting := rtRule(rtRuleNew2, "customer-name-split-rule", "pending_validation")
	waiting["validation_state"] = "in_flight"
	waiting["pending_migration_model_id"] = 9
	e.rules = append(e.rules, waiting)
	serveRTMigrationAlert(e, 77, 9)

	_, _, err := e.run(t, "--table-id", "42", "--yes", "--wait", "--wait-timeout", "10")
	requireExit(t, err, qerrors.ExitTimeout)
}

// A rule listing the wait makes when it polls ends with the wait's budget:
// the wait exits 124 at --wait-timeout rather than outliving it.
func TestApplyTemplateWaitBudgetBoundsTheRuleListing(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtCreated))
	created := rtCreated
	created.target, created.note = rtRuleNew2, "Waiting for validation."
	e.applyBody = rtBody("applying", "null", "", created)
	e.getBodies = []string{e.applyBody}
	listings := 0
	e.mock.RegisterHandler("GET", rtRulesPath, func(w http.ResponseWriter, r *http.Request) {
		listings++
		if listings > 1 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Second):
			}
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": e.rules, "total_rows": len(e.rules), "page": 1})
	})
	start := time.Now()
	_, _, err := e.run(t, "--table-id", "42", "--yes", "--wait", "--wait-timeout", "1")
	requireExit(t, err, qerrors.ExitTimeout)
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("the wait took %s: the rule listing outlived the 1s budget", elapsed)
	}
}

func serveRTMigrationAlert(e *rtEnv, alertID, migrationID int) {
	e.mock.RegisterHandler("GET", rtAlertsPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []map[string]any{
			{"id": alertID, "dataset_id": rtTableID, "issue_type": "needs_review_migration"},
		}, "total_rows": 1})
	})
	e.mock.RegisterHandler("GET", fmt.Sprintf("%s/%d", rtAlertsPath, alertID), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"id": alertID, "dataset_id": rtTableID, "issue_type": "needs_review_migration", "migration_model_id": migrationID,
		})
	})
}

func TestApplyTemplateUnresolvedUnmappedExitsTwo(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtKept, rtUnmapped))
	stdout, _, err := e.run(t, "--table-id", "42", "--yes")
	requireExit(t, err, qerrors.ExitBadUsage)
	if n := e.count("POST", rtApplyPath); n != 0 {
		t.Errorf("%d apply requests, want none", n)
	}
	rtWantContains(t, "stdout", stdout, "COLUMN MAPPINGS TO ADD", `"Purchase Time":`,
		"--write-mappings")
	rtWantContains(t, "error", err.Error(), "nothing was applied: 1 conflict needs a decision: customer-name-split-rule.")
}

func TestApplyTemplateResolutionsResolveAConflict(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtDiffers, rtCreated))
	e.applyBody = rtBody("applied", `"Rules: 1 created, 1 kept."`, "", rtApplied("")...)
	resolutions := e.te.CreateFile("resolutions.yaml", `apiVersion: qluster.ai/v1
kind: RuleMergeResolution
spec:
  resolutions:
    - rule: Order Total Check
      choice: keep_target
`)
	if _, _, err := e.run(t, "--table-id", "42", "--yes", "--resolutions", resolutions); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(e.applySent) != 1 {
		t.Fatalf("%d apply requests, want 1", len(e.applySent))
	}
	var sent struct {
		Resolutions []struct {
			ItemID int    `json:"item_id"`
			Choice string `json:"choice"`
		} `json:"resolutions"`
	}
	if err := json.Unmarshal([]byte(e.applySent[0]), &sent); err != nil {
		t.Fatalf("apply body: %v", err)
	}
	if len(sent.Resolutions) != 1 || sent.Resolutions[0].ItemID != 301 || sent.Resolutions[0].Choice != "keep_target" {
		t.Errorf("apply body = %s, want item 301 keep_target", e.applySent[0])
	}
}

// rtResolutions writes a RuleMergeResolution document with entries, YAML
// list items, and returns its path.
func rtResolutions(e *rtEnv, entries string) string {
	return e.te.CreateFile("resolutions.yaml",
		"apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nspec:\n  resolutions:\n"+entries)
}

// --resolutions declining every item that would create a rule, none of them
// paired with a table rule, leaves nothing to do: no apply request, exit 0.
func TestApplyTemplateResolutionsDecliningEveryChangeIsNothingToDo(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtCreated))
	resolutions := rtResolutions(e, "    - rule: customer-name-split-rule\n      choice: keep_target\n")
	stdout, _, err := e.run(t, "--table-id", "42", "--yes", "--resolutions", resolutions)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if n := e.count("POST", rtApplyPath); n != 0 {
		t.Errorf("%d apply requests, want none", n)
	}
	rtWantContains(t, "stdout", stdout, "Nothing to do: --resolutions declines every item that would create a rule, "+
		"and none of them pairs with a table rule (rule merge 61).")
}

// With a kept entry beside the declined ones, the lineage apply carries the
// run's resolutions.
func TestApplyTemplateResolutionsDecliningEveryChangeStillRecordsLineage(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtKept, rtCreated))
	e.applyBody = rtBody("applied", `"Rules: 1 kept, 1 skipped."`, "", rtKept)
	resolutions := rtResolutions(e, "    - rule: customer-name-split-rule\n      choice: keep_target\n")
	stdout, _, err := e.run(t, "--table-id", "42", "--yes", "--resolutions", resolutions)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(e.applySent) != 1 || !strings.Contains(e.applySent[0], `"item_id":302`) ||
		!strings.Contains(e.applySent[0], `"choice":"keep_target"`) {
		t.Errorf("apply requests = %q, want one declining item 302", e.applySent)
	}
	rtWantContains(t, "stdout", stdout, "Nothing to do: --resolutions declines every item",
		"Recorded the template's lineage for Order Total Check")
}

// Declining an item paired with a table rule is still applied, though no rule
// is created: the applied merge records the decline.
func TestApplyTemplateResolutionsDecliningAPairedItemIsApplied(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtDiffers, rtCreated))
	e.applyBody = rtBody("applied", `"Rules: 1 kept, 1 skipped."`, "", rtKept)
	resolutions := rtResolutions(e, "    - rule: Order Total Check\n      choice: keep_target\n"+
		"    - rule: customer-name-split-rule\n      choice: keep_target\n")
	if _, _, err := e.run(t, "--table-id", "42", "--yes", "--resolutions", resolutions); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if n := e.count("POST", rtApplyPath); n != 1 {
		t.Errorf("%d apply requests, want 1", n)
	}
}

// rtDraftMigration is the preview's draft_migration apply blocker.
const rtDraftMigration = `[{"code":"draft_migration","message":"This table has a draft migration, and the merge would ` +
	`create a rule with produced fields. Apply or discard the draft migration first."}]`

// A draft_migration blocker refuses only while an item the apply creates has
// produced fields: declining the one that has them lets the apply through.
func TestApplyTemplateDraftMigrationJudgedAgainstTheResolutions(t *testing.T) {
	producing := rtCreated
	producing.produced = `[{"rule_field_name":"First Name","dataset_field_name":"first_name"}]`

	e := newRTEnv(t, rtBody("previewed", "null", rtDraftMigration, rtReplaced, producing))
	_, _, err := e.run(t, "--table-id", "42", "--yes")
	requireExit(t, err, qerrors.ExitBadUsage)
	rtWantContains(t, "error", err.Error(), "draft_migration")
	if n := e.count("POST", rtApplyPath); n != 0 {
		t.Errorf("%d apply requests with the producing item applied, want none", n)
	}

	e = newRTEnv(t, rtBody("previewed", "null", rtDraftMigration, rtReplaced, producing))
	e.applyBody = rtBody("applied", `"Rules: 1 replaced."`, "", rtApplied("")...)
	resolutions := rtResolutions(e, "    - rule: customer-name-split-rule\n      choice: keep_target\n")
	if _, _, err := e.run(t, "--table-id", "42", "--yes", "--resolutions", resolutions); err != nil {
		t.Fatalf("apply with the producing item declined: %v", err)
	}
	if n := e.count("POST", rtApplyPath); n != 1 {
		t.Errorf("%d apply requests with the producing item declined, want 1", n)
	}
}

func TestApplyTemplateBadResolutionEntryExitsTwo(t *testing.T) {
	for name, entry := range map[string]string{
		"names no item":       "    - rule: no-such-entry\n      choice: keep_target\n",
		"a choice it lacks":   "    - rule: Order Total Check\n      choice: keep_both\n",
		"a created item take": "    - rule: customer-name-split-rule\n      choice: take_source\n",
	} {
		t.Run(name, func(t *testing.T) {
			e := newRTEnv(t, rtBody("previewed", "null", "", rtDiffers, rtCreated))
			resolutions := e.te.CreateFile("resolutions.yaml",
				"apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nspec:\n  resolutions:\n"+entry)
			_, _, err := e.run(t, "--table-id", "42", "--yes", "--resolutions", resolutions)
			requireExit(t, err, qerrors.ExitBadUsage)
			if n := e.count("POST", rtApplyPath); n != 0 {
				t.Errorf("%d apply requests, want none", n)
			}
		})
	}
}

// The change review gate's refusal reaches the user as apierror renders it,
// with its exit code.
// A --resolutions entry the template's keys alone refuse exits 2 before any
// request, so no preview is stored.
func TestApplyTemplateResolutionsCheckedBeforeThePreview(t *testing.T) {
	for name, entry := range map[string]string{
		"names no template key": "    - rule: no-such-entry\n      choice: keep_target\n",
		"keep_both":             "    - rule: Order Total Check\n      choice: keep_both\n",
	} {
		t.Run(name, func(t *testing.T) {
			e := newRTEnv(t, rtBody("previewed", "null", "", rtDiffers, rtCreated))
			resolutions := e.te.CreateFile("resolutions.yaml",
				"apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nspec:\n  resolutions:\n"+entry)
			_, _, err := e.run(t, "--table-id", "42", "--yes", "--resolutions", resolutions)
			requireExit(t, err, qerrors.ExitBadUsage)
			if n := len(e.mock.Requests()); n != 0 {
				t.Errorf("%d requests, want none: %v", n, e.mock.Requests())
			}
		})
	}
}

func TestApplyTemplateChangeReviewPolicyDenied(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtCreated))
	e.applyStatus = http.StatusForbidden
	e.applyBody = `{"detail":{"msg":"This table's change review policy does not let the agent role resolve this change.",` +
		`"error_code":"CHANGE_REVIEW_POLICY_DENIED",` +
		`"context":{"operation":"rule_merge_apply","verb":"resolve","denied_keys":[["rules"]]}}}`
	_, _, err := e.run(t, "--table-id", "42", "--yes")
	requireExit(t, err, qerrors.ExitUnauthorized)
	rtWantContains(t, "error", err.Error(), "The policy does not let your role resolve rule_merge_apply key rules.",
		"qctl get change-review-policy --table <table>")
	if n := strings.Count(err.Error(), "change-review-policy"); n != 1 {
		t.Errorf("the error names the policy command %d times, want once:\n%s", n, err)
	}
}

func TestApplyTemplateConflictsFromTheApply(t *testing.T) {
	for name, tc := range map[string]struct{ msg, want string }{
		"another merge applying": {
			"Rule merge 60 into this table is applying; apply this one once it finishes.",
			"qctl describe rule-merge 60 --table-id 42",
		},
		"stale preview": {
			"The table's rules changed since the preview; preview the rule template again.",
			"Run 'qctl apply -f ",
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newRTEnv(t, rtBody("previewed", "null", "", rtCreated))
			e.applyStatus = http.StatusConflict
			e.applyBody = fmt.Sprintf(`{"detail":{"msg":%q}}`, tc.msg)
			_, _, err := e.run(t, "--table-id", "42", "--yes")
			requireExit(t, err, qerrors.ExitBadUsage)
			rtWantContains(t, "error", err.Error(), "Nothing was applied.", tc.want)
		})
	}
}

func TestApplyTemplateBlockerRefusesBeforeTheApply(t *testing.T) {
	blockers := `[{"code":"merge_applying","message":"Rule merge 60 into this table is applying; apply this one once it finishes."}]`
	e := newRTEnv(t, rtBody("previewed", "null", blockers, rtCreated))
	_, _, err := e.run(t, "--table-id", "42", "--yes")
	requireExit(t, err, qerrors.ExitBadUsage)
	if n := e.count("POST", rtApplyPath); n != 0 {
		t.Errorf("%d apply requests, want none", n)
	}
	rtWantContains(t, "error", err.Error(), "merge_applying", "qctl describe rule-merge 60 --table-id 42")
}

func TestApplyTemplateOutputFormats(t *testing.T) {
	e := newRTEnv(t, rtBody("previewed", "null", "", rtCreated))
	created := rtCreated
	created.target = rtRuleNew2
	e.applyBody = rtBody("applied", `"Rules: 1 created."`, "", created)

	stdout, stderr, err := e.run(t, "--table-id", "42", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("apply -o json: %v", err)
	}
	var doc struct {
		ID    int    `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc.ID != rtMergeID || doc.State != "applied" {
		t.Errorf("stdout is not the applied merge: %v\n%s", err, stdout)
	}
	rtWantContains(t, "stderr", stderr, "SUMMARY (rule merge 61: applied)")

	stdout, _, err = e.run(t, "--table-id", "42", "--yes", "-o", "name")
	if err != nil || stdout != "rule-merge/61\n" {
		t.Errorf("-o name: stdout = %q, err = %v", stdout, err)
	}
}

// Every refusal below exits 2 before any request.
func TestApplyTemplateLocalRefusals(t *testing.T) {
	const table = "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\nspec: {}\n"
	for name, tc := range map[string]struct {
		file string
		args []string
		want string
	}{
		"--table-id on another kind":    {table, []string{"--table-id", "42", "--yes"}, "--table-id applies only to kind TableRuleTemplate"},
		"--resolutions on another kind": {table, []string{"--resolutions", "r.yaml", "--yes"}, "--resolutions applies only to kind TableRuleTemplate"},
		"no target":                     {rtFile, []string{"--yes"}, "table is required"},
		"two documents":                 {rtFile + "---\n" + rtFile, []string{"--table-id", "42", "--yes"}, "must be the only document in its file"},
		"no --yes without a terminal":   {rtFile, []string{"--table-id", "42"}, "pass --yes"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newRTEnv(t, rtBody("previewed", "null", "", rtCreated))
			path := e.te.CreateFile("manifest.yaml", tc.file)
			_, _, err := execApplyRoot(t, append([]string{"apply", "-f", path}, tc.args...)...)
			requireExit(t, err, qerrors.ExitBadUsage)
			rtWantContains(t, "error", err.Error(), tc.want)
			if n := len(e.mock.Requests()); n != 0 {
				t.Errorf("%d requests, want none: %v", n, e.mock.Requests())
			}
		})
	}
}
