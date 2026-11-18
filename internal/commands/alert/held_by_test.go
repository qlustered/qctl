package alert

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

var holderID = uuid.MustParse("6f1c2d3e-4b5a-4c6d-8e7f-90a1b2c3d4e5")

// A direct resolve of an alert a reviewable proposal holds says on stderr that
// the proposal turns obsolete; an executing holder does not, since the backend
// leaves it alone. Both the flag and -f paths.
func TestResolveWarnsThatAHeldAlertsProposalTurnsObsolete(t *testing.T) {
	manifest := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: discard_migration\n"
	for _, tc := range []struct {
		name  string
		state api.ChangeProposalState
		file  bool
		warns bool
	}{
		{"flag/pending_review", api.ChangeProposalStatePendingReview, false, true},
		{"file/changes_requested", api.ChangeProposalStateChangesRequested, true, true},
		{"flag/executing", api.ChangeProposalStateExecuting, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			alert := migrationAlert()
			alert.ChangeProposal = &api.ChangeProposalSummarySchema{ID: holderID, State: tc.state}
			registerResolvingGetAlert(mock, alert)
			registerEmptySuggestions(mock)
			sent := 0
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
				func(w http.ResponseWriter, _ *http.Request) {
					sent++
					testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
				})

			args := []string{"alert", "resolve", "9", "--action", "discard_migration", "--yes"}
			if tc.file {
				args = []string{"alert", "resolve", "-f", writeManifest(t, manifest), "--yes"}
			}
			stdout, stderr, err := runAlertCommandSplit(t, args...)
			if err != nil {
				t.Fatalf("resolve: %v\n%s%s", err, stdout, stderr)
			}
			if sent != 1 {
				t.Fatalf("want one resolve request, got %d", sent)
			}
			want := "warning: alert 9 is held by change proposal " + holderID.String() + " (" + string(tc.state) +
				"); resolving it directly turns that proposal obsolete\n"
			if got := strings.Contains(stderr, want); got != tc.warns {
				t.Errorf("stderr has the obsolete warning = %t, want %t:\n%s", got, tc.warns, stderr)
			}
			if strings.Contains(stdout, "held by change proposal") {
				t.Errorf("the warning belongs on stderr, not stdout:\n%s", stdout)
			}
		})
	}
}
