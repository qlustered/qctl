package rows

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestReprocessQuarantined_ByIDsUsesTheScopedRoute(t *testing.T) {
	mock := newRowsTestEnv(t)
	scoped := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows",
		`{"result":"ok","affected_rows":2}`)
	all := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-all-bad-rows", `{"result":"ok"}`)

	if _, _, err := runRows(t, "rows", "reprocess-quarantined", "orders",
		"--row-id", "7", "--row-id", "8", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scoped.hits != 1 || all.hits != 0 {
		t.Fatalf("scoped=%d all=%d, want 1 and 0", scoped.hits, all.hits)
	}
	if scoped.query != "" {
		t.Errorf("query = %q, want empty — no status was given", scoped.query)
	}
	if scoped.body != `{"ids":[7,8]}` {
		t.Errorf("body = %q", scoped.body)
	}
}

func TestReprocessQuarantined_ByStatusSendsEmptyIDs(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows",
		`{"result":"ok","affected_rows":9}`)

	if _, _, err := runRows(t, "rows", "reprocess-quarantined", "orders",
		"--status", "on_hold", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.query != "quarantine_status=on_hold" {
		t.Errorf("query = %q", got.query)
	}
	if got.body != `{"ids":[]}` {
		t.Errorf("body = %q, want {\"ids\":[]} — a nil slice marshals to null", got.body)
	}
}

// --all at table scope takes the all-bad-rows route, which is the whole
// selector: no body, no status filter. The scoped route must not be touched.
func TestReprocessQuarantined_AllUsesTheAllBadRowsRoute(t *testing.T) {
	mock := newRowsTestEnv(t)
	scoped := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows", `{"result":"ok"}`)
	all := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-all-bad-rows",
		`{"result":"ok","affected_rows":120}`)

	stdout, _, err := runRows(t, "rows", "reprocess-quarantined", "orders", "--all", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if all.hits != 1 || scoped.hits != 0 {
		t.Errorf("hits: reprocess-all-bad-rows = %d (want 1), reprocess-bad-rows = %d (want 0)", all.hits, scoped.hits)
	}
	if !strings.Contains(stdout, "120 row(s) affected") {
		t.Errorf("stdout = %q, want the affected-row count", stdout)
	}
}

func TestReprocessQuarantined_AllAtCloudSourceScope(t *testing.T) {
	mock := newRowsTestEnv(t)
	// The cloud source itself must RESOLVE: hydrateScope looks it up before the
	// route is called, so without this fixture the GET 404s and the run fails at
	// exit 3 on the scope, never reaching the route under test.
	registerCloudSourceResolve(mock)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/reprocess-all-bad-rows",
		`{"result":"ok","affected_rows":31}`)

	stdout, _, err := runRows(t, "rows", "reprocess-quarantined", "orders",
		"--cloud-source-id", "7", "--all", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 {
		t.Errorf("hits = %d, want 1", got.hits)
	}
	if !strings.Contains(stdout, "31 row(s) affected") {
		t.Errorf("stdout = %q", stdout)
	}
}

// The follow-up line must read back the rows this run acted on. At cloud-source
// scope that is ONE source's quarantine, not the whole table's — a hand-built
// "qctl get data <table> --is-bad-data" drops the narrowing and sends the user
// to a listing whose rows the command never touched.
func TestReprocessQuarantined_FollowUpLineNarrowsToTheCloudSource(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerCloudSourceResolve(mock)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/reprocess-all-bad-rows",
		`{"result":"ok","affected_rows":31}`)

	stdout, _, err := runRows(t, "rows", "reprocess-quarantined", "orders",
		"--cloud-source-id", "7", "--all", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "  qctl get data orders --data-source-model-id 7 --is-bad-data\n") {
		t.Errorf("stdout = %q, want a follow-up narrowed to the cloud source", stdout)
	}
}

// At table scope the same line names the quarantine population and nothing
// else — no narrowing flag, and no dangling argument where one would have gone.
func TestReprocessQuarantined_FollowUpLineNamesTheQuarantinePopulation(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows",
		`{"result":"ok","affected_rows":1}`)

	stdout, _, err := runRows(t, "rows", "reprocess-quarantined", "orders", "--row-id", "7", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "  qctl get data orders --is-bad-data\n") {
		t.Errorf("stdout = %q, want the --is-bad-data follow-up line", stdout)
	}
}

