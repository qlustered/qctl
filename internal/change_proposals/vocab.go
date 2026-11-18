package change_proposals //nolint:revive // Established package-path style in this repo (rule_reviews, privilege_requests).

import (
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

var (
	states = []State{
		api.ChangeProposalStateDraft,
		api.ChangeProposalStatePendingReview,
		api.ChangeProposalStateChangesRequested,
		api.ChangeProposalStateExecuting,
		api.ChangeProposalStateExecuted,
		api.ChangeProposalStateExecutionFailed,
		api.ChangeProposalStateRejected,
		api.ChangeProposalStateWithdrawn,
		api.ChangeProposalStateObsolete,
	}
	liveStates = []State{
		api.ChangeProposalStateDraft,
		api.ChangeProposalStatePendingReview,
		api.ChangeProposalStateChangesRequested,
		api.ChangeProposalStateExecuting,
	}
	proposableOperations = []Operation{
		api.ChangeReviewOperationAlertResolve,
		api.ChangeReviewOperationWarningResolve,
		api.ChangeReviewOperationDatasetRulePatch,
	}
)

// States returns every proposal state, in lifecycle order.
func States() []State { return append([]State(nil), states...) }

// LiveStates returns the non-terminal states.
func LiveStates() []State { return append([]State(nil), liveStates...) }

// ProposableOperations returns the operations a proposal can carry.
func ProposableOperations() []Operation { return append([]Operation(nil), proposableOperations...) }

// IsLive reports whether s is a non-terminal state.
func IsLive(s State) bool {
	for _, v := range liveStates {
		if v == s {
			return true
		}
	}
	return false
}

// StateNames returns the wire spelling of every state, in lifecycle order.
func StateNames() []string { return names(states) }

// OperationNames returns the wire spelling of every proposable operation.
func OperationNames() []string { return names(proposableOperations) }

func names[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

func match[T ~string](values []T, s string) (T, bool) {
	for _, v := range values {
		if strings.EqualFold(string(v), s) {
			return v, true
		}
	}
	return "", false
}

// ParseState matches s case-insensitively against the states. An unknown
// value is a bad-usage error (exit 2).
func ParseState(s string) (State, error) {
	v, ok := match(states, s)
	if !ok {
		return "", qerrors.NewBadUsagef("unknown change proposal state %q\nvalid states: %s",
			s, strings.Join(StateNames(), ", "))
	}
	return v, nil
}

// ParseStates applies ParseState to each value, keeping the first occurrence
// of each state.
func ParseStates(values []string) ([]State, error) {
	out := make([]State, 0, len(values))
	for _, s := range values {
		v, err := ParseState(s)
		if err != nil {
			return nil, err
		}
		dup := false
		for _, seen := range out {
			if seen == v {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, v)
		}
	}
	return out, nil
}

// ParseOperation matches s case-insensitively against the proposable
// operations. Any other value is a bad-usage error (exit 2).
func ParseOperation(s string) (Operation, error) {
	v, ok := match(proposableOperations, s)
	if !ok {
		return "", qerrors.NewBadUsagef("unknown change proposal operation %q\nvalid operations: %s",
			s, strings.Join(OperationNames(), ", "))
	}
	return v, nil
}

// ParseID parses a proposal id. Anything but a UUID is a bad-usage error
// (exit 2).
func ParseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}, qerrors.NewBadUsagef("invalid change proposal id %q: expected a UUID", s)
	}
	return id, nil
}

// Target names the proposal's target as alert/<id>, warning/<id> or
// rule/<uuid>, or "" when it has none.
func Target(p *Proposal) string {
	return target(p.AlertItemID, p.WarningItemID, p.DatasetRuleID)
}

// ListItemTarget is Target for a listed proposal.
func ListItemTarget(p *ListItem) string {
	return target(p.AlertItemID, p.WarningItemID, p.DatasetRuleID)
}

func target(alertID, warningID *int, ruleID *uuid.UUID) string {
	switch {
	case alertID != nil:
		return "alert/" + strconv.Itoa(*alertID)
	case warningID != nil:
		return "warning/" + strconv.Itoa(*warningID)
	case ruleID != nil:
		return RuleRef(*ruleID)
	default:
		return ""
	}
}

// RuleRef names a table rule as rule/<uuid>.
func RuleRef(id uuid.UUID) string { return "rule/" + id.String() }

// TargetKind names the kind of target an operation's proposal has: alert,
// warning, rule, or "" for any other operation.
func TargetKind(op Operation) string {
	switch op {
	case api.ChangeReviewOperationAlertResolve:
		return "alert"
	case api.ChangeReviewOperationWarningResolve:
		return "warning"
	case api.ChangeReviewOperationDatasetRulePatch:
		return TargetKindRule
	default:
		return ""
	}
}

// TargetKindRule is TargetKind's name for a dataset_rule_patch target.
const TargetKindRule = "rule"
