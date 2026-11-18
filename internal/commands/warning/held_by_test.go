package warning

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// The warning twin of the alert test: a direct resolve of a warning a
// reviewable proposal holds says on stderr that the proposal turns obsolete.
func TestResolveWarnsThatAHeldWarningsProposalTurnsObsolete(t *testing.T) {
	holder := uuid.MustParse("6f1c2d3e-4b5a-4c6d-8e7f-90a1b2c3d4e5")
	for _, tc := range []struct {
		state api.ChangeProposalState
		warns bool
	}{
		{api.ChangeProposalStatePendingReview, true},
		{api.ChangeProposalStateExecuting, false},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			mock := newWarningTestEnv(t)
			warning := anomalyWarning(api.NotAnomaly)
			warning.ChangeProposal = &api.ChangeProposalSummarySchema{ID: holder, State: tc.state}
			registerResolvingGetWarning(mock, warning)
			sent := registerResolvePost(mock, nil)

			stdout, stderr, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "not_anomaly")
			if err != nil {
				t.Fatalf("resolve: %v\n%s%s", err, stdout, stderr)
			}
			if *sent != 1 {
				t.Fatalf("want one resolve request, got %d", *sent)
			}
			want := "warning: warning 55 is held by change proposal " + holder.String() + " (" + string(tc.state) +
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
