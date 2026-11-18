package rows

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// hold, release and reset-hold are ONE route separated by quarantine_hold, so
// the three bodies are asserted as a set: a runner that read the direction from
// the verb name instead of its own parameter would still pass any one of them
// alone. With three values rather than two that is not a theoretical worry —
// release and reset-hold both leave the row unheld, and the wrong one of the
// two changes whether the approval path runs and whether the rows' reviews
// close.
func TestHold_SendsHoldWithIDs(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
		`{"result":"ok","affected_rows":2}`)

	if _, _, err := runRows(t, "rows", "hold", "orders", "--row-id", "7", "--row-id", "8", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 {
		t.Fatalf("update-keep-in-quarantine hits = %d, want 1", got.hits)
	}
	// The exact wire body. Ids is `*[]int` with no omitempty and
	// QuarantineStatus has omitempty, while QuarantineHold is a non-pointer
	// with no omitempty, so a by-id run sends ids and quarantine_hold and no
	// status.
	if got.body != `{"ids":[7,8],"quarantine_hold":"hold"}` {
		t.Errorf("body = %q", got.body)
	}
	if got.query != "" {
		t.Errorf("query = %q, want empty — no status was given", got.query)
	}
}

func TestRelease_SendsRelease(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
		`{"result":"ok","affected_rows":1}`)

	if _, _, err := runRows(t, "rows", "release", "orders", "--row-id", "7", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 {
		t.Fatalf("update-keep-in-quarantine hits = %d, want 1", got.hits)
	}
	if got.body != `{"ids":[7],"quarantine_hold":"release"}` {
		t.Errorf("body = %q", got.body)
	}
}

// The --status form: ids is absent (a nil *[]int marshals to null) and the
// status rides the BODY, not the query string.
//
// That is this route's own shape, and it differs from reprocess-bad-rows, whose
// status is a query parameter — UpdateKeepInQuarantineViewParams and its
// data-source twin (UpdateKeepInQuarantineForDataSourceViewParams) carry
// nothing but oauth_session (client.gen.go), while
// UpdateKeepInQuarantineRequestSchema carries quarantine_status. Asserting the
// query here would have pinned the sibling route's contract onto this one.
func TestHold_ByStatusSendsNoIDs(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
		`{"result":"ok","affected_rows":9}`)

	if _, _, err := runRows(t, "rows", "hold", "orders",
		"--status", "failed_validation", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.body != `{"ids":null,"quarantine_hold":"hold","quarantine_status":"failed_validation"}` {
		t.Errorf("body = %q", got.body)
	}
	if got.query != "" {
		t.Errorf("query = %q, want empty — this route takes the status in the body", got.query)
	}
}

