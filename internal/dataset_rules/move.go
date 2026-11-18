package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/org"
)

// FindRuleInList resolves a table-rule reference against a table's full rule
// list, and only against it: an exact instance name, a full UUID, or a UUID
// prefix (the SHORT-ID `get table-rules` prints).
//
// It is stricter than ResolveDatasetRule in two ways, both because the caller
// is about to WRITE. It never falls back to a case-insensitive substring match,
// so a partial name cannot pick a rule to move; and a full UUID must actually
// be in the list, so a rule from another table is refused here rather than by
// the server after a prompt named it.
func FindRuleInList(rules []DatasetRuleTiny, input string) (DatasetRuleTiny, error) {
	if input == "" {
		return DatasetRuleTiny{}, qerrors.NewBadUsage("table-rule identifier cannot be empty")
	}
	if matches := findExactInstanceNameMatches(rules, input); len(matches) == 1 {
		return matches[0], nil
	} else if len(matches) > 1 {
		return DatasetRuleTiny{}, qerrors.Wrap(qerrors.ExitBadUsage, formatAmbiguousError(input, matches), "")
	}
	if org.IsUUIDLike(input) {
		matches := findUUIDPrefixMatches(rules, input)
		if isFullUUID(input) {
			// A prefix match on a full UUID can only be the rule itself, but
			// say so structurally rather than relying on that.
			matches = matches[:0:0]
			for _, r := range rules {
				if strings.EqualFold(r.ID.String(), input) {
					matches = append(matches, r)
				}
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return DatasetRuleTiny{}, qerrors.Wrap(qerrors.ExitBadUsage, formatAmbiguousError(input, matches), "")
		}
	}
	return DatasetRuleTiny{}, qerrors.NewNotFound(apierror.WithScopeNote(formatNoMatchError(input, rules).Error()))
}

// MoveDestination is where `table-rule move` was asked to put a rule. Exactly
// one of the three is set.
type MoveDestination struct {
	// Position is a STORED position value, the number `get table-rules`
	// prints, and never a dense index: positions can have gaps.
	Position int
	Before   *DatasetRuleTiny
	After    *DatasetRuleTiny
}

// MovePlan is what a reorder will do, computed from the rule list with the
// server's own arithmetic (RuleController.reorder_rule):
//
//   - the requested position is clamped into [1, max(position)], and max is
//     taken over EVERY rule on the table, soft-deleted ones included, because
//     the controller's select carries no state filter;
//   - moving UP to p shifts every rule with p <= position < from down the list
//     by one (+1); moving DOWN to p shifts every rule with from < position <= p
//     up the list by one (-1).
//
// To is therefore both the value sent and the position the rule holds
// afterwards. Moving the rule back to From restores the ORDER and every
// shifted rule's position exactly, gaps included — the reverse move's shift
// range is the same set of rules one position along. The one number it may not
// restore is the moved rule's own, and only when From was the table's highest
// position with NO rule directly below it (a gap at From-1): moving the rule up
// then lowers max(position), and From is clamped on the way back to the new
// last slot. On a table with a rule at From-1 that rule shifts up into From and
// the max does not move. TestPlanMove_PositionMatchesTheServer checks both
// halves against a transcription of the server.
type MovePlan struct {
	Rule DatasetRuleTiny
	From int
	To   int
	// Requested is the --position value as typed, 0 for --before/--after.
	Requested int
	// Last is max(position), the value an over-large --position clamps to.
	Last int
	// Shifted is every other rule that moves by one, in position order.
	Shifted []DatasetRuleTiny
}

// Unchanged reports a plan that moves nothing. The server would answer ok to
// it too (reorder_rule returns early), so the caller sends no request.
func (p MovePlan) Unchanged() bool { return p.From == p.To }

// Clamped reports a --position the server would not have honoured as typed.
func (p MovePlan) Clamped() bool { return p.Requested > 0 && p.Requested != p.To }

// PlanMove computes the plan for moving rule to dest over the table's rules.
//
// --before and --after are translated into the ONE stored position that puts
// the rule immediately next to the target in execution order, and the
// translation depends on the direction of travel, because the target itself
// shifts when it lies inside the shift range:
//
//   - before X, moving up (from > X): send X's position; X shifts down one.
//   - before X, moving down (from < X): send X's position - 1; X stays put.
//   - after X, moving down (from < X): send X's position; X shifts up one.
//   - after X, moving up (from > X): send X's position + 1; X stays put.
//
// A rule that is already immediately before (or after) its target is left
// where it is, even when a gap separates the two: "before X" is a statement
// about order, and sending X-1 there would renumber the rule for nothing.
func PlanMove(rules []DatasetRuleTiny, rule DatasetRuleTiny, dest MoveDestination) (MovePlan, error) {
	ordered := make([]DatasetRuleTiny, len(rules))
	copy(ordered, rules)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })

	idx := indexOfRule(ordered, rule)
	if idx < 0 {
		return MovePlan{}, fmt.Errorf("internal error: table-rule %s is not in its table's rule list", rule.ID)
	}
	plan := MovePlan{Rule: ordered[idx], From: ordered[idx].Position}
	for _, r := range ordered {
		plan.Last = max(plan.Last, r.Position)
	}

	var err error
	switch {
	case dest.Before != nil:
		plan.To, err = positionBefore(ordered, idx, dest.Before)
	case dest.After != nil:
		plan.To, err = positionAfter(ordered, idx, dest.After)
	case dest.Position > 0:
		plan.Requested = dest.Position
		plan.To = min(dest.Position, plan.Last)
	default:
		err = fmt.Errorf("internal error: move destination is empty")
	}
	if err != nil {
		return MovePlan{}, err
	}
	plan.Shifted = shiftedRules(ordered, plan)
	return plan, nil
}

