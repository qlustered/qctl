package rule_suggestions

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
)

// candidate builds a pending revalidation candidate; each test mutates one field.
func candidate() *Suggestion {
	yes := true
	jobID := 8814
	return &Suggestion{
		State:                   api.SuggestionStatePending,
		IsRevalidationCandidate: &yes,
		ValidationDryRunJobID:   &jobID,
	}
}

func TestIsCandidate(t *testing.T) {
	no := false
	cases := map[string]struct {
		s    *Suggestion
		want bool
	}{
		"flag true":    {candidate(), true},
		"flag false":   {&Suggestion{IsRevalidationCandidate: &no}, false},
		"flag absent":  {&Suggestion{}, false},
		"nil received": {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := IsCandidate(tc.s); got != tc.want {
				t.Errorf("IsCandidate = %v, want %v", got, tc.want)
			}
		})
	}
}

// IsValidatedCandidate is the backend's three-field predicate. All three are
// required: dropping any one must flip it false.
func TestIsValidatedCandidateNeedsAllThreeFields(t *testing.T) {
	at := time.Now()
	full := candidate()
	full.ValidatedAt = &at
	if !IsValidatedCandidate(full) {
		t.Fatal("a candidate with validated_at and a job id must be validated")
	}

	drop := map[string]func(*Suggestion){
		"not a candidate":    func(s *Suggestion) { s.IsRevalidationCandidate = nil },
		"dry run not passed": func(s *Suggestion) { s.ValidatedAt = nil },
		"no dry run job id":  func(s *Suggestion) { s.ValidationDryRunJobID = nil },
	}
	for name, mutate := range drop {
		t.Run(name, func(t *testing.T) {
			s := candidate()
			s.ValidatedAt = &at
			mutate(s)
			if IsValidatedCandidate(s) {
				t.Errorf("%s must not count as validated", name)
			}
		})
	}
}

func TestPhaseOf(t *testing.T) {
	at := time.Now()

	validated := candidate()
	validated.ValidatedAt = &at

	promoted := candidate()
	promoted.State = api.SuggestionStateApplied

	failed := candidate()
	failed.State = api.SuggestionStateFailed

	dismissed := candidate()
	dismissed.State = api.SuggestionStateDismissed

	yes, no := true, false
	queued := candidate()
	queued.ValidationDryRunJobID = nil
	queued.ValidationQueued = &yes

	staged := candidate()
	staged.ValidationQueued = &no

	// A terminal state wins over validated_at: a promoted candidate still
	// carries the timestamp that promoted it.
	promotedAfterValidation := candidate()
	promotedAfterValidation.State = api.SuggestionStateApplied
	promotedAfterValidation.ValidatedAt = &at

	cases := map[string]struct {
		s    *Suggestion
		want Phase
	}{
		"ordinary staged edit":       {&Suggestion{State: api.SuggestionStatePending}, PhaseNotCandidate},
		"validation queued":          {queued, PhaseQueued},
		"dry run staged":             {staged, PhaseValidating},
		"queued flag absent":         {candidate(), PhaseValidating},
		"passed, awaiting migration": {validated, PhaseValidatedPendingMigration},
		"promoted":                   {promoted, PhasePromoted},
		"promoted after validating":  {promotedAfterValidation, PhasePromoted},
		"failed":                     {failed, PhaseFailed},
		"dismissed":                  {dismissed, PhaseDismissed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := PhaseOf(tc.s, nil); got != tc.want {
				t.Errorf("PhaseOf = %v (%s), want %v (%s)", got, got, tc.want, tc.want)
			}
		})
	}
}

func testLockedPeriod() *datasets.LockedPeriod {
	return &datasets.LockedPeriod{
		SeriesID:   uuid.MustParse("0b8c3f9e-1d2a-4e5f-9a6b-7c8d9e0f1a2b"),
		SeriesName: "Monthly bordereau",
		PeriodKey:  "2026-09",
	}
}

// A locked period holds only a pending candidate that has no run and is not
// queued; every other phase ignores it.
func TestPhaseOfLockedPeriod(t *testing.T) {
	at := time.Now()
	yes, no := true, false

	held := candidate()
	held.ValidationDryRunJobID = nil
	held.ValidationQueued = &no

	heldFlagAbsent := candidate()
	heldFlagAbsent.ValidationDryRunJobID = nil

	queued := candidate()
	queued.ValidationDryRunJobID = nil
	queued.ValidationQueued = &yes

	validated := candidate()
	validated.ValidatedAt = &at

	dismissed := candidate()
	dismissed.ValidationDryRunJobID = nil
	dismissed.State = api.SuggestionStateDismissed

	cases := map[string]struct {
		s    *Suggestion
		want Phase
	}{
		"no run, not queued":   {held, PhaseHeldLockedPeriod},
		"no run, flag absent":  {heldFlagAbsent, PhaseHeldLockedPeriod},
		"queued":               {queued, PhaseQueued},
		"run linked":           {candidate(), PhaseValidating},
		"passed":               {validated, PhaseValidatedPendingMigration},
		"dismissed":            {dismissed, PhaseDismissed},
		"ordinary staged edit": {&Suggestion{State: api.SuggestionStatePending}, PhaseNotCandidate},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := PhaseOf(tc.s, testLockedPeriod()); got != tc.want {
				t.Errorf("PhaseOf = %v (%s), want %v (%s)", got, got, tc.want, tc.want)
			}
			if tc.want == PhaseHeldLockedPeriod && PhaseOf(tc.s, nil) != PhaseValidating {
				t.Errorf("without a locked period the candidate must be PhaseValidating, got %s", PhaseOf(tc.s, nil))
			}
		})
	}
}
