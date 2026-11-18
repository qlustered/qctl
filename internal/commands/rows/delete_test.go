package rows

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// testReason is the --reason every delete test sends, and the bodies the delete
// routes must receive for it.
const (
	testReason        = "duplicate upload"
	testSignatureBody = `{"comment":"` + testReason + `","signatures":["deadbeef"]}`
	testStatusBody    = `{"comment":"` + testReason + `","signatures":[]}`
	testCommentBody   = `{"comment":"` + testReason + `"}`
)

func TestDeleteQuarantined_CompositeRefusalKeepsHintAcrossScopes(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, tc := range []struct {
			name, route string
			flags       []string
		}{
			{"table signatures", "/datasets/42/delete-rows", []string{"--signature", "deadbeef"}},
			{"table status", "/datasets/42/delete-rows", []string{"--status", "failed_validation"}},
			{"table all", "/datasets/42/delete-all-bad-rows", []string{"--all"}},
			{"cloud signatures", "/data-sources/7/delete-rows", []string{"--cloud-source-id", "7", "--signature", "deadbeef"}},
			{"cloud all", "/data-sources/7/delete-all-bad-rows", []string{"--cloud-source-id", "7", "--all"}},
			{"file signatures", "/stored-items/3/delete-rows", []string{"--file-id", "3", "--signature", "deadbeef"}},
		} {
			t.Run(fmt.Sprintf("%s/shared=%t", tc.name, shared), func(t *testing.T) {
				mock := newRowsTestEnv(t)
				registerCloudSourceResolve(mock)
				prefix := "/api/orgs/" + testOrgID
				args := []string{"rows", "delete-quarantined", "42", "--reason", testReason, "--yes"}
				if shared {
					prefix += "/third-party"
					args = append(args, "--shared")
				}
				record(mock, "GET", prefix+"/stored-items/3",
					`{"id":3,"dataset_id":42,"file_name":"a.csv","data_source_model_id":7,"created_at":"2026-11-01T00:00:00Z"}`)
				mock.RegisterHandler("POST", prefix+tc.route, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusUnprocessableEntity, json.RawMessage(
						`{"detail":{"msg":"Rows of a composite table cannot be deleted.","error_code":"COMPOSITE_ROW_DELETE_REFUSED","error_id":"req-composite"}}`))
				})
				stdout, _, err := runRows(t, append(args, tc.flags...)...)
				if qerrors.GetExitCode(err) != 2 {
					t.Fatalf("error = %v, want exit 2", err)
				}
				for _, want := range []string{"Rows of a composite table cannot be deleted.", "COMPOSITE_ROW_DELETE_REFUSED", "req-composite",
					"Hint:", "qctl get linked-table <table> -o yaml", "--row-id <upstream-row-id>", "--signature <upstream-signature> --reason <reason>"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %v, want %q", err, want)
					}
				}
				if stdout != "" {
					t.Errorf("stdout = %q, want no success output", stdout)
				}
			})
		}
	}
}

func TestDeleteQuarantined_ByStatusSendsIsBadDataTrueAndEmptyBody(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows",
		`{"result":"ok","affected_rows":75}`)

	stdout, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--status", "failed_validation", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.query != "is_bad_data=true&quarantine_status=failed_validation" {
		t.Errorf("query = %q", got.query)
	}
	if got.body != testStatusBody {
		t.Errorf("body = %q, want %s", got.body, testStatusBody)
	}
	if !strings.Contains(stdout, "75 row(s) affected") {
		t.Errorf("stdout = %q, want the affected-row count", stdout)
	}
}

func TestDeleteQuarantined_AllUsesTheAllBadRowsRoute(t *testing.T) {
	mock := newRowsTestEnv(t)
	scoped := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows", `{"result":"ok"}`)
	all := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-all-bad-rows",
		`{"result":"ok","affected_rows":120}`)

	if _, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--all", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if all.hits != 1 {
		t.Errorf("delete-all-bad-rows hits = %d, want 1", all.hits)
	}
	if scoped.hits != 0 {
		t.Errorf("delete-rows hits = %d, want 0 — --all must not use the scoped route", scoped.hits)
	}
}

