package get

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	changeProposalsURL = "/api/orgs/" + testOrgID + "/change-proposals"
	cpAlertID          = "0190cccc-0000-4000-8000-000000000001"
	cpWarningID        = "0190cccc-0000-4000-8000-000000000002"
	cpAuthorID         = "0190dddd-0000-4000-8000-000000000001"
)

// changeProposalItem is one wire proposal. The alert one sits on table 7 and
// the caller may decide it; the warning one has no table and no actions.
func changeProposalItem(id string) string {
	created := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	target, dataset, actions := `"alert_item_id":42,"warning_item_id":null`, `7`, `["approve","reject","request_changes"]`
	op, state := "alert_resolve", "pending_review"
	if id == cpWarningID {
		target, dataset, actions = `"alert_item_id":null,"warning_item_id":9`, `null`, `[]`
		op, state = "warning_resolve", "draft"
	}
	return `{"id":"` + id + `","organization_id":"` + testOrgID + `","operation":"` + op + `",` +
		`"state":"` + state + `",` + target + `,"dataset_id":` + dataset + `,` +
		`"data_source_model_id":3,"dataset_rule_id":null,"payload":{"action":"add_value"},` +
		`"addressed_alert_item_ids":[],"addressed_warning_item_ids":[],"allowed_actions":` + actions + `,` +
		`"author_ids":["` + cpAuthorID + `"],"created_by_id":"` + cpAuthorID + `",` +
		`"created_at":"` + created + `","updated_at":"` + created + `","decision_reason":null,"depends_on_id":null,` +
		`"approved_by_id":null,"approver_role":null,"approved_at":null,"execution_started_at":null,` +
		`"executed_at":null,"execution_result":null,"policy_keys":[["alert_resolve","add_value"]],` +
		`"policy_snapshot":null,"version_id":2,"base_params":null,"dry_run_job_id":null,` +
		`"created_by":{"id":"` + cpAuthorID + `","email":"pat@corp.com","first_name":"Pat","last_name":"Proposer"},` +
		`"author_users":[{"id":"` + cpAuthorID + `","email":"pat@corp.com","first_name":"Pat","last_name":"Proposer"}],` +
		`"authors":[{"user_id":"` + cpAuthorID + `","delegations":[]}],"approved_by":null,` +
		`"decided_at":null,"decided_by":null,"decided_by_id":null,"note":null,"obsolete_reasons":[],` +
		`"validity_summary":{"validity":"current","reasons":[],"blockers":[]}}`
}

func changeProposalBody(ids ...string) []byte {
	items := make([]string, len(ids))
	for i, id := range ids {
		items[i] = changeProposalItem(id)
	}
	return []byte(`{"items":[` + strings.Join(items, ",") + `]}`)
}

// serveChangeProposals answers the list and the users directory (directory
// status dirStatus) and returns the list query of the last request.
func serveChangeProposals(e *dataReviewGetEnv, body []byte, dirStatus int) *url.Values {
	query := servePrivilege(e, changeProposalsURL, http.StatusOK, body)
	if dirStatus != http.StatusOK {
		servePrivilege(e, privilegeUsersURL, dirStatus, []byte(`{"detail":{"msg":"directory unavailable"}}`))
	} else {
		servePrivilege(e, privilegeUsersURL, dirStatus, []byte(`{"results":[`+
			`{"id":"`+cpAuthorID+`","email":"pat@corp.com","first_name":"Pat","last_name":"Proposer"}`+
			`],"next":null}`))
	}
	return query
}

func runChangeProposalsCmd(t *testing.T, e *dataReviewGetEnv, args ...string) (string, string, error) {
	t.Helper()
	return e.run(t, NewChangeProposalsCommand(), "change-proposals", args...)
}

func TestGetChangeProposalsDefaultsToLiveStates(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := serveChangeProposals(e, changeProposalBody(), http.StatusOK)

	_, stderr, err := runChangeProposalsCmd(t, e)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := url.Values{
		"state": {"draft", "pending_review", "changes_requested", "executing"},
		"limit": {"50"},
	}
	if !reflect.DeepEqual(*query, want) {
		t.Errorf("query = %v, want %v", *query, want)
	}
	if !strings.Contains(stderr, "Only live states") {
		t.Errorf("an empty default listing should say the default narrowed it\n%s", stderr)
	}
}

func TestGetChangeProposalsStateReplacesTheDefault(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := serveChangeProposals(e, changeProposalBody(), http.StatusOK)

	_, stderr, err := runChangeProposalsCmd(t, e, "--state", "Executed", "--state", "execution_failed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := (*query)["state"], []string{"executed", "execution_failed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("state = %q, want %q", got, want)
	}
	if strings.Contains(stderr, "Only live states") {
		t.Errorf("an explicit --state is not the default\n%s", stderr)
	}
}