// --all is legal ONLY at cloud-source scope: update_keep_in_quarantine_core
// raises 422 for an empty selector unless limit_to_data_source_model_id is set
// (data_view_core.py), and only the data-source route sets it.
func TestHold_AllIsCloudSourceOnly(t *testing.T) {
	mock := newRowsTestEnv(t)
	// hydrateScope resolves the cloud source before the route is called, so
	// without this fixture the GET 404s and the run dies at exit 3 on the
	// scope — the route assertion below would pass for the wrong reason.
	registerCloudSourceResolve(mock)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/update-keep-in-quarantine",
		`{"result":"ok","affected_rows":88}`)
	table := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
		`{"result":"ok","affected_rows":88}`)

	stdout, _, err := runRows(t, "rows", "hold", "orders", "--cloud-source-id", "7", "--all", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 || table.hits != 0 {
		t.Fatalf("hits: data-source = %d (want 1), dataset = %d (want 0)", got.hits, table.hits)
	}
	if got.body != `{"ids":null,"quarantine_hold":"hold"}` {
		t.Errorf("body = %q — --all sends no ids and no status", got.body)
	}
	if !strings.Contains(stdout, "88 row(s) affected") {
		t.Errorf("stdout = %q, want the affected-row count", stdout)
	}

	_, _, err = runRows(t, "rows", "hold", "orders", "--all", "--yes")
	if err == nil {
		t.Fatal("expected --all to be refused at table scope")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	// Exit 2 alone proves nothing: cobra's unknown-flag error and every other
	// client-side refusal in this group share it. The message must name the
	// flag AND the scope where it would have worked.
	if !strings.Contains(err.Error(), "--all is not supported by 'qctl rows hold' at table scope") {
		t.Errorf("error does not name the refused flag and scope: %v", err)
	}
	if !strings.Contains(err.Error(), "it is supported at cloud-source scope") {
		t.Errorf("error does not name the scope --all would work at: %v", err)
	}
	if table.hits != 0 {
		t.Errorf("dataset route hits = %d, want 0 — the refusal must be client-side", table.hits)
	}
}

// Under a policy that requires review, update_keep_in_quarantine_core
// (data_view_core.py) refuses a release at EVERY selector with 403 when the
// caller lacks DATA_REVIEW_APPROVE, and with 403 APPROVER_NOT_DESIGNATED when
// approve_quarantine_rows_sync (direct_approval.py) finds a row whose designated
// reviewers exclude the caller. Both exit 4, stdout carries no result line, and
// the server's own words reach the user.
func TestRelease_ApprovalRefusalExitsUnauthorizedAtEverySelector(t *testing.T) {
	refusals := []struct {
		name string
		body string
		want string
	}{
		// The backend's REAL messages: an invented one would make the assertion
		// below an echo of this handler rather than a probe of what qctl passes
		// through. The first is update_keep_in_quarantine_core's permission
		// check; the second is ReviewerNotDesignatedError's text from
		// validate_reviewers_designated (review_shared.py).
		{"no approve permission",
			`{"detail":{"msg":"Approving these rows requires the data-review approve permission."}}`,
			"requires the data-review approve permission"},
		{"not a designated reviewer",
			`{"detail":{"msg":"Reviewer(s) not in the designated reviewer set for this review: ` +
				`6ba7b810-9dad-11d1-80b4-00c04fd430c8","error_code":"APPROVER_NOT_DESIGNATED"}}`,
			"not in the designated reviewer set"},
	}
	for _, tc := range []struct {
		name  string
		route string
		args  []string
		cloud bool
	}{
		{"by id", "/datasets/42/update-keep-in-quarantine",
			[]string{"--row-id", "7"}, false},
		{"by status", "/datasets/42/update-keep-in-quarantine",
			[]string{"--status", "held_for_approval"}, false},
		{"by status at cloud-source scope", "/data-sources/7/update-keep-in-quarantine",
			[]string{"--cloud-source-id", "7", "--status", "passed_validation"}, true},
		{"--all at cloud-source scope", "/data-sources/7/update-keep-in-quarantine",
			[]string{"--cloud-source-id", "7", "--all"}, true},
	} {
		for _, refusal := range refusals {
			t.Run(tc.name+"/"+refusal.name, func(t *testing.T) {
				mock := newRowsTestEnv(t)
				if tc.cloud {
					registerCloudSourceResolve(mock)
				}
				hits := 0
				mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+tc.route,
					func(w http.ResponseWriter, _ *http.Request) {
						hits++
						testutil.RespondJSON(w, http.StatusForbidden, json.RawMessage(refusal.body))
					})

				args := append([]string{"rows", "release", "orders"}, tc.args...)
				stdout, stderr, err := runRows(t, append(args, "--yes")...)
				if err == nil {
					t.Fatal("expected the 403 to surface")
				}
				if hits != 1 {
					t.Fatalf("update-keep-in-quarantine hits = %d, want 1 — the 403 under test never happened", hits)
				}
				if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
					t.Errorf("GetExitCode = %d, want %d", code, qerrors.ExitUnauthorized)
				}
				if !strings.Contains(err.Error(), refusal.want) {
					t.Errorf("error = %v, want the server's own explanation", err)
				}
				if stdout != "" {
					t.Errorf("stdout = %q, want nothing — a refused release released no row", stdout)
				}
				// Under --yes the note is the only warning a script gets.
				if !strings.Contains(stderr, releaseApprovalNote) {
					t.Errorf("stderr does not carry the approval note: %q", stderr)
				}
			})
		}
	}
}

