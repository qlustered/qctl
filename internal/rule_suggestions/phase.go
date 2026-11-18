package rule_suggestions

import (
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
)

// MaxRevalidationRetries is the backend's fresh-dry-run budget for one
// candidate snapshot (atlas cettings/cettings/controllers/rule_revalidation.py,
// MAX_REVALIDATION_RETRIES). Rendered alongside revalidation_retry_count,
// because a bare "1" tells a reader nothing. Only a run the orphan sweeper finds
// dead spends one (retry_or_fail_revalidation_candidate), and a rule patch that
// replaces the snapshot resets the count to 0 (apply_candidate_replacement,
// capi/capi/controllers/rule_revalidation_view_controller.py).
const MaxRevalidationRetries = 2

// Phase is where a suggestion sits in its lifecycle. It exists because a
// revalidation candidate and an ordinary staged edit share `state` values
// but are resolved by completely different things: an edit waits for a
// person, a candidate waits for its own validation dry run.
type Phase int

const (
	// PhaseNotCandidate is an ordinary staged edit, decided by accept/dismiss.
	PhaseNotCandidate Phase = iota
	// PhaseQueued is a pending candidate whose validation request waits for
	// the scheduler to stage its dry run: validation_queued is true and no run
	// is linked yet.
	PhaseQueued
	// PhaseValidating is a pending candidate that has not passed and is not
	// reported queued: its dry run is staged or running, or it completed with
	// a failing verdict. That last case is not an end state -- a later rule
	// patch can replace the snapshot, or you can dismiss it.
	PhaseValidating
	// PhaseValidatedPendingMigration is a candidate that passed but needs a
	// schema change, so it waits for the draft migration to land. This is the
	// case alerts.SuggestionGate models as Exempt.
	PhaseValidatedPendingMigration
	// PhasePromoted is a candidate the backend wrote onto the live rule when
	// its dry run passed. No person accepted it.
	PhasePromoted
	// PhaseFailed is a candidate the backend resolved as failed;
	// resolution_msg says why. Only a candidate can reach this state. It is
	// never a verdict on the rule: a run that completes and fails validation
	// leaves the candidate PhaseValidating.
	PhaseFailed
	// PhaseDismissed is a candidate a person retired.
	PhaseDismissed
	// PhaseHeldLockedPeriod is a pending candidate with no validation dry
	// run, not reported queued, on a table whose series period is locked.
	PhaseHeldLockedPeriod
)

func (p Phase) String() string {
	switch p {
	case PhaseQueued:
		return "queued (dry run not staged yet)"
	case PhaseValidating:
		return "validating"
	case PhaseValidatedPendingMigration:
		return "validated (awaiting schema migration)"
	case PhasePromoted:
		return "promoted onto the live rule"
	case PhaseFailed:
		return "failed"
	case PhaseDismissed:
		return "dismissed"
	case PhaseHeldLockedPeriod:
		return "held (table period locked)"
	default:
		return "staged edit"
	}
}

// IsCandidate reports whether this full suggestion is an in-place revalidation
// candidate rather than an ordinary staged edit. List rows carry the same flag,
// but this helper accepts the detail type used by phase classification.
func IsCandidate(s *Suggestion) bool {
	return s != nil && s.IsRevalidationCandidate != nil && *s.IsRevalidationCandidate
}

// IsValidatedCandidate ports the backend's _is_validated_revalidation_candidate
// (atlas capi/capi/controllers/alert_handlers/migration_handlers.py). All
// three fields are required: the flag marks the row a candidate, the job id is
// stamped when its dry run launches, and validated_at only when that run passes.
func IsValidatedCandidate(s *Suggestion) bool {
	return IsCandidate(s) && s.ValidatedAt != nil && s.ValidationDryRunJobID != nil
}

// AwaitsValidationRun reports whether s is a pending candidate that has not
// passed, has no validation dry run and is not reported queued: the only
// candidate whose phase depends on its table's period lock.
func AwaitsValidationRun(s *Suggestion) bool {
	return IsCandidate(s) && s.State == api.SuggestionStatePending && s.ValidatedAt == nil &&
		s.ValidationDryRunJobID == nil && (s.ValidationQueued == nil || !*s.ValidationQueued)
}

// PhaseOf classifies a suggestion; locked is its table's locked period, nil
// when the period is not locked or was not read. A terminal state wins over
// validated_at, because a promoted candidate still carries the timestamp that
// promoted it. A response without validation_queued classifies a queued
// candidate as PhaseValidating, or PhaseHeldLockedPeriod when locked is set.
func PhaseOf(s *Suggestion, locked *datasets.LockedPeriod) Phase {
	if !IsCandidate(s) {
		return PhaseNotCandidate
	}
	switch s.State {
	case api.SuggestionStateApplied:
		return PhasePromoted
	case api.SuggestionStateFailed:
		return PhaseFailed
	case api.SuggestionStateDismissed:
		return PhaseDismissed
	}
	if s.ValidatedAt != nil {
		return PhaseValidatedPendingMigration
	}
	if s.ValidationQueued != nil && *s.ValidationQueued {
		return PhaseQueued
	}
	if locked != nil && AwaitsValidationRun(s) {
		return PhaseHeldLockedPeriod
	}
	return PhaseValidating
}