func TestDeleteQuarantined_FollowUpLineNamesTheQuarantinePopulation(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows",
		`{"result":"ok","affected_rows":1}`)

	stdout, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--signature", "deadbeef", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "  qctl get data orders --is-bad-data\n") {
		t.Errorf("stdout = %q, want the --is-bad-data follow-up line", stdout)
	}
}

// The file scope has exactly one route and no --all.
func TestDeleteQuarantined_FileScopeUsesStoredItemRoute(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "GET", "/api/orgs/"+testOrgID+"/stored-items/3", `{"id":3,"dataset_id":42}`)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/stored-items/3/delete-rows", `{"result":"ok"}`)

	stdout, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--file-id", "3", "--signature", "deadbeef", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 {
		t.Errorf("stored-items delete-rows hits = %d, want 1", got.hits)
	}
	// The population is the MUST-hold routing fact at this scope too: the route
	// defaults is_bad_data to false, so a quarantine delete that omitted it
	// would silently delete clean rows of the file.
	if got.query != "dataset_id=42&is_bad_data=true" {
		t.Errorf("query = %q, want dataset_id=42&is_bad_data=true", got.query)
	}
	if got.body != testSignatureBody {
		t.Errorf("body = %q, want %s", got.body, testSignatureBody)
	}
	// The follow-up must read back the rows the delete acted on, not the whole
	// table: `get data` narrows to one file with --stored-item-id.
	if !strings.Contains(stdout, "  qctl get data orders --stored-item-id 3 --is-bad-data\n") {
		t.Errorf("stdout = %q, want a follow-up narrowed to the file", stdout)
	}
	// This response carries no affected_rows; inventing a count is the failure mode.
	if strings.Contains(stdout, "row(s) affected") {
		t.Errorf("stdout claims an affected-row count the API never sent: %q", stdout)
	}
	if !strings.Contains(stdout, "requested") {
		t.Errorf("stdout = %q, want the 'requested' wording", stdout)
	}
}

// Cloud-source scope has both routes: the signature form and --all must not be
// collapsed.
func TestDeleteQuarantined_CloudSourceScopeUsesDataSourceRoute(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerCloudSourceResolve(mock)
	got := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/delete-rows", `{"result":"ok"}`)

	stdout, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--cloud-source-id", "7", "--signature", "deadbeef", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.hits != 1 {
		t.Errorf("data-sources delete-rows hits = %d, want 1", got.hits)
	}
	if got.query != "is_bad_data=true" {
		t.Errorf("query = %q, want is_bad_data=true", got.query)
	}
	if !strings.Contains(stdout, "  qctl get data orders --data-source-model-id 7 --is-bad-data\n") {
		t.Errorf("stdout = %q, want a follow-up narrowed to the cloud source", stdout)
	}
	if strings.Contains(stdout, "row(s) affected") {
		t.Errorf("stdout claims an affected-row count the API never sent: %q", stdout)
	}
}

func TestDeleteQuarantined_CloudSourceAllUsesTheAllBadRowsRoute(t *testing.T) {
	mock := newRowsTestEnv(t)
	registerCloudSourceResolve(mock)
	scoped := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/delete-rows", `{"result":"ok"}`)
	all := record(mock, "POST", "/api/orgs/"+testOrgID+"/data-sources/7/delete-all-bad-rows",
		`{"result":"ok","affected_rows":9}`)

	stdout, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--cloud-source-id", "7", "--all", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if all.hits != 1 || scoped.hits != 0 {
		t.Errorf("hits: delete-all-bad-rows = %d (want 1), delete-rows = %d (want 0)", all.hits, scoped.hits)
	}
	if !strings.Contains(stdout, "9 row(s) affected") {
		t.Errorf("stdout = %q, want the affected-row count this route does return", stdout)
	}
}

