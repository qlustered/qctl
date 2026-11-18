package alerts

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestCarriedSuggestionFitGate(t *testing.T) {
	fit := pendingDetail("11111111-1111-1111-1111-111111111111", api.SuggestionActionCreate, "Fits", 3)
	fit.Source = api.RecreatedPeriod
	misfit := pendingDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionCreate, "Misfit\x1b[31m", 3)
	misfit.Source = api.RecreatedPeriod
	unknown := pendingDetail("33333333-3333-3333-3333-333333333333", api.SuggestionActionCreate, "Unknown", 3)
	unknown.Source = api.RecreatedPeriod
	other := pendingDetail("44444444-4444-4444-4444-444444444444", api.SuggestionActionCreate, "Other draft", 4)
	other.Source = api.RecreatedPeriod
	decided := pendingDetail("55555555-5555-5555-5555-555555555555", api.SuggestionActionCreate, "Decided", 3)
	decided.Source, decided.State = api.RecreatedPeriod, api.SuggestionStateDismissed
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{fit, misfit, unknown, other, decided}, migrationID(3)).WithCarriedRules([]api.RecreatedPeriodRuleSchema{
		{SuggestionID: fit.ID, Fits: true},
		{SuggestionID: misfit.ID, Fits: false, MissingColumns: []string{"absent\x1b[31m"}},
		{SuggestionID: other.ID, Fits: false, MissingColumns: []string{"wrong draft"}},
		{SuggestionID: decided.ID, Fits: true},
	})
	if len(g.AutoAccept) != 1 || g.AutoAccept[0].ID != fit.ID.String() || len(g.AutoDismiss) != 1 || g.AutoDismiss[0].ID != misfit.ID.String() ||
		len(g.Unchecked) != 1 || g.Unchecked[0].ID != unknown.ID.String() || len(g.JobApply) != 1 || g.JobApply[0].ID != other.ID.String() {
		t.Fatalf("unexpected carried suggestion gate: %+v", g)
	}
	out := g.RenderPreflight(42)
	for _, want := range []string{"Fits (carried from recreated period)", "1 carried suggestion(s) do not fit", "Misfit\\x1b[31m", "absent\\x1b[31m", "Could not check the fit of 1", "Other draft"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q: %s", want, out)
		}
	}
	if strings.ContainsRune(out, '\x1b') || strings.Contains(out, "Decided") {
		t.Fatalf("unsafe or already decided entry: %s", out)
	}
	if err := g.CheckAccepts([]uuid.UUID{misfit.ID}); err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("accept misfit: %v, want exit 2", err)
	}
	if err := g.CheckAccepts([]uuid.UUID{fit.ID, unknown.ID, other.ID}); err != nil {
		t.Fatal(err)
	}
	g = g.WithDecisions([]uuid.UUID{fit.ID}, []uuid.UUID{misfit.ID})
	if len(g.AutoAccept)+len(g.AutoDismiss) != 0 || len(g.Accept) != 1 || len(g.Dismiss) != 1 {
		t.Fatalf("explicit carried decisions: %+v", g)
	}
	if !strings.Contains(g.RenderPreflight(42), "server rechecks") {
		t.Fatal("dismissal preview must explain fit re-evaluation")
	}
}

func TestCarriedOnlyGateIsNotEmpty(t *testing.T) {
	s := pendingDetail("11111111-1111-1111-1111-111111111111", api.SuggestionActionCreate, "Carried", 3)
	s.Source = api.RecreatedPeriod
	g := BuildSuggestionGate([]api.DatasetRuleSuggestionSchema{s}, migrationID(3))
	for _, gate := range []SuggestionGate{g, g.WithCarriedRules([]api.RecreatedPeriodRuleSchema{{SuggestionID: s.ID, Fits: false, MissingColumns: []string{"gone"}}})} {
		if gate.Empty() || gate.RenderPreflight(42) == "" || len(gate.AutoAccept) != 0 {
			t.Fatalf("carried suggestion must be previewed without promising acceptance: %+v", gate)
		}
	}
}
