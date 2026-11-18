package rows

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestSendToQuarantine_SendsHold(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine", `{"result":"ok"}`)

	if _, _, err := runRows(t, "rows", "send-to-quarantine", "orders",
		"--row-id", "7", "--row-id", "8", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if body["quarantine_hold"] != "hold" {
		t.Errorf("quarantine_hold = %v, want \"hold\"", body["quarantine_hold"])
	}
}

// reprocess-clean is the SAME route with "undecided" — cosmosview's clean-tab
// "Reprocess rows", and what the retired JSON null used to mean.
//
// Three assertions, because there are three ways to lose it. An OMITTED key is
// not the same thing: quarantine_hold has omitempty on this schema, so an
// omitted value takes the server's default, which is `hold` and would PARK the
// rows this verb promised to reprocess. A JSON NULL is not the same thing
// either, and is no longer even legal — quarantine_hold is non-nullable here
// (atlas MoveRowsToQuarantineRequestSchema.quarantine_hold,
// capi/capi/schemas/data_schema.py), so the old body would 422.
// And the value itself must be the word, not "release": release is a different
// outcome on the way back out.
func TestReprocessClean_SendsUndecided(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine", `{"result":"ok"}`)

	if _, _, err := runRows(t, "rows", "reprocess-clean", "orders", "--row-id", "7", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	v, present := body["quarantine_hold"]
	if !present {
		t.Fatal("quarantine_hold is absent; an omitted value takes the server default (hold) and parks the rows")
	}
	if v != "undecided" {
		t.Errorf("quarantine_hold = %v, want \"undecided\"", v)
	}
	for key, value := range body {
		if value == nil && key != "comment" && key != "comment_is_internal" {
			t.Errorf("body = %s, want no JSON null in %s — the route refuses one", got.body, key)
		}
	}
}

// The --row-id form sends the ids as a plain list.
func TestSendToQuarantine_SendsTheRowIDs(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine", `{"result":"ok"}`)

	if _, _, err := runRows(t, "rows", "send-to-quarantine", "orders",
		"--row-id", "7", "--row-id", "8", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 {
		t.Fatalf("move-rows-to-quarantine hits = %d, want 1", got.hits)
	}
	var body struct {
		IDs []int `json:"ids"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if len(body.IDs) != 2 || body.IDs[0] != 7 || body.IDs[1] != 8 {
		t.Errorf("ids = %v, want [7 8]", body.IDs)
	}
}

// Neither move verb's support row declares a cloud list, so addScopeFlags
// registers no cloud flags at all and cobra's own unknown-flag error is the
// refusal — the designed answer per addScopeFlags' doc comment.
//
// The MESSAGE is asserted, not just the exit code: every client-side refusal in
// this group is bad usage too, so an exit-code-only test would pass on a run
// refused for some entirely different reason and prove nothing about the flag
// being unregistered.
func TestSendToQuarantine_CloudSourceScopeIsBadUsage(t *testing.T) {
	newRowsTestEnv(t)
	_, _, err := runRows(t, "rows", "send-to-quarantine", "orders", "--cloud-source-id", "7", "--row-id", "7", "--yes")
	if err == nil {
		t.Fatal("expected cloud-source scope to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), cobraUnknownFlag(t, "cloud-source-id")) {
		t.Errorf("error = %q, want cobra's unknown-flag refusal", err.Error())
	}
}

func TestReprocessClean_FileScopeIsBadUsage(t *testing.T) {
	newRowsTestEnv(t)
	_, _, err := runRows(t, "rows", "reprocess-clean", "orders", "--file-id", "3", "--row-id", "7", "--yes")
	if err == nil {
		t.Fatal("expected file scope to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), cobraUnknownFlag(t, "file-id")) {
		t.Errorf("error = %q, want cobra's unknown-flag refusal", err.Error())
	}
}

// NEITHER verb takes --status: MoveRowsToQuarantineRequestSchema has ids and
// quarantine_hold and nothing else, and MoveAllRowsToQuarantineRequestSchema
// has quarantine_hold alone. Both cells are driven, because a claim about both verbs is not evidenced
// by testing one.
func TestMoveVerbs_StatusSelectorIsUnregistered(t *testing.T) {
	for _, verb := range []string{"send-to-quarantine", "reprocess-clean"} {
		t.Run(verb, func(t *testing.T) {
			newRowsTestEnv(t)
			_, _, err := runRows(t, "rows", verb, "orders", "--status", "failed_validation", "--yes")
			if err == nil {
				t.Fatal("expected --status to be refused")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			// Same reason as above: without the message this passes against a
			// stubbed RunE.
			if !strings.Contains(err.Error(), cobraUnknownFlag(t, "status")) {
				t.Errorf("error = %q, want cobra's unknown-flag refusal", err.Error())
			}
		})
	}
}

// --all is a SECOND route, not a wildcard over the by-id one, and each verb's
// quarantine_hold reaches it exactly as it reaches the by-id route.
//
// The negative route assertion is load-bearing: a `--all` that fell through to
// move-rows-to-quarantine would send `{"ids":[],"quarantine_hold":...}`, which
// the backend answers 422 to and which no assertion on the move-all route alone
// would notice if both were called. The whole body is pinned because an omitted
// quarantine_hold takes the server's default, hold, and would park the rows
// reprocess-clean promised to reprocess.
func TestMoveVerbs_AllUsesTheMoveAllRouteWithTheVerbsHold(t *testing.T) {
	for _, tc := range []struct {
		verb     string
		wantBody string
	}{
		{"send-to-quarantine", `{"comment":null,"quarantine_hold":"hold"}`},
		{"reprocess-clean", `{"comment":null,"quarantine_hold":"undecided"}`},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			all := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-all-rows-to-quarantine",
				`{"result":"ok","msg":"Moved 3 rows to quarantine.","moved":3,"already_in_quarantine":0,"not_found":0}`)
			byID := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine", `{"result":"ok"}`)

			if _, _, err := runRows(t, "rows", tc.verb, "orders", "--all", "--yes"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if all.hits != 1 {
				t.Errorf("move-all-rows-to-quarantine hits = %d, want 1", all.hits)
			}
			if byID.hits != 0 {
				t.Errorf("move-rows-to-quarantine hits = %d, want 0 — --all is a different route", byID.hits)
			}
			if all.body != tc.wantBody {
				t.Errorf("body = %q, want %s", all.body, tc.wantBody)
			}
		})
	}
}

// reprocess-clean --all reports a reprocess, not a park: the result line says
// the rows went for reprocessing and the follow-up reads the CLEAN table, the
// one the rows return to. Both come from the hold runMove sends, so they are
// the --row-id form's lines at the other selector.
func TestReprocessClean_AllReportsAReprocess(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-all-rows-to-quarantine",
		`{"result":"ok","moved":3,"already_in_quarantine":0,"not_found":0}`)

	stdout, _, err := runRows(t, "rows", "reprocess-clean", "orders", "--all", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "3 rows moved to quarantine for reprocessing in table orders (id: 42)\n") {
		t.Errorf("stdout = %q, want the reprocessing moved count", stdout)
	}
	if !strings.Contains(stdout, "  qctl get data orders\n") {
		t.Errorf("stdout = %q, want the clean follow-up line", stdout)
	}
	if strings.Contains(stdout, "--is-bad-data") {
		t.Errorf("stdout = %q, must not read back the quarantine table", stdout)
	}
}

func TestSendToQuarantine_NoSelectorIsBadUsage(t *testing.T) {
	newRowsTestEnv(t)
	_, _, err := runRows(t, "rows", "send-to-quarantine", "orders", "--yes")
	if err == nil {
		t.Fatal("expected a missing row selector to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "--row-id") {
		t.Errorf("error = %q, want it to name --row-id", err.Error())
	}
}

// The two verbs move rows in opposite directions, so their follow-up lines must
// name different tables. send-to-quarantine parks rows in quarantine, so the
// line that reads them back carries --is-bad-data.
func TestSendToQuarantine_FollowUpLineNamesTheQuarantinePopulation(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine", `{"result":"ok"}`)

	stdout, _, err := runRows(t, "rows", "send-to-quarantine", "orders", "--row-id", "7", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "  qctl get data orders --is-bad-data\n") {
		t.Errorf("stdout = %q, want the --is-bad-data follow-up line", stdout)
	}
}

// The prompt's missing-count reason must name the CLEAN-row count for both
// move verbs, because that is the population move-all-rows-to-quarantine reads.
// Before support.all existed the clause said "quarantined-row count" to every
// --all, which for these verbs is the count of the rows the action does not
// touch — handed to a user about to empty a table's clean side.
//
// The negative is the assertion that can fail: both strings contain the word
// "count", and the whole defect was one of them being printed instead of the
// other.
func TestMoveVerbs_AllPromptNamesTheCleanRowCount(t *testing.T) {
	for _, verb := range []string{"send-to-quarantine", "reprocess-clean"} {
		t.Run(verb, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-all-rows-to-quarantine", `{"result":"ok"}`)

			_, stderr, err := runRows(t, "rows", verb, "orders", "--all", "--yes")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stderr, "'qctl describe table orders' reports the table's clean-row count.") {
				t.Errorf("stderr = %q, want the clean-row count recipe", stderr)
			}
			if strings.Contains(stderr, "quarantined-row count") {
				t.Errorf("stderr = %q, names the population this verb does not act on", stderr)
			}
		})
	}
}

// The control for the test above: the SAME clause, same scope, same selector,
// for a verb whose --all does read the quarantine table. Without it the
// negative above would pass against a clause that had simply stopped naming a
// count at all.
func TestQuarantinedAllPromptStillNamesTheQuarantinedRowCount(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-all-bad-rows",
		`{"result":"ok","affected_rows":3}`)

	_, stderr, err := runRows(t, "rows", "reprocess-quarantined", "orders", "--all", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "'qctl describe table orders' reports the table's quarantined-row count.") {
		t.Errorf("stderr = %q, want the quarantined-row count recipe", stderr)
	}
}

// --all parks the rows, so its follow-up line reads them back from quarantine
// exactly as the by-id form's does. The population comes from the hold value
// runMove sends, and --all takes the same hold, so the two cannot disagree.
func TestSendToQuarantine_AllFollowUpLineNamesTheQuarantinePopulation(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-all-rows-to-quarantine", `{"result":"ok","moved":3,"already_in_quarantine":0,"not_found":0}`)

	stdout, _, err := runRows(t, "rows", "send-to-quarantine", "orders", "--all", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "  qctl get data orders --is-bad-data\n") {
		t.Errorf("stdout = %q, want the --is-bad-data follow-up line", stdout)
	}
	if !strings.Contains(stdout, "3 rows moved to quarantine in table orders (id: 42)\n") {
		t.Errorf("stdout = %q, want actual moved count", stdout)
	}
}

func TestSendToQuarantine_NegatedAllIsRefused(t *testing.T) {
	mock := newRowsTestEnv(t)
	all := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-all-rows-to-quarantine", `{"result":"ok"}`)

	_, _, err := runRows(t, "rows", "send-to-quarantine", "orders", "--all=false", "--yes")
	if err == nil {
		t.Fatal("expected --all=false to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "--all=false is not a row selector") {
		t.Errorf("error = %q", err.Error())
	}
	if !strings.Contains(err.Error(), "--row-id") {
		t.Errorf("error = %q, want the alternative named", err.Error())
	}
	if all.hits != 0 {
		t.Errorf("--all=false reached the parking route %d time(s)", all.hits)
	}
}

// reprocess-clean returns rows to the clean table when they pass, so its
// follow-up line is the plain one — and must not carry a dangling argument.
func TestReprocessClean_FollowUpLineNamesTheCleanPopulation(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine", `{"result":"ok"}`)

	stdout, _, err := runRows(t, "rows", "reprocess-clean", "orders", "--row-id", "7", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "  qctl get data orders\n") {
		t.Errorf("stdout = %q, want a clean follow-up line", stdout)
	}
	if strings.Contains(stdout, "--is-bad-data") {
		t.Errorf("stdout = %q, must not read back the quarantine table", stdout)
	}
}

// The prompt must say the rows leave the clean table, because that is the part
// a user does not expect from a verb named "reprocess".
func TestReprocessClean_PromptSaysRowsLeaveTheCleanTable(t *testing.T) {
	newRowsTestEnv(t)
	_, stderr, err := runRows(t, "rows", "reprocess-clean", "orders", "--row-id", "7")
	if err == nil {
		t.Fatal("expected the non-interactive confirmation refusal")
	}
	if !strings.Contains(stderr, "leave the clean table") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestSendToQuarantine_PromptSaysRowsLeaveTheCleanTable(t *testing.T) {
	newRowsTestEnv(t)
	_, stderr, err := runRows(t, "rows", "send-to-quarantine", "orders", "--row-id", "7")
	if err == nil {
		t.Fatal("expected the non-interactive confirmation refusal")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(stderr, "leave the clean table") {
		t.Errorf("stderr = %q", stderr)
	}
}

// --table-id names the table without a positional, end to end, for THIS verb.
// The group-wide claim it used to make here is now pinned by
// TestEveryVerbTakesTheTablePositionalOptionally in scope_test.go, which walks
// the real command tree; this one proves the property actually works through a
// full run rather than only at the Args function.
func TestReprocessClean_TableIDFlagWithoutPositional(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine", `{"result":"ok"}`)

	stdout, _, err := runRows(t, "rows", "reprocess-clean", "--table-id", "42", "--row-id", "7", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 {
		t.Errorf("move-rows-to-quarantine hits = %d, want 1", got.hits)
	}
	// No name was supplied, so every printed reference falls back to the id.
	if !strings.Contains(stdout, "  qctl get data 42\n") {
		t.Errorf("stdout = %q, want the id-based follow-up line", stdout)
	}
}

// The third instance of the shape deleteRows' and reprocessRows' scope switches
// carry, and the reason moveRows is a function of its own: BOTH move routes are
// DATASET routes and neither takes a narrowing parameter, so a support{} edit
// adding `cloud` to moveSupport would send the table-scoped request while the
// prompt named a cloud source.
//
// The default must be REACHED, not merely present. The server URL is
// unroutable, so a fall-through would fail this test rather than pass quietly.
func TestMoveRows_RefusesANonTableScope(t *testing.T) {
	ctx := &cmdutil.CommandContext{
		ServerURL:  "http://127.0.0.1:1",
		Credential: &auth.Credential{AccessToken: "t"},
	}
	for _, sc := range []scope{
		{kind: scopeCloudSource, tableID: 42, tableName: "orders", cloudSourceID: 7},
		{kind: scopeFile, tableID: 42, tableName: "orders", fileID: 3},
	} {
		t.Run(sc.kindName(), func(t *testing.T) {
			res, err := moveRows(ctx, sc, selector{kind: selRowIDs, rowIDs: []int{7}}, api.QuarantineHoldHold, "send-to-quarantine", datasets.MoveComment{})
			if err == nil {
				t.Fatalf("%s scope was routed instead of refused; got %+v", sc.kindName(), res)
			}
			if code := qerrors.GetExitCode(err); code != 1 {
				t.Errorf("GetExitCode = %d, want 1", code)
			}
			if !strings.Contains(err.Error(), "internal error: 'qctl rows send-to-quarantine' has no route for selector \"--row-id\" at "+sc.kindName()+" scope") {
				t.Errorf("error = %v, want unroutableSelector's message", err)
			}
		})
	}
}

// A 409 PERIOD_LOCKED from move-rows-to-quarantine reaches qctl's parser with
// its message and code intact, exits 2, and prints the code's hint once.
func TestSendToQuarantine_PeriodLockedExitsBadUsageWithTheHint(t *testing.T) {
	mock := newRowsTestEnv(t)
	hits := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine",
		func(w http.ResponseWriter, _ *http.Request) {
			hits++
			testutil.RespondJSON(w, http.StatusConflict, json.RawMessage(
				`{"detail":{"msg":"Period '2026-07' is locked. Unlock it before changing its data.",`+
					`"error_code":"PERIOD_LOCKED","title":"Error","severity":"error"}}`))
		})

	_, _, err := runRows(t, "rows", "send-to-quarantine", "orders", "--row-id", "7", "--yes")
	if err == nil {
		t.Fatal("want the PERIOD_LOCKED refusal, got nil")
	}
	if hits != 1 {
		t.Fatalf("move-rows-to-quarantine hits = %d, want 1", hits)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
	}
	if code := apierror.ErrorCodeOf(err); code != apierror.CodePeriodLocked {
		t.Errorf("error_code in the chain = %q, want %q: %v", code, apierror.CodePeriodLocked, err)
	}
	for _, want := range []string{
		"Period '2026-07' is locked. Unlock it before changing its data.",
		"Code: PERIOD_LOCKED",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	testutil.AssertOnePeriodUnlockHint(t, err)
}

const moveRowsRoute = "/api/orgs/" + testOrgID + "/datasets/42/move-rows-to-quarantine"

// --comment rides on the --row-id route as comment, leaves comment_is_internal
// to the server's per-caller default, and the result names the thread.
func TestSendToQuarantine_CommentOpensAThread(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", moveRowsRoute,
		`{"result":"ok","moved":2,"already_in_quarantine":0,"not_found":0,"comment_thread_id":"0190bbbb-1111-2222-3333-444444444444","notified_user_count":null}`)

	stdout, _, err := runRows(t, "rows", "send-to-quarantine", "orders",
		"--row-id", "7", "--row-id", "8", "--comment", "wrong currency", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if body["comment"] != "wrong currency" {
		t.Errorf("comment = %v, want %q", body["comment"], "wrong currency")
	}
	if body["comment_is_internal"] != nil {
		t.Errorf("comment_is_internal = %v, want null (the server's per-caller default)", body["comment_is_internal"])
	}
	if !strings.Contains(stdout, "qctl describe comment-thread 0190bbbb-1111-2222-3333-444444444444 --table-id 42") {
		t.Errorf("stdout = %q, want the describe comment-thread follow-up", stdout)
	}
}

// Without --comment the body carries both comment fields as null — the
// request every earlier qctl sent.
func TestSendToQuarantine_NoCommentSendsNulls(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", moveRowsRoute, `{"result":"ok","moved":1,"already_in_quarantine":0,"not_found":0}`)

	stdout, stderr, err := runRows(t, "rows", "send-to-quarantine", "orders", "--row-id", "7", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	for _, key := range []string{"comment", "comment_is_internal"} {
		if v, ok := body[key]; ok && v != nil {
			t.Errorf("%s = %v, want null or absent", key, v)
		}
	}
	if strings.Contains(stdout+stderr, "comment") {
		t.Errorf("output mentions a comment nobody sent:\n%s%s", stdout, stderr)
	}
}

func TestSendToQuarantine_VisibleToSenderSendsFalse(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", moveRowsRoute,
		`{"result":"ok","moved":1,"already_in_quarantine":0,"not_found":0,"comment_thread_id":"0190bbbb-1111-2222-3333-444444444444"}`)

	if _, _, err := runRows(t, "rows", "send-to-quarantine", "orders",
		"--row-id", "7", "--comment", "please resend", "--visible-to-sender", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if v, ok := body["comment_is_internal"]; !ok || v != false {
		t.Errorf("comment_is_internal = %v (present %t), want false", v, ok)
	}
}

// The move-all form sends comment only, and reports the notified count.
func TestReprocessCleanAll_CommentNotifiesFollowers(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-all-rows-to-quarantine",
		`{"result":"ok","moved":5,"already_in_quarantine":0,"not_found":0,"comment_thread_id":null,"notified_user_count":3}`)

	stdout, _, err := runRows(t, "rows", "reprocess-clean", "orders", "--all", "--comment", "new rules", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if body["comment"] != "new rules" {
		t.Errorf("comment = %v, want %q", body["comment"], "new rules")
	}
	if _, ok := body["comment_is_internal"]; ok {
		t.Errorf("body = %s, want no comment_is_internal on the move-all route", got.body)
	}
	if !strings.Contains(stdout, "Comment sent to 3 people") {
		t.Errorf("stdout = %q, want the notified count", stdout)
	}
}

// A --comment the server ignored (no thread id back) is warned about, not
// silently reported as kept.
func TestSendToQuarantine_CommentWithoutThreadWarns(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", moveRowsRoute, `{"result":"ok","moved":1,"already_in_quarantine":0,"not_found":0}`)

	_, stderr, err := runRows(t, "rows", "send-to-quarantine", "orders",
		"--row-id", "7", "--comment", "x", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "no comment thread") {
		t.Errorf("stderr = %q, want a missing-thread warning", stderr)
	}
}

// Every combination the server would refuse is refused locally, with exit 2
// and no request.
func TestMoveComment_LocalRefusals(t *testing.T) {
	manyIDs := make([]string, 0, 2*(maxMoveCommentRows+1))
	for i := 1; i <= maxMoveCommentRows+1; i++ {
		manyIDs = append(manyIDs, "--row-id", strconv.Itoa(i))
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"visible without comment", []string{"send-to-quarantine", "orders", "--row-id", "7", "--visible-to-sender"}},
		{"visible with all", []string{"send-to-quarantine", "orders", "--all", "--comment", "x", "--visible-to-sender"}},
		{"visible with shared", []string{"send-to-quarantine", "orders", "--shared", "--row-id", "7", "--comment", "x", "--visible-to-sender"}},
		{"blank comment", []string{"reprocess-clean", "orders", "--row-id", "7", "--comment", "  "}},
		{"long comment", []string{"send-to-quarantine", "orders", "--all", "--comment", strings.Repeat("a", maxMoveCommentLength+1)}},
		{"too many rows", append([]string{"send-to-quarantine", "orders", "--comment", "x"}, manyIDs...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			rows := record(mock, "POST", moveRowsRoute, `{"result":"ok"}`)
			all := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-all-rows-to-quarantine", `{"result":"ok"}`)

			_, _, err := runRows(t, append(append([]string{"rows"}, tc.args...), "--yes")...)
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Fatalf("exit = %d (%v), want 2", code, err)
			}
			if rows.hits+all.hits != 0 {
				t.Errorf("a refused command made %d move request(s)", rows.hits+all.hits)
			}
		})
	}
}