// The approval note prints BEFORE the prompt, so a user about to release by
// any selector learns that a review requirement turns the release into an
// approval. It is an `extra` line, which means a script running with --yes
// sees it too.
func TestRelease_PromptNamesTheReviewApproval(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	newRowsTestEnv(t)
	// No route registered: the run must stop at the confirmation, before any
	// row is touched.
	_, stderr, err := runRows(t, "rows", "release", "orders", "--row-id", "7")
	if err == nil {
		t.Fatal("expected the non-interactive confirmation refusal")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("GetExitCode = %d, want 2 — the run did not stop at the confirmation: %v", code, err)
	}
	if !strings.Contains(stderr, "data-review policy") || !strings.Contains(stderr, "gets your approval") {
		t.Errorf("stderr does not disclose that a review requirement records an approval: %q", stderr)
	}
	// The question names what the route may do under that requirement.
	if !strings.Contains(err.Error(), "pass --yes to approve 1 row(s)") {
		t.Errorf("the prompt does not ask to approve the rows: %v", err)
	}
	// R4: reprocessing is the consequence a user cannot predict from the verb's
	// name, and it must survive --yes, which is what `extra` guarantees.
	if !strings.Contains(stderr, "re-validated") {
		t.Errorf("stderr does not disclose that a release queues the rows for reprocessing: %q", stderr)
	}
	if !strings.Contains(stderr, "quarantine-raised data reviews") {
		t.Errorf("stderr does not disclose the review auto-close: %q", stderr)
	}
	// The shipped wording claimed BOTH sweeps unconditionally for a whole
	// review round, so this is a tripwire rather than a redundant negative. It
	// bans the CLAIM, not the noun: the auto-close is origin-conditional and
	// the thread sweep is target-conditional, so honest prose about either has
	// to name its condition and is welcome here. "cell comment thread" on its
	// own is NOT banned — the true disclaimer contains that noun, and `extra`
	// is exactly where this branch puts what a --yes script must see, so a
	// noun-shaped ban would fire on a correct sentence moved into it.
	//
	// Both strings below are round 0's, verbatim from the false autoCloseNote
	// ("Releasing also closes any open data reviews and cell comment threads on
	// the rows it matches."), and a revert to it goes red on both.
	for _, claim := range []string{"any open data review", "and cell comment threads"} {
		if strings.Contains(stderr, claim) {
			t.Errorf("stderr claims %q unconditionally, which a release does not do: %q", claim, stderr)
		}
	}
}

// The follow-up line must read back the rows this run acted on — the
// QUARANTINE population at table scope, and ONE cloud source's quarantine at
// cloud-source scope. A hand-built "qctl get data <table> --is-bad-data" passes
// the first half and silently drops the narrowing in the second.
func TestHoldRelease_FollowUpLineNamesTheQuarantinePopulation(t *testing.T) {
	t.Run("table scope", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
			`{"result":"ok","affected_rows":1}`)

		stdout, _, err := runRows(t, "rows", "hold", "orders", "--row-id", "7", "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stdout, "  qctl get data orders --is-bad-data\n") {
			t.Errorf("stdout = %q, want the --is-bad-data follow-up line", stdout)
		}
	})

	t.Run("cloud-source scope", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		registerCloudSourceResolve(mock)
		record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/update-keep-in-quarantine",
			`{"result":"ok","affected_rows":88}`)

		stdout, _, err := runRows(t, "rows", "release", "orders",
			"--cloud-source-id", "7", "--all", "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stdout, "  qctl get data orders --data-source-model-id 7 --is-bad-data\n") {
			t.Errorf("stdout = %q, want a follow-up narrowed to the cloud source", stdout)
		}
	})
}