func TestDeleteQuarantined_AllAtFileScopeIsBadUsage(t *testing.T) {
	newRowsTestEnv(t)
	_, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--file-id", "3", "--all", "--yes")
	if err == nil {
		t.Fatal("expected --all to be refused at file scope")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	// Exit 2 alone cannot tell this refusal from any other bad usage, and the
	// user's next move is in the wording: name the scopes where --all works.
	if !strings.Contains(err.Error(), "at file scope") {
		t.Errorf("error does not name the offending scope: %v", err)
	}
}

func TestDeleteQuarantined_StatusAtCloudSourceScopeIsBadUsage(t *testing.T) {
	mock := newRowsTestEnv(t)
	// The scope itself must RESOLVE, so the refusal that follows is provably
	// the selector's and not a 404 on the cloud-source lookup.
	registerCloudSourceResolve(mock)
	_, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
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

// --row-id is registered on delete-quarantined so the refusal can teach the
// signature recipe; cobra's own "unknown flag" cannot.
func TestDeleteQuarantined_RowIDIsRefusedWithTheSignatureRecipe(t *testing.T) {
	newRowsTestEnv(t)
	_, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--row-id", "1042", "--yes")
	if err == nil {
		t.Fatal("expected --row-id to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "qctl get data orders --is-bad-data -o json") {
		t.Errorf("error does not point at the signature column: %v", err)
	}
}

// The prompt must say what the delete keeps from loading again BEFORE the
// user answers: for --status, nothing.
func TestDeleteQuarantined_PromptSaysAStatusDeleteExcludesNothing(t *testing.T) {
	newRowsTestEnv(t)
	// No --yes and no terminal: Confirm reports bad usage after printing the
	// consequence lines, so stderr carries both.
	_, stderr, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--status", "failed_validation")
	if err == nil {
		t.Fatal("expected the non-interactive confirmation refusal")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--status excludes nothing") {
		t.Errorf("stderr does not say a status delete excludes nothing: %q", stderr)
	}
	if !strings.Contains(stderr, "NULL") {
		t.Errorf("stderr does not explain the missing count for failed_validation: %q", stderr)
	}
}

// The consequence is what a script never sees a prompt for, so it must print
// under --yes too.
func TestDeleteQuarantined_YesStillNamesTheExclusionList(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows", `{"result":"ok","affected_rows":1}`)

	_, stderr, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--signature", "deadbeef", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "to the table's exclusion list") {
		t.Errorf("stderr does not name the exclusion list under --yes: %q", stderr)
	}
}

// This is one of the sizeable statuses (sizeableStatus), counted only at table scope.
// The prompt spends exactly one extra request to get it.
func TestDeleteQuarantined_YesSkipsTheCountRequest(t *testing.T) {
	mock := newRowsTestEnv(t)
	counts := record(mock, "GET", "/api/orgs/"+testOrgID+"/datasets/42/quarantine/count-by-status",
		`{"on_hold":6,"approved":4,"held_for_approval":3,"held_for_approval_by_reason":{"possible_duplicate":1,"review_policy":2,"table_setting":0},"passed_validation":12,"failed_validation":75}`)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows", `{"result":"ok","affected_rows":12}`)

	// But --yes skips the prompt, so it must ALSO skip the request that only the
	// prompt needs.
	if _, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--status", "passed_validation", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if counts.hits != 0 {
		t.Errorf("count-by-status hits = %d under --yes, want 0", counts.hits)
	}
}

// A failed delete must not report success, and its exit code must survive the
// wrap chain rather than degrading to 1.
func TestDeleteQuarantined_ServerErrorIsReported(t *testing.T) {
	mock := newRowsTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, json.RawMessage(`{"detail":"dataset not found"}`))
		})

	stdout, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders", "--signature", "deadbeef", "--yes")
	if err == nil {
		t.Fatal("expected the 404 to surface as an error")
	}
	if strings.Contains(stdout, "delete-quarantined:") {
		t.Errorf("stdout reported a result for a failed delete: %q", stdout)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("GetExitCode = %d, want %d", code, qerrors.ExitNotFound)
	}
}