func indexOfRule(ordered []DatasetRuleTiny, rule DatasetRuleTiny) int {
	for i, r := range ordered {
		if r.ID == rule.ID {
			return i
		}
	}
	return -1
}

// targetIndex finds a --before/--after target in the ordered list, refusing
// the rule being moved as its own target.
func targetIndex(ordered []DatasetRuleTiny, idx int, target *DatasetRuleTiny, flag string) (int, error) {
	if target.ID == ordered[idx].ID {
		return 0, qerrors.NewBadUsagef("%s names the rule being moved: pick a different rule to move it next to", flag)
	}
	if ti := indexOfRule(ordered, *target); ti >= 0 {
		return ti, nil
	}
	return 0, fmt.Errorf("internal error: %s target %s is not in the table's rule list", flag, target.ID)
}

// positionBefore is the stored position that puts ordered[idx] immediately
// before target (see PlanMove for the direction rule).
func positionBefore(ordered []DatasetRuleTiny, idx int, target *DatasetRuleTiny) (int, error) {
	ti, err := targetIndex(ordered, idx, target, "--before")
	if err != nil {
		return 0, err
	}
	from, at := ordered[idx].Position, ordered[ti].Position
	switch {
	case ti == idx+1:
		return from, nil
	case from > at:
		return at, nil
	default:
		return at - 1, nil
	}
}

// positionAfter is positionBefore's mirror.
func positionAfter(ordered []DatasetRuleTiny, idx int, target *DatasetRuleTiny) (int, error) {
	ti, err := targetIndex(ordered, idx, target, "--after")
	if err != nil {
		return 0, err
	}
	from, at := ordered[idx].Position, ordered[ti].Position
	switch {
	case ti == idx-1:
		return from, nil
	case from < at:
		return at, nil
	default:
		return at + 1, nil
	}
}

// shiftedRules is every other rule inside the server's shift range, in order.
func shiftedRules(ordered []DatasetRuleTiny, plan MovePlan) []DatasetRuleTiny {
	var shifted []DatasetRuleTiny
	for _, r := range ordered {
		if r.ID == plan.Rule.ID {
			continue
		}
		if (plan.To < plan.From && r.Position >= plan.To && r.Position < plan.From) ||
			(plan.To > plan.From && r.Position > plan.From && r.Position <= plan.To) {
			shifted = append(shifted, r)
		}
	}
	return shifted
}