// hold, release and reset-hold do NOT skip queued rows: their filter builder
// _get_params_filters_and_where_clause_based_on_ids (atlas quarantine.py)
// has no awaiting_processing_ clause — and takes no quarantine_hold argument at
// all, so that is true of all three values — leaving
// support.statusSkipsQueuedRows false and every sizeable status count intact.
//
// This asserts that from the USER's side rather than the field's: the count
// request is spent and the prompt states the count. The field itself is
// pinned by TestOnlyTheReprocessPathSkipsQueuedRows.
func TestHold_UsableStatusesAtTableScopeAreSized(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	for _, tc := range []struct {
		status string
		count  int
	}{
		{status: "on_hold", count: 6},
		{status: "approved", count: 3},
		{status: "held_for_approval", count: 5},
		{status: "passed_validation", count: 17},
	} {
		t.Run(tc.status, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			// The backend's full shape: every key is always present, and the
			// by-reason values sum to held_for_approval
			// (get_quarantine_count_by_status_core, data_view_core.py). Every
			// bucket carries a distinct number, so a status wired to a
			// sibling's bucket states the wrong one.
			counts := record(mock, "GET", "/api/orgs/"+testOrgID+"/datasets/42/quarantine/count-by-status",
				`{"on_hold":6,"approved":3,"held_for_approval":5,`+
					`"held_for_approval_by_reason":{"possible_duplicate":2,"review_policy":3,"table_setting":0},`+
					`"passed_validation":17,"failed_validation":4}`)
			// No update route: the run must stop at the confirmation.
			_, stderr, err := runRows(t, "rows", "hold", "orders", "--status", tc.status)
			if err == nil {
				t.Fatal("expected the non-interactive refusal, not a silent hold")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Fatalf("GetExitCode = %d, want 2 — the run did not stop at the confirmation: %v", code, err)
			}
			if counts.hits != 1 {
				t.Fatalf("count-by-status hits = %d, want 1 — this verb does not skip queued rows, so the count is honest", counts.hits)
			}
			// sizedStatusPhrase, interpolated into the non-interactive refusal
			// right after the verb, so a hedge in front of the number fails it.
			want := fmt.Sprintf("to hold %d row(s) with status %s", tc.count, tc.status)
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the prompt does not state the count %q: %q", want, err.Error())
			}
			if strings.Contains(stderr, "No count is shown") {
				t.Errorf("a count WAS available, so no missing-count reason belongs on stderr: %q", stderr)
			}
		})
	}
}

// The route switch's default must be REACHED, not merely present. It is
// unreachable from any command line — holdReleaseSupport lists no file scope,
// so addScopeFlags registers no --file-id — which is exactly why a test that
// only read the source would prove nothing.
//
// One support{} edit away, the alternative shape is NOT a whole-table mutation
// -- update_keep_in_quarantine_core opens with a 422 on "no ids, no status, no
// data-source limit" (atlas data_view_core.py), so the fall-through
// would be refused at the server. It is a round trip and a confusing server
// error in place of this clear one; see unroutableSelector for why the default
// stays regardless.
func TestUpdateKeepInQuarantine_RefusesAnUnroutableScope(t *testing.T) {
	ctx := &cmdutil.CommandContext{
		// Deliberately unreachable: the default must fire before any request.
		ServerURL:  "http://127.0.0.1:1",
		Credential: &auth.Credential{AccessToken: "t"},
	}
	sc := scope{kind: scopeFile, tableID: 42, tableName: "orders", fileID: 3}
	sel := selector{kind: selRowIDs, rowIDs: []int{7}}

	res, err := updateKeepInQuarantine(ctx, sc, sel, api.QuarantineHoldHold, "hold")
	if err == nil {
		t.Fatalf("file scope was routed instead of refused; got %+v", res)
	}
	// An internal error, not bad usage: exit 1, and the message names the verb,
	// the selector and the scope so a bug report can start from it.
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("GetExitCode = %d, want 1", code)
	}
	if !strings.Contains(err.Error(), "internal error: 'qctl rows hold' has no route for selector \"--row-id\" at file scope") {
		t.Errorf("error = %v, want unroutableSelector's message", err)
	}
}

// At cloud-source scope the count is sent the source's data_source_model_id,
// the source update_keep_in_quarantine_core limits the write to, so the number
// describes that source's rows and not the table's.
func TestHold_UsableStatusAtCloudSourceScopeIsSizedForTheSource(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	mock := newRowsTestEnv(t)
	registerCloudSourceResolve(mock)
	counts := record(mock, "GET", "/api/orgs/"+testOrgID+"/datasets/42/quarantine/count-by-status",
		`{"on_hold":6,"approved":3,"held_for_approval":5,"passed_validation":17,"failed_validation":4}`)
	_, stderr, err := runRows(t, "rows", "hold", "orders", "--cloud-source-id", "7", "--status", "approved")
	if code := qerrors.GetExitCode(err); err == nil || code != 2 {
		t.Fatalf("err = %v (exit %d), want the non-interactive refusal at exit 2", err, code)
	}
	if counts.hits != 1 || counts.query != "data_source_model_id=7" {
		t.Fatalf("count-by-status hits = %d, queries = %q; want one request narrowed to source 7", counts.hits, counts.queries)
	}
	if want := "to hold 3 row(s) with status approved in cloud source s3-nightly (id: 7)"; !strings.Contains(err.Error(), want) {
		t.Errorf("the prompt does not state the source's count %q: %q", want, err.Error())
	}
	if strings.Contains(stderr, "No count is shown") {
		t.Errorf("a count WAS available, so no missing-count reason belongs on stderr: %q", stderr)
	}
}