// registerCloudSourceByName answers the exact-name search
// cmdutil.ResolveCloudSource makes for `--cloud-source <name>`, and counts it.
// The count is the point: this is the ONE lookup the group is allowed to spend
// on building an error message.
func registerCloudSourceByName(mock *testutil.MockAPIServer) *int {
	hits := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		hits++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":7,"name":"s3-drop"}]}`))
	})
	return &hits
}

// THE CARVE-OUT. `--cloud-source <name>` is the one refusal whose MESSAGE needs
// a value the flags cannot supply: the recipe narrows with
// --data-source-model-id, and `qctl get data` has no --cloud-source flag, so a
// name will not do.
//
// An UN-NARROWED recipe is not the fallback: initial_signature_ is a content
// hash, so a signature read off the whole table's listing can belong to a row in
// a different source, which the cloud-source delete skips while still
// answering ok. So qctl pays one lookup here, deliberately.
func TestDeleteQuarantined_RowIDRefusalNarrowsANamedCloudSource(t *testing.T) {
	mock := newRowsTestEnv(t)
	lookups := registerCloudSourceByName(mock)

	_, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--cloud-source", "s3-drop", "--row-id", "1042", "--yes")
	if err == nil {
		t.Fatal("expected --row-id to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "--data-source-model-id 7") {
		t.Errorf("recipe is not narrowed to the named cloud source: %v", err)
	}
	if *lookups == 0 {
		t.Error("the cloud source was never resolved, so the id in the recipe came from nowhere")
	}
}

// The other half, and the one that protects the exit-code contract: when that
// lookup FAILS, the refusal is still the user's usage error at exit 2. A network
// failure while BUILDING an error message must not replace the message or its
// code — that would be the exact defect the reorder removed, reintroduced
// through the message path.
//
// The recipe stays narrowed in FORM, with a placeholder the user must fill in,
// rather than degrading to the whole-table listing.
func TestDeleteQuarantined_RowIDRefusalStaysExitTwoWhenTheLookupFails(t *testing.T) {
	// No data-sources handler at all: the name lookup 404s.
	newRowsTestEnv(t)

	_, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--cloud-source", "s3-drop", "--row-id", "1042", "--yes")
	if err == nil {
		t.Fatal("expected --row-id to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2 — the failed lookup replaced the usage error", code)
	}
	if !strings.Contains(err.Error(), "--row-id is not supported") {
		t.Errorf("the lookup failure replaced the refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "--data-source-model-id <cloud-source-id>") {
		t.Errorf("recipe lost its narrowing when the lookup failed: %v", err)
	}
	if strings.Contains(err.Error(), "--data-source-model-id 0") {
		t.Errorf("recipe names data source 0, a line that runs: %v", err)
	}
}

// A2. qctl told the user "the API offers no way to look a signature up by id"
// and handed them a whole-population dump. The per-id lookup ships: `id` is a
// permitted filter field on the bad-data table as well as the clean one
// (get_field_names_set_for_table seeds the set with StableSetEq(['id']),
// atlas .../data_controller/core.py), and an omitted filter_operator
// reaches the SQL as "=". On a large quarantine that claim was the difference
// between one request and paging through the table.
func TestDeleteVerbs_RowIDRefusalOffersThePerIDLookup(t *testing.T) {
	for _, tc := range []struct {
		verb string
		want string
	}{
		{"delete-quarantined", "qctl get data orders --is-bad-data --filter-by-field-name id --filter-by-field-value <row-id> -o json"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			newRowsTestEnv(t)
			_, _, err := runRows(t, "rows", tc.verb, "orders", "--row-id", "1042", "--yes")
			if err == nil {
				t.Fatal("expected --row-id to be refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal does not carry the per-id lookup\n  want: %s\n  got:  %v", tc.want, err)
			}
			// The false claim itself, in the words it was written in. It is
			// asserted as an ABSENCE because the fix is a narrowing: any
			// re-broadening puts one of these back.
			for _, banned := range []string{
				"offers no way to look a signature up by id",
				"no id-to-signature lookup",
				"no id->signature lookup",
			} {
				if strings.Contains(err.Error(), banned) {
					t.Errorf("refusal repeats the false claim %q: %v", banned, err)
				}
			}
		})
	}
}

// A3. THE LATENT HAZARD, made unreachable.
//
// Both delete-all-bad-rows routes name the QUARANTINE table by construction and
// take no is_bad_data parameter, so --all is the one selector whose population
// comes from the route rather than from the verb. A support row with isBadData
// false and selAll would delete the quarantine table while the prompt named the
// clean one.
//
// deleteRows is called DIRECTLY here, with isBadData false, so the guard is
// proven at the layer such a support row would reach.
func TestDeleteRows_AllIsRefusedForACleanPopulation(t *testing.T) {
	for _, tc := range []struct {
		name string
		sc   scope
		path string
	}{
		{"table scope", scope{kind: scopeTable, tableID: 42},
			"/api/orgs/" + testOrgID + "/datasets/42/delete-all-bad-rows"},
		{"cloud-source scope", scope{kind: scopeCloudSource, tableID: 42, cloudSourceID: 7},
			"/api/orgs/" + testOrgID + "/data-sources/7/delete-all-bad-rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			got := record(mock, "POST", tc.path, `{"result":"ok","affected_rows":9999}`)
			ctx := &cmdutil.CommandContext{
				ServerURL:      mock.Server.URL,
				OrganizationID: testOrgID,
				Credential:     &auth.Credential{AccessToken: "test-token"},
			}

			_, err := deleteRows(ctx, tc.sc, selector{kind: selAll}, false, "delete-quarantined", testReason)
			if err == nil {
				t.Fatal("--all on a clean-population verb reached a delete-all-BAD-rows route")
			}
			if got.hits != 0 {
				t.Errorf("the quarantine table was called %d time(s) by a clean-population verb", got.hits)
			}

			// And the quarantined verb, which the route DOES belong to, is
			// untouched by the guard — otherwise this would "pass" by refusing
			// everything.
			if _, err := deleteRows(ctx, tc.sc, selector{kind: selAll}, true, "delete-quarantined", testReason); err != nil {
				t.Fatalf("delete-quarantined --all was refused too: %v", err)
			}
			if got.hits != 1 {
				t.Errorf("hits = %d, want 1 from delete-quarantined", got.hits)
			}
		})
	}
}