func TestGetChangeProposalsFiltersReachTheQuery(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := serveChangeProposals(e, changeProposalBody(), http.StatusOK)

	if _, _, err := runChangeProposalsCmd(t, e, "--reviewable", "--mine", "--table-id", "7",
		"--operation", "WARNING_RESOLVE", "--page", "3", "--limit", "20"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// --reviewable pins pending_review server-side, so no default states go.
	want := url.Values{
		"reviewable_by_me": {"true"},
		"authored_by_me":   {"true"},
		"dataset_id":       {"7"},
		"operation":        {"warning_resolve"},
		"limit":            {"20"},
		"offset":           {"40"},
	}
	if !reflect.DeepEqual(*query, want) {
		t.Errorf("query = %v, want %v", *query, want)
	}
}

func TestGetChangeProposalsResolvesATableName(t *testing.T) {
	e := newDataReviewGetEnv(t)
	var lookedUp string
	e.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		lookedUp = r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
			Results: []api.DataSetTinySchema{{ID: 7, Name: "orders"}},
		})
	})
	query := serveChangeProposals(e, changeProposalBody(cpAlertID), http.StatusOK)

	out, _, err := runChangeProposalsCmd(t, e, "--table", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lookedUp != "orders" || query.Get("dataset_id") != "7" {
		t.Errorf("looked up %q, sent dataset_id=%q; want orders and 7", lookedUp, query.Get("dataset_id"))
	}
	if row := strings.Join(rowFields(t, out, cpAlertID), " "); !strings.Contains(row, "orders (7)") {
		t.Errorf("TABLE should name the --table it was filtered by: %s", row)
	}
}

// Every locally decidable mistake exits 2 before config, credentials or any
// request.
func TestGetChangeProposalsRejectsBadFlagsWithoutARequest(t *testing.T) {
	for _, args := range [][]string{
		{"--state", "pending"},
		{"--state", "draft", "--state", "nope"},
		{"--operation", "settings_patch"},
		{"--operation", "resolve"},
		{"--table", "orders", "--table-id", "7"},
		{"--table-id", "0"},
		{"--page", "0"},
		{"--limit", "0"},
		{"--limit", "201"},
		{"--page", "9223372036854775807", "--limit", "200"},
		{"--reviewable", "--state", "executed"},
	} {
		t.Run(strings.Join(args, "="), func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			_, _, err := runChangeProposalsCmd(t, e, args...)
			assertExitCode(t, err, qerrors.ExitBadUsage)
			assertPrivilegeRequests(t, e)
		})
	}
}

func TestGetChangeProposalsReviewableAcceptsAPendingReviewState(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := serveChangeProposals(e, changeProposalBody(), http.StatusOK)

	if _, _, err := runChangeProposalsCmd(t, e, "--reviewable", "--state", "pending_review"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := (*query)["state"]; !reflect.DeepEqual(got, []string{"pending_review"}) {
		t.Errorf("state = %q, want [pending_review]", got)
	}
}

func TestGetChangeProposalsTable(t *testing.T) {
	e := newDataReviewGetEnv(t)
	serveChangeProposals(e, changeProposalBody(cpAlertID, cpWarningID), http.StatusOK)

	out, stderr, err := runChangeProposalsCmd(t, e)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stderr)
	}
	for _, header := range []string{"ID", "STATE", "OPERATION", "TARGET", "TABLE", "CREATED-BY", "CREATED", "ALLOWED"} {
		if !strings.Contains(out, header) {
			t.Errorf("missing header %q\n%s", header, out)
		}
	}
	if strings.Contains(out, "VERSION") || strings.Contains(out, "UPDATED") {
		t.Errorf("version and updated are reachable through --columns, not default columns\n%s", out)
	}
	alert := strings.Join(rowFields(t, out, cpAlertID), " ")
	for _, want := range []string{"pending_review", "alert_resolve", "alert/42", " 7 ", "Pat Proposer",
		"3 hours ago", "approve,reject,request_changes"} {
		if !strings.Contains(alert, want) {
			t.Errorf("alert row missing %q: %s", want, alert)
		}
	}
	warning := strings.Join(rowFields(t, out, cpWarningID), " ")
	for _, want := range []string{"draft", "warning_resolve", "warning/9", " - "} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning row missing %q: %s", want, warning)
		}
	}
	if strings.Contains(stderr, "more may exist") || strings.Contains(stderr, "Warning") {
		t.Errorf("a short page with a working directory prints nothing extra\n%s", stderr)
	}
}