// hold is the SINGLE declaration of the direction, and it must reach the wire
// unchanged at BOTH scopes — a runner that derived it from act.verb would pass
// the dataset tests above and still be one rename from sending a different
// member.
func TestRelease_SendsReleaseAtCloudSourceScope(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerCloudSourceResolve(mock)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/update-keep-in-quarantine",
		`{"result":"ok","affected_rows":4}`)

	if _, _, err := runRows(t, "rows", "release", "orders",
		"--cloud-source-id", "7", "--status", "held_for_approval", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 {
		t.Fatalf("hits = %d, want 1", got.hits)
	}
	if got.body != `{"ids":null,"quarantine_hold":"release","quarantine_status":"held_for_approval"}` {
		t.Errorf("body = %q", got.body)
	}
}

// Help text that points a user at a sibling verb by NAME is a line they will
// type, so it has to name a verb that exists. `hold` and `release` both send
// the reader to `qctl rows reset-hold`, and these are Long text, which no other
// test reads.
//
// The count guard is the harness-alive control: without it a regex that
// matched nothing would pass while proving nothing, which is the failure mode
// of every "no bad occurrences" test.
func TestRowsHelpCrossReferencesNameLiveVerbs(t *testing.T) {
	root := NewCommand()
	live := map[string]bool{}
	for _, sub := range root.Commands() {
		live[sub.Name()] = true
	}
	refs := 0
	for _, sub := range root.Commands() {
		for _, text := range []string{sub.Short, sub.Long} {
			for _, m := range rowsVerbReference.FindAllStringSubmatch(text, -1) {
				refs++
				if m[1] == sub.Name() {
					continue // the verb's own examples
				}
				if !live[m[1]] {
					t.Errorf("'qctl rows %s' help names 'qctl rows %s', which no verb in the group registers",
						sub.Name(), m[1])
				}
			}
		}
	}
	if refs < 5 {
		t.Fatalf("found %d 'qctl rows <verb>' references in the group's help, want at least 5 — "+
			"the pattern stopped matching and this test is proving nothing", refs)
	}
}

// rowsVerbReference is deliberately narrow: a lowercase hyphenated word only,
// so `qctl rows <verb>` placeholders and `qctl rows …` ellipses in prose are
// not read as verb names.
var rowsVerbReference = regexp.MustCompile(`qctl rows ([a-z][a-z-]*)`)

// reset-hold is the third value, and the assertion is the WORD: "undecided" and
// "release" both leave the row unheld and both queue it for reprocessing, so a
// verb wired to the wrong one of the two still passes every hits-and-exit-code
// check while changing whether the approval path runs and whether the rows'
// reviews close.
func TestResetHold_SendsUndecidedWithIDs(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
		`{"result":"ok","affected_rows":1}`)

	if _, _, err := runRows(t, "rows", "reset-hold", "orders", "--row-id", "7", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 {
		t.Fatalf("update-keep-in-quarantine hits = %d, want 1", got.hits)
	}
	if got.body != `{"ids":[7],"quarantine_hold":"undecided"}` {
		t.Errorf("body = %q", got.body)
	}
	if got.query != "" {
		t.Errorf("query = %q, want empty — no status was given", got.query)
	}
}

// The value must reach the wire unchanged at the OTHER scope too, and through
// the --all selector holdReleaseSupport shares with hold and release: the
// cloud-source arm is a different client, and a runner that hard-coded a value
// in one of the two arms would pass the dataset test above.
func TestResetHold_SendsUndecidedAtCloudSourceScope(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerCloudSourceResolve(mock)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/update-keep-in-quarantine",
		`{"result":"ok","affected_rows":12}`)
	table := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
		`{"result":"ok","affected_rows":12}`)

	if _, _, err := runRows(t, "rows", "reset-hold", "orders",
		"--cloud-source-id", "7", "--all", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 || table.hits != 0 {
		t.Fatalf("hits: data-source = %d (want 1), dataset = %d (want 0)", got.hits, table.hits)
	}
	if got.body != `{"ids":null,"quarantine_hold":"undecided"}` {
		t.Errorf("body = %q — --all sends no ids and no status", got.body)
	}
}

