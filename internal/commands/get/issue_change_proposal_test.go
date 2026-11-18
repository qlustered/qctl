package get

import (
	"bytes"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

// issueListings are the two listings that carry the change-proposal filters.
var issueListings = []struct {
	noun  string
	setup func() *cobra.Command
	empty any
}{
	{"alerts", setupAlertsTestCommand, alerts.AlertsListResponse{}},
	{"warnings", setupWarningsTestCommand, warnings.WarningsListResponse{}},
}

// runIssueListing runs `get <noun> args...` against a mock and returns the
// query of each request it sent to the listing route, and the error.
func runIssueListing(t *testing.T, noun string, setup func() *cobra.Command, empty any, args ...string) ([]url.Values, error) {
	t.Helper()
	mock := newAlertsTestEnv(t)
	var queries []url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/"+noun, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		testutil.RespondJSON(w, http.StatusOK, empty)
	})
	cmd := setup()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", noun}, args...))
	return queries, cmd.Execute()
}

func TestIssueListingsSendTheChangeProposalFilters(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantHas   []string
		wantState []string
	}{
		{"neither", nil, nil, nil},
		{"held", []string{"--has-change-proposal"}, []string{"true"}, nil},
		{"not held", []string{"--has-change-proposal=false"}, []string{"false"}, nil},
		{"states", []string{"--change-proposal-state", "pending_review", "--change-proposal-state", "EXECUTING"},
			nil, []string{"pending_review", "executing"}},
		{"held in a state", []string{"--has-change-proposal", "--change-proposal-state", "changes_requested"},
			[]string{"true"}, []string{"changes_requested"}},
	}
	for _, listing := range issueListings {
		for _, tc := range cases {
			t.Run(listing.noun+"/"+tc.name, func(t *testing.T) {
				queries, err := runIssueListing(t, listing.noun, listing.setup, listing.empty, tc.args...)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(queries) != 1 {
					t.Fatalf("want one request, got %d", len(queries))
				}
				if got := queries[0]["has_change_proposal"]; !reflect.DeepEqual(got, tc.wantHas) {
					t.Errorf("has_change_proposal = %v, want %v", got, tc.wantHas)
				}
				if got := queries[0]["change_proposal_state"]; !reflect.DeepEqual(got, tc.wantState) {
					t.Errorf("change_proposal_state = %v, want %v", got, tc.wantState)
				}
			})
		}
	}
}

func TestIssueListingsRejectBadChangeProposalFiltersBeforeSending(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown state", []string{"--change-proposal-state", "pending"}, `unknown change proposal state "pending"`},
		{"state with not held", []string{"--has-change-proposal=false", "--change-proposal-state", "executing"},
			"cannot be combined with --has-change-proposal=false"},
	}
	for _, listing := range issueListings {
		for _, tc := range cases {
			t.Run(listing.noun+"/"+tc.name, func(t *testing.T) {
				queries, err := runIssueListing(t, listing.noun, listing.setup, listing.empty, tc.args...)
				if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
					t.Fatalf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error = %q, want it to contain %q", err, tc.want)
				}
				if len(queries) != 0 {
					t.Errorf("a refused filter must send nothing, sent %d request(s)", len(queries))
				}
			})
		}
	}
}

// A held alert's change_proposal cell reads "<id> <state>", and the column is
// a default only when the filters select held alerts.
func TestGetAlertsShowsTheHoldingProposal(t *testing.T) {
	held := sampleAlertTiny()
	id := uuid.MustParse("6f1c2d3e-4b5a-4c6d-8e7f-90a1b2c3d4e5")
	held.ChangeProposal = &api.ChangeProposalSummarySchema{ID: id, State: api.ChangeProposalStatePendingReview}

	out := runGetAlertsPage(t, []alerts.AlertTiny{held}, "--has-change-proposal")
	if !strings.Contains(out, "CHANGE-PROPOSAL") || !strings.Contains(out, id.String()+" pending_review") {
		t.Errorf("--has-change-proposal should show the change_proposal column as <id> <state>:\n%s", out)
	}
	if out := runGetAlertsPage(t, []alerts.AlertTiny{held}); strings.Contains(out, "CHANGE-PROPOSAL") {
		t.Errorf("the change_proposal column should be opt-in without a held filter:\n%s", out)
	}
	if out := runGetAlertsPage(t, []alerts.AlertTiny{held}, "-o", "json"); !strings.Contains(out, `"state": "pending_review"`) {
		t.Errorf("-o json should keep change_proposal an object:\n%s", out)
	}
	if out := runGetAlertsPage(t, []alerts.AlertTiny{held}, "-o", "yaml"); !strings.Contains(out, "id: "+id.String()) {
		t.Errorf("-o yaml should print the proposal id as a UUID string:\n%s", out)
	}
}

func TestIssueCellRowsRendersAMissingProposalEmpty(t *testing.T) {
	rows := issueCellRows([]alerts.AlertTiny{sampleAlertTiny()})
	cell := reflect.ValueOf(rows).Index(0).FieldByName("ChangeProposal")
	if cell.Kind() != reflect.String || cell.String() != "" {
		t.Errorf("ChangeProposal cell = %#v, want the empty string", cell.Interface())
	}
}