// The POSITIVE half of the preserved delete behaviour: the prompt path DOES
// spend the count request, and DOES render the number it gets.
//
// TestDeleteQuarantined_YesSkipsTheCountRequest above pins the negative — --yes
// spends nothing — and TestOnlyTheReprocessPathSkipsQueuedRows pins, at unit
// level, that delete-quarantined is not the verb whose count is suppressed.
// Neither can see this: a sizedStatusCount that returned nil for every verb, or
// a support{} edit that set statusSkipsQueuedRows on the delete row, would
// leave both of them green while every delete prompt silently lost its number.
//
// Reached through the non-interactive refusal, as
// TestReprocessQuarantined_PassedValidationPromptStatesNoCount is: confirmAction
// INTERPOLATES the subject describeSubject built into that message, so the noun
// phrase the prompt would have carried is assertable from err.Error() — and by
// then the count request either was or was not spent.
//
// No delete route is registered on purpose: the run must stop at the
// confirmation, before a row is touched.
func TestDeleteQuarantined_PromptSpendsTheCountAndStatesIt(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	mock := newRowsTestEnv(t)
	counts := record(mock, "GET", "/api/orgs/"+testOrgID+"/datasets/42/quarantine/count-by-status",
		`{"on_hold":6,"approved":4,"held_for_approval":3,"held_for_approval_by_reason":{"possible_duplicate":1,"review_policy":2,"table_setting":0},"passed_validation":12,"failed_validation":75}`)

	_, stderr, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--status", "passed_validation")
	if err == nil {
		t.Fatal("expected the non-interactive refusal, not a silent delete")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("GetExitCode = %d, want 2 — the run did not stop at the confirmation: %v", code, err)
	}
	// Exactly one, and un-narrowed: the count route takes a dataset id alone,
	// so a query string here would mean qctl believed it could scope it.
	if counts.hits != 1 {
		t.Errorf("count-by-status hits = %d, want 1 — the prompt did not spend the count", counts.hits)
	}
	if len(counts.queries) == 1 && counts.queries[0] != "" {
		t.Errorf("count-by-status query = %q, want none", counts.queries[0])
	}
	// The number, unhedged: delete_bad_rows_by_status filters exactly as the
	// counter does (see sizedStatusPhrase).
	if !strings.Contains(err.Error(), "to delete 12 row(s) with status passed_validation") {
		t.Errorf("the prompt does not state the counted subject: %q", err.Error())
	}
	// And no reason clause: a prompt carries a number or an explanation, never
	// both, and never neither.
	if strings.Contains(stderr, "No count is shown") {
		t.Errorf("a sized prompt also explained a missing count: %q", stderr)
	}
}