// What a reset does NOT do is the reason it is a verb of its own, so the prompt
// says it, and says it as an `extra` so a --yes script sees it too.
//
// The negatives are the load-bearing half. The approval path and the review
// auto-close are both keyed on QuarantineHold.release (the two
// `body.quarantine_hold is QuarantineHold.release` checks in
// update_keep_in_quarantine_core, data_view_core.py) — so wiring reset-hold
// with release's `extra` slice would ship promises that never come true, and
// every positive assertion above would still pass.
func TestResetHold_PromptNamesWhatItDoesNotDo(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	newRowsTestEnv(t)
	// No route registered: the run must stop at the confirmation, before any
	// row is touched.
	_, stderr, err := runRows(t, "rows", "reset-hold", "orders", "--row-id", "7")
	if err == nil {
		t.Fatal("expected the non-interactive confirmation refusal")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("GetExitCode = %d, want 2 — the run did not stop at the confirmation: %v", code, err)
	}
	if !strings.Contains(stderr, "records no approval") {
		t.Errorf("stderr does not disclose that a reset approves nothing: %q", stderr)
	}
	if !strings.Contains(stderr, "closes no data review") {
		t.Errorf("stderr does not disclose that the reviews stay open: %q", stderr)
	}
	if !strings.Contains(stderr, "re-validated") {
		t.Errorf("stderr does not disclose that a reset queues the rows for reprocessing: %q", stderr)
	}
	// A reset is NOT a release, and the difference survives the reprocess: an
	// undecided row is held anyway when it is a suspected duplicate, or the
	// table quarantines rows until approved or its data-review policy requires
	// review (atlas Cleaner._should_hold_valid_row_in_quarantine,
	// cmapper/cmapper/cleaner.py), so promising the release's unconditional
	// outcome here would be false.
	if !strings.Contains(stderr, "not held for approval") {
		t.Errorf("stderr promises the release's outcome without the hold for approval: %q", stderr)
	}
	for _, released := range []string{releaseApprovalNote, autoCloseNote, reprocessNote} {
		if strings.Contains(stderr, released) {
			t.Errorf("stderr carries a RELEASE-only note (%q), which a reset does not do: %q", released, stderr)
		}
	}
}

// All three verbs act on the QUARANTINE table, so the status a user may name is
// the full enum and a typo fails client-side rather than costing a 422.
func TestHold_InvalidStatusIsBadUsage(t *testing.T) {
	newRowsTestEnv(t)
	_, _, err := runRows(t, "rows", "hold", "orders", "--status", "quarantined", "--yes")
	if err == nil {
		t.Fatal("expected an invalid --status to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), `invalid --status "quarantined"`) {
		t.Errorf("error = %v, want the client-side status refusal", err)
	}
	// Not a placeholder and not cobra: the message names the flag it rejected.
	if strings.Contains(err.Error(), "not implemented") {
		t.Errorf("the verb is still a stub: %v", err)
	}
}

// The status pointer is the "no status filter" form when nil, so a by-id run
// must not send one and a by-status run must. Asserted on the selector->request
// boundary rather than only through the wire, because nil and the zero
// QuarantineStatus look identical in a query string that omits the parameter.
func TestHoldRelease_StatusPointerIsNilForTheListSelectors(t *testing.T) {
	for _, tc := range []struct {
		name string
		sel  selector
		want *api.QuarantineStatus
	}{
		{"--row-id", selector{kind: selRowIDs, rowIDs: []int{7}}, nil},
		{"--all", selector{kind: selAll}, nil},
		{"--status", selector{kind: selStatus, status: api.QuarantineStatusFailedValidation}, ptrTo(api.QuarantineStatusFailedValidation)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := statusFilter(tc.sel)
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("statusFilter = %q, want nil", *got)
			case tc.want != nil && got == nil:
				t.Errorf("statusFilter = nil, want %q", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Errorf("statusFilter = %q, want %q", *got, *tc.want)
			}
		})
	}
}

func ptrTo[T any](v T) *T { return &v }
