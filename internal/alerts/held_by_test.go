package alerts

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

var heldByID = uuid.MustParse("6f1c2d3e-4b5a-4c6d-8e7f-90a1b2c3d4e5")

func TestResolveObsoletesFollowsTheProposalState(t *testing.T) {
	for state, want := range map[api.ChangeProposalState]bool{
		api.ChangeProposalStatePendingReview:    true,
		api.ChangeProposalStateChangesRequested: true,
		api.ChangeProposalStateExecuting:        false,
		api.ChangeProposalStateExecuted:         false,
		api.ChangeProposalStateObsolete:         false,
	} {
		cp := &api.ChangeProposalSummarySchema{ID: heldByID, State: state}
		if got := ResolveObsoletes(cp); got != want {
			t.Errorf("ResolveObsoletes(%s) = %t, want %t", state, got, want)
		}
	}
	if ResolveObsoletes(nil) {
		t.Error("ResolveObsoletes(nil) = true, want false")
	}
}

func TestRenderHeldByPlain(t *testing.T) {
	if got := RenderHeldByPlain(nil, "alert"); got != "" {
		t.Errorf("RenderHeldByPlain(nil) = %q, want empty", got)
	}

	pending := RenderHeldByPlain(&api.ChangeProposalSummarySchema{ID: heldByID, State: api.ChangeProposalStatePendingReview}, "warning")
	want := "\nHELD BY CHANGE PROPOSAL\n" +
		"  " + heldByID.String() + " (pending_review) — resolving this warning directly turns the proposal obsolete\n" +
		"Next: qctl describe change-proposal " + heldByID.String() + "\n"
	if pending != want {
		t.Errorf("RenderHeldByPlain(pending_review) =\n%q\nwant\n%q", pending, want)
	}

	executing := RenderHeldByPlain(&api.ChangeProposalSummarySchema{ID: heldByID, State: api.ChangeProposalStateExecuting}, "alert")
	if strings.Contains(executing, "obsolete") || !strings.Contains(executing, "(executing)") {
		t.Errorf("an executing holder is named without the obsolete note:\n%s", executing)
	}
}

func TestHeldByResolveWarning(t *testing.T) {
	cp := &api.ChangeProposalSummarySchema{ID: heldByID, State: api.ChangeProposalStateChangesRequested}
	want := "warning: alert 9 is held by change proposal " + heldByID.String() +
		" (changes_requested); resolving it directly turns that proposal obsolete\n"
	if got := HeldByResolveWarning(cp, "alert", 9); got != want {
		t.Errorf("HeldByResolveWarning = %q, want %q", got, want)
	}
	cp.State = api.ChangeProposalStateExecuting
	if got := HeldByResolveWarning(cp, "alert", 9); got != "" {
		t.Errorf("HeldByResolveWarning(executing) = %q, want empty", got)
	}
}