// Each of the ten delete routes receives the reason as comment, in the body its
// request schema declares. The status form shares the dataset delete-rows
// route, so it is listed too.
func TestDeleteRoutesSendTheReasonAsComment(t *testing.T) {
	const org = "/api/orgs/" + testOrgID
	for _, tc := range []struct {
		name, path, body string
		args             []string
		setup            func(*testutil.MockAPIServer)
	}{
		{"dataset signatures", org + "/datasets/42/delete-rows", testSignatureBody,
			[]string{"delete-quarantined", "orders", "--signature", "deadbeef"}, nil},
		{"dataset status", org + "/datasets/42/delete-rows", testStatusBody,
			[]string{"delete-quarantined", "orders", "--status", "failed_validation"}, nil},
		{"dataset all", org + "/datasets/42/delete-all-bad-rows", testCommentBody,
			[]string{"delete-quarantined", "orders", "--all"}, nil},
		{"cloud source signatures", org + "/data-sources/7/delete-rows", testSignatureBody,
			[]string{"delete-quarantined", "orders", "--cloud-source-id", "7", "--signature", "deadbeef"}, registerCloudSourceResolve},
		{"cloud source all", org + "/data-sources/7/delete-all-bad-rows", testCommentBody,
			[]string{"delete-quarantined", "orders", "--cloud-source-id", "7", "--all"}, registerCloudSourceResolve},
		{"file signatures", org + "/stored-items/3/delete-rows", testSignatureBody,
			[]string{"delete-quarantined", "orders", "--file-id", "3", "--signature", "deadbeef"},
			func(mock *testutil.MockAPIServer) {
				record(mock, "GET", org+"/stored-items/3", `{"id":3,"dataset_id":42}`)
			}},
		{"shared dataset signatures", org + "/third-party/datasets/42/delete-rows", testSignatureBody,
			[]string{"delete-quarantined", "42", "--shared", "--signature", "deadbeef"}, nil},
		{"shared dataset status", org + "/third-party/datasets/42/delete-rows", testStatusBody,
			[]string{"delete-quarantined", "42", "--shared", "--status", "failed_validation"}, nil},
		{"shared dataset all", org + "/third-party/datasets/42/delete-all-bad-rows", testCommentBody,
			[]string{"delete-quarantined", "42", "--shared", "--all"}, nil},
		{"shared cloud source signatures", org + "/third-party/data-sources/7/delete-rows", testSignatureBody,
			[]string{"delete-quarantined", "42", "--shared", "--cloud-source-id", "7", "--signature", "deadbeef"}, nil},
		{"shared cloud source all", org + "/third-party/data-sources/7/delete-all-bad-rows", testCommentBody,
			[]string{"delete-quarantined", "42", "--shared", "--cloud-source-id", "7", "--all"}, nil},
		{"shared file signatures", sharedFileDeletePath, testSignatureBody,
			[]string{"delete-quarantined", "42", "--shared", "--file-id", "3", "--signature", "deadbeef"},
			func(mock *testutil.MockAPIServer) {
				record(mock, "GET", sharedFileReadPath, `{"id":3,"dataset_id":42,"file_name":"a.csv","data_source_model_id":7,"created_at":"2026-11-01T00:00:00Z"}`)
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			if tc.setup != nil {
				tc.setup(mock)
			}
			got := record(mock, "POST", tc.path, `{"result":"ok","affected_rows":1}`)
			args := append([]string{"rows"}, tc.args...)
			_, stderr, err := runRows(t, append(args, "--reason", testReason, "--yes")...)
			if err != nil {
				t.Fatalf("unexpected error: %v\n%s", err, stderr)
			}
			if got.hits != 1 {
				t.Fatalf("hits = %d, want 1", got.hits)
			}
			if got.body != tc.body {
				t.Errorf("body = %s, want %s", got.body, tc.body)
			}
		})
	}
}