// A deleted target arrives as a null id (ON DELETE SET NULL), so TARGET has
// nothing to name.
func TestGetChangeProposalsDeletedTargetShowsADash(t *testing.T) {
	e := newDataReviewGetEnv(t)
	body := strings.Replace(string(changeProposalBody(cpAlertID)), `"alert_item_id":42`, `"alert_item_id":null`, 1)
	serveChangeProposals(e, []byte(body), http.StatusOK)

	out, stderr, err := runChangeProposalsCmd(t, e)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stderr)
	}
	fields := rowFields(t, out, cpAlertID)
	if len(fields) < 4 || fields[3] != "-" {
		t.Errorf("TARGET cell = %v, want - in the fourth column", fields)
	}
}

func TestGetChangeProposalsDirectoryFailureFallsBackToUUIDs(t *testing.T) {
	e := newDataReviewGetEnv(t)
	serveChangeProposals(e, changeProposalBody(cpAlertID), http.StatusForbidden)

	out, stderr, err := runChangeProposalsCmd(t, e)
	if err != nil {
		t.Fatalf("a failed directory sweep must not fail the listing: %v", err)
	}
	if !strings.Contains(stderr, "Warning: user directory unavailable") {
		t.Errorf("stderr should warn about the directory\n%s", stderr)
	}
	if row := strings.Join(rowFields(t, out, cpAlertID), " "); !strings.Contains(row, cpAuthorID) {
		t.Errorf("CREATED BY should fall back to the UUID: %s", row)
	}
}

func TestGetChangeProposalsFullPageSaysMoreMayExist(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit string
		want  bool
	}{
		{"full page", "2", true},
		{"short page", "3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			serveChangeProposals(e, changeProposalBody(cpAlertID, cpWarningID), http.StatusOK)

			_, stderr, err := runChangeProposalsCmd(t, e, "--limit", tc.limit, "--page", "2")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			hint := "Showing 2 change proposals, a full page; more may exist: use --page 3."
			if got := strings.Contains(stderr, hint); got != tc.want {
				t.Errorf("hint present = %t, want %t\n%s", got, tc.want, stderr)
			}
		})
	}
}

func TestGetChangeProposalsNamePrintsIDsWithoutTheDirectory(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, changeProposalsURL, http.StatusOK, changeProposalBody(cpAlertID, cpWarningID))

	out, _, err := runChangeProposalsCmd(t, e, "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "change-proposal/" + cpAlertID + "\nchange-proposal/" + cpWarningID + "\n"
	if out != want {
		t.Errorf("-o name = %q, want %q", out, want)
	}
	assertPrivilegeRequests(t, e, "GET "+changeProposalsURL)
}

func TestGetChangeProposalsJSONIsTheRawResponse(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, changeProposalsURL, http.StatusOK, changeProposalBody(cpAlertID))

	out, _, err := runChangeProposalsCmd(t, e, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got, want any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", err, out)
	}
	_ = json.Unmarshal(changeProposalBody(cpAlertID), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("-o json is not the wire body\n got: %s", out)
	}
	assertPrivilegeRequests(t, e, "GET "+changeProposalsURL)
}

func TestGetChangeProposalsYAMLKeepsWireKeys(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, changeProposalsURL, http.StatusOK, changeProposalBody(cpAlertID))

	out, _, err := runChangeProposalsCmd(t, e, "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"items:", "allowed_actions:", "created_by_id: " + cpAuthorID, "alert_item_id: 42"} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml missing %q\n%s", want, out)
		}
	}
}

func TestGetChangeProposalsServerErrorsKeepTheirExitCode(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, changeProposalsURL, http.StatusForbidden,
		[]byte(`{"detail":{"msg":"You don't have permission"}}`))

	_, _, err := runChangeProposalsCmd(t, e)
	assertExitCode(t, err, qerrors.ExitUnauthorized)
}

// TestGetChangeProposalsDependsOnColumn: --columns depends_on names the
// dependency, or a dash for none.
func TestGetChangeProposalsDependsOnColumn(t *testing.T) {
	e := newDataReviewGetEnv(t)
	dep := "0190cccc-0000-4000-8000-0000000000d0"
	body := strings.Replace(string(changeProposalBody(cpAlertID, cpWarningID)), `"depends_on_id":null`, `"depends_on_id":"`+dep+`"`, 1)
	serveChangeProposals(e, []byte(body), http.StatusOK)

	out, stderr, err := runChangeProposalsCmd(t, e, "--columns", "id,depends_on")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stderr)
	}
	if !strings.Contains(out, "DEPENDS-ON") {
		t.Errorf("missing DEPENDS-ON header\n%s", out)
	}
	if got := rowFields(t, out, cpAlertID); len(got) != 2 || got[1] != dep {
		t.Errorf("alert row = %v, want its dependency", got)
	}
	if got := rowFields(t, out, cpWarningID); len(got) != 2 || got[1] != "-" {
		t.Errorf("warning row = %v, want a dash", got)
	}
}