// At cloud-source scope reprocess-all-bad-rows IS the scoped form
// (limit_to_data_source_model_id), so --row-id and --status have no route.
func TestReprocessQuarantined_RowIDAtCloudSourceScopeIsBadUsage(t *testing.T) {
	mock := newRowsTestEnv(t)
	// Without this the cloud-source GET 404s and the failure is ExitNotFound
	// (3) from the SCOPE, so the exit-code assertion below would be checking a
	// refusal that never happened.
	registerCloudSourceResolve(mock)
	_, _, err := runRows(t, "rows", "reprocess-quarantined", "orders",
		"--cloud-source-id", "7", "--row-id", "7", "--yes")
	if err == nil {
		t.Fatal("expected --row-id to be refused at cloud-source scope")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	// Exit 2 alone cannot tell this refusal from a cobra unknown-flag error, or
	// from any other client-side refusal in this group: name the scope it is
	// about.
	if !strings.Contains(err.Error(), "at cloud-source scope") {
		t.Errorf("error does not name the offending scope: %v", err)
	}
}

func TestReprocessQuarantined_StatusAtCloudSourceScopeIsBadUsage(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerCloudSourceResolve(mock)
	_, _, err := runRows(t, "rows", "reprocess-quarantined", "orders",
		"--cloud-source-id", "7", "--status", "failed_validation", "--yes")
	if err == nil {
		t.Fatal("expected --status to be refused at cloud-source scope")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "at cloud-source scope") {
		t.Errorf("error does not name the offending scope: %v", err)
	}
}

// The verb has no --file-id at all: reprocessSupport declares no file list, so
// addScopeFlags never registers one and cobra's unknown-flag error is the right
// answer. Asserting the mechanism rather than the exit code, which every
// refusal in this group shares.
func TestReprocessQuarantined_FileScopeIsNotRegistered(t *testing.T) {
	newRowsTestEnv(t)
	_, _, err := runRows(t, "rows", "reprocess-quarantined", "orders", "--file-id", "3", "--all", "--yes")
	if err == nil {
		t.Fatal("expected --file-id to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), cobraUnknownFlag(t, "file-id")) {
		t.Errorf("error = %v, want cobra's unknown-flag refusal", err)
	}
}

// The verb must be WIRED, not refusing. The placeholder RunE this replaced
// exited 2 with "not implemented" — the same exit code every refusal test above
// asserts — so without this test they would all have passed against a verb that
// never reached the network. Task 8 deleted that placeholder; the collision
// that placeholder created is why those tests assert their messages too.
func TestReprocessQuarantined_IsWired(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows",
		`{"result":"ok","affected_rows":1}`)

	_, _, err := runRows(t, "rows", "reprocess-quarantined", "orders", "--row-id", "7", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --status passed_validation is the ONE status GetQuarantineCountByStatus can
// answer, and for THIS verb its number is only a ceiling: the bulk path adds
// `awaiting_processing_ IS NOT TRUE`, which the counter lacks. So the number
// is suppressed and the reason is printed instead.
//
// THIS test covers the --yes half, and only what that half owns: no prompt is
// shown, so the reason still has to reach the user — under the skipped-prompt
// lead, not the prompt's. It deliberately does NOT assert that the count
// request was skipped: --yes short-circuits before describeSubject for every
// verb, so that is the pre-existing --yes path rather than this verb's
// suppression, and it is pinned by TestDeleteQuarantined_YesSkipsTheCountRequest.
// The interactive half — where a number would actually have been stated — is
// the test below.
func TestReprocessQuarantined_PassedValidationYesStillExplainsTheMissingCount(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows",
		`{"result":"ok","affected_rows":8}`)

	stdout, stderr, err := runRows(t, "rows", "reprocess-quarantined", "orders",
		"--status", "passed_validation", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "The prompt --yes skipped had no count") {
		t.Errorf("--yes dropped the skipped-prompt lead: %q", stderr)
	}
	if !strings.Contains(stderr, "only a ceiling") {
		t.Errorf("--yes dropped the reason the count is missing: %q", stderr)
	}
	// Not a claim about the suppression: it pins that withholding the count did
	// not cost the run the server's own affected-row number.
	if !strings.Contains(stdout, "8 row(s) affected") {
		t.Errorf("output lost the server's own count: %q", stdout)
	}
}

// The INTERACTIVE half, which is where the contradiction actually appeared:
// with 17 passed_validation rows of which 9 are already queued, the old code
// prompted "Reprocess at least 17 row(s)…" and then printed "8 row(s)
// affected" — the CLI contradicting itself inside one run, two paragraphs below
// a --help that already says --status skips queued rows.
//
// Reached without a terminal: when stdin cannot be prompted, confirmAction
// refuses with its own nonInteractive message, which INTERPOLATES the subject
// describeSubject built. So the noun phrase the prompt would have carried is
// assertable from err.Error(), and by then the count request either was or was
// not spent.
//
// No reprocess route is registered on purpose: the run must stop at the
// confirmation, before any row is touched.
func TestReprocessQuarantined_PassedValidationPromptStatesNoCount(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	mock := newRowsTestEnv(t)
	counts := record(mock, "GET", "/api/orgs/"+testOrgID+"/datasets/42/quarantine/count-by-status",
		`{"on_hold":0,"approved":0,"held_for_approval":0,"held_for_approval_by_reason":{"possible_duplicate":0,"review_policy":0,"table_setting":0},"passed_validation":17,"failed_validation":4}`)

	_, stderr, err := runRows(t, "rows", "reprocess-quarantined", "orders",
		"--status", "passed_validation")
	if err == nil {
		t.Fatal("expected the non-interactive refusal, not a silent reprocess")
	}
	// The test's own premise, not the fix: exit 2 is confirmAction's refusal,
	// so a route failure cannot masquerade as one.
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("GetExitCode = %d, want 2 — the run did not stop at the confirmation: %v", code, err)
	}
	if counts.hits != 0 {
		t.Errorf("count-by-status hits = %d, want 0 — a number nobody may state is a request spent for nothing", counts.hits)
	}
	// The specific failure: 17 above a run that affects 8.
	if strings.Contains(err.Error(), "17 row(s)") {
		t.Errorf("the prompt states a count that is only a ceiling: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "every row with status passed_validation") {
		t.Errorf("the prompt does not name the unsized subject: %q", err.Error())
	}
	if !strings.Contains(stderr, "No count is shown") {
		t.Errorf("the prompt carried neither a number nor a reason: %q", stderr)
	}
	if !strings.Contains(stderr, "only a ceiling") {
		t.Errorf("the prompt does not explain the missing count: %q", stderr)
	}
}

// The suppression is declared per verb on support, so it must not leak to the
// verbs whose bulk path does NOT filter awaiting_processing_.
// hold, release and reset-hold go through
// _get_params_filters_and_where_clause_based_on_ids, whose filters are FALSE /
// id IN / data_source_model_id IN / keep_in_quarantine_ / passes_validation_
// and nothing else — and which takes no quarantine_hold argument, so this holds
// for all three of them equally. They inherit the count, not the suppression.
func TestOnlyTheReprocessPathSkipsQueuedRows(t *testing.T) {
	for _, tc := range []struct {
		name string
		sup  support
		want bool
	}{
		{"reprocess-quarantined", reprocessSupport, true},
		{"hold/release", holdReleaseSupport, false},
		{"send-to-quarantine/reprocess-clean", moveSupport, false},
		{"delete-quarantined", deleteQuarantinedSupport, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sup.statusSkipsQueuedRows != tc.want {
				t.Errorf("statusSkipsQueuedRows = %v, want %v", tc.sup.statusSkipsQueuedRows, tc.want)
			}
			// The behaviour, not just the field: only a verb that skips queued
			// rows loses every otherwise usable status count.
			sc := scope{kind: scopeTable, tableID: 42}
			sel := selector{kind: selStatus, status: api.QuarantineStatusPassedValidation}
			gotClause := unsizedClause(sc, sel, tc.sup) != ""
			if gotClause != tc.want {
				t.Errorf("unsizedClause non-empty = %v, want %v", gotClause, tc.want)
			}
		})
	}
}

// reprocessRows' SCOPE dispatch was `if sc.kind == scopeCloudSource { … }`
// falling THROUGH to the dataset route, so a fourth scopeKind would have
// reprocessed the whole table's quarantine rather than the narrower scope it
// named. It is a switch with named arms and a refusing default now, and this
// asserts the default is REACHED rather than merely written: reprocessSupport
// lists no file scope, so no command line can produce this.
func TestReprocessRows_RefusesAnUnroutableScope(t *testing.T) {
	ctx := &cmdutil.CommandContext{
		// Deliberately unreachable: the default must fire before any request.
		ServerURL:  "http://127.0.0.1:1",
		Credential: &auth.Credential{AccessToken: "t"},
	}
	sc := scope{kind: scopeFile, tableID: 42, tableName: "orders", fileID: 3}
	sel := selector{kind: selRowIDs, rowIDs: []int{7}}

	// The verb is THREADED now rather than a literal in the three defaults, so
	// it is passed in here too — and a name the caller chose is what proves
	// the threading: a default still carrying the literal would answer
	// "reprocess-quarantined" to this call and pass every other assertion.
	res, err := reprocessRows(ctx, sc, sel, "a-second-caller")
	if err == nil {
		t.Fatalf("file scope was routed instead of refused; got %+v", res)
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("GetExitCode = %d, want 1", code)
	}
	if !strings.Contains(err.Error(), "internal error: 'qctl rows a-second-caller' has no route for selector \"--row-id\" at file scope") {
		t.Errorf("error = %v, want unroutableSelector's message naming the CALLER's verb", err)
	}
}

// The lesson refuseAllBadRows records, applied to the other route that ignores
// the value its arm was chosen on. ReprocessAllBadRowsForDataSourceViewParams
// is an empty struct, so the cloud-source route reprocesses the WHOLE source
// whatever selector reached it. Naming the arm is not enough; the value that
// goes on the wire has to be guarded.
//
// Reachable only by a support{} edit (reprocessSupport.cloud is []{selAll}),
// which is exactly the edit this refuses. The server URL is unroutable, so a
// request would fail this test rather than pass it quietly.
func TestReprocessRows_CloudSourceRefusesANonAllSelector(t *testing.T) {
	ctx := &cmdutil.CommandContext{
		ServerURL:  "http://127.0.0.1:1",
		Credential: &auth.Credential{AccessToken: "t"},
	}
	sc := scope{kind: scopeCloudSource, tableID: 42, tableName: "orders", cloudSourceID: 7}

	for _, sel := range []selector{
		{kind: selRowIDs, rowIDs: []int{7, 8}},
		{kind: selStatus, status: api.QuarantineStatusOnHold},
		{kind: selSignatures, signatures: []string{"9f2a"}},
	} {
		t.Run(string(sel.kind), func(t *testing.T) {
			res, err := reprocessRows(ctx, sc, sel, "reprocess-quarantined")
			if err == nil {
				t.Fatalf("%s reached the all-bad-rows route; got %+v", sel.kind, res)
			}
			if code := qerrors.GetExitCode(err); code != 1 {
				t.Errorf("GetExitCode = %d, want 1", code)
			}
			if !strings.Contains(err.Error(), "at cloud-source scope") {
				t.Errorf("error = %v, want unroutableSelector's message", err)
			}
		})
	}
}

// The skipped-queue fact is printed ONCE, on both paths.
//
// It used to print twice on adjacent lines: an act.extra line ("Rows already
// queued for processing are skipped by a --status reprocess.") immediately
// above unsizedClause's reason clause ("a --status reprocess skips rows
// already queued for processing, …"), which is the same sentence in two
// wordings. confirmAction prints act.extra on both paths and the reason clause
// on both paths, so the pair appeared under --yes as well as before a prompt.
//
// Asserted by COUNTING rather than by the absence of the retired wording: a
// future edit that re-adds the fact in a third phrasing would satisfy an
// absence assertion and still print it twice.
func TestReprocessQuarantined_StatusSkipNoteIsPrintedOnce(t *testing.T) {
	const fact = "queued for processing"

	t.Run("--yes", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows",
			`{"result":"ok","affected_rows":8}`)

		stdout, stderr, err := runRows(t, "rows", "reprocess-quarantined", "orders",
			"--status", "passed_validation", "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n := strings.Count(stdout+stderr, fact); n != 1 {
			t.Errorf("the skipped-queue fact is printed %d time(s), want 1:\n%s%s", n, stderr, stdout)
		}
	})

	t.Run("prompt", func(t *testing.T) {
		if cmdutil.CanPrompt() {
			t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
		}
		mock := newRowsTestEnv(t)
		// No reprocess route: the run must stop at the confirmation.
		_ = mock

		_, stderr, err := runRows(t, "rows", "reprocess-quarantined", "orders",
			"--status", "passed_validation")
		if err == nil {
			t.Fatal("expected the non-interactive refusal")
		}
		if n := strings.Count(stderr, fact); n != 1 {
			t.Errorf("the skipped-queue fact is printed %d time(s), want 1:\n%s", n, stderr)
		}
	})
}