// A delete without a usable reason is refused with exit 2 before any request,
// at every scope and selector.
func TestDeleteReasonRefusedBeforeAnyRequest(t *testing.T) {
	selectors := [][]string{
		{"delete-quarantined", "orders", "--signature", "deadbeef"},
		{"delete-quarantined", "orders", "--status", "failed_validation"},
		{"delete-quarantined", "orders", "--all"},
		{"delete-quarantined", "orders", "--cloud-source-id", "7", "--all"},
		{"delete-quarantined", "orders", "--cloud-source-id", "7", "--signature", "deadbeef"},
		{"delete-quarantined", "orders", "--file-id", "3", "--signature", "deadbeef"},
	}
	reasons := []struct {
		name string
		flag []string
	}{
		{"missing", nil},
		{"empty", []string{"--reason", ""}},
		{"blank", []string{"--reason", " \t\n "}},
		{"too long", []string{"--reason", strings.Repeat("x", maxDeleteReasonLength+1)}},
	}
	for _, sel := range selectors {
		for _, r := range reasons {
			t.Run(strings.Join(sel, " ")+"/"+r.name, func(t *testing.T) {
				mock := newRowsTestEnv(t)
				args := append([]string{"rows"}, sel...)
				args = append(args, r.flag...)
				_, _, err := runRows(t, append(args, "--yes")...)
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Errorf("GetExitCode = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
				}
				if reqs := mock.Requests(); len(reqs) != 0 {
					t.Errorf("requests = %v, want none", reqs)
				}
			})
		}
	}
}

// The limit is in characters, as the backend counts them, not bytes.
func TestDeleteReasonAtTheLimitIsSent(t *testing.T) {
	for _, reason := range []string{
		strings.Repeat("x", maxDeleteReasonLength),
		strings.Repeat("é", maxDeleteReasonLength),
	} {
		mock := newRowsTestEnv(t)
		got := record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-all-bad-rows", `{"result":"ok","affected_rows":1}`)
		_, _, err := runRows(t, "rows", "delete-quarantined", "orders", "--all", "--reason", reason, "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var body struct {
			Comment string `json:"comment"`
		}
		if err := json.Unmarshal([]byte(got.body), &body); err != nil {
			t.Fatal(err)
		}
		if body.Comment != reason {
			t.Errorf("comment has %d characters, want the %d sent", len([]rune(body.Comment)), maxDeleteReasonLength)
		}
	}
}

// The removed verb answers with the way to delete a clean row, at exit 2 when
// run (with or without its old flags) and on stdout at exit 0 under --help.
func TestDeleteClean_IsRemovedWithARedirect(t *testing.T) {
	for _, args := range [][]string{
		{"rows", "delete-clean"},
		{"rows", "delete-clean", "orders", "--signature", "deadbeef", "--reason", testReason, "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := newRowsTestEnv(t)
			_, _, err := runRows(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("GetExitCode = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
			}
			for _, want := range []string{"qctl rows send-to-quarantine", "qctl rows delete-quarantined"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not name %q: %v", want, err)
				}
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("requests = %v, want none", reqs)
			}
		})
	}
	t.Run("--help", func(t *testing.T) {
		newRowsTestEnv(t)
		stdout, _, err := runRows(t, "rows", "delete-clean", "--help")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stdout, "qctl rows send-to-quarantine") {
			t.Errorf("stdout = %q, want the redirect", stdout)
		}
	})
}
