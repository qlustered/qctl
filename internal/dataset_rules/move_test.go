package dataset_rules //nolint:revive // Match the established package under test.

import (
	"fmt"
	"sort"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// serverReorder is atlas's RuleController.reorder_rule, transcribed
// (cettings/cettings/controllers/rule_controller/rule_controller_core.py,
// reorder_rule): clamp into [1, max(position)], then shift the rules between
// the old and new positions by one toward the vacated slot. It is the oracle
// every PlanMove property below is checked against, so the plan is judged by
// what the server would DO with the value it sends, not by the plan's own
// arithmetic.
func serverReorder(rules []DatasetRuleTiny, id uuid.UUID, newPosition int) []DatasetRuleTiny {
	out := make([]DatasetRuleTiny, len(rules))
	copy(out, rules)
	maxPos := 0
	for _, r := range out {
		if r.Position > maxPos {
			maxPos = r.Position
		}
	}
	if newPosition < 1 {
		newPosition = 1
	} else if newPosition > maxPos {
		newPosition = maxPos
	}
	var moved *DatasetRuleTiny
	for i := range out {
		if out[i].ID == id {
			moved = &out[i]
		}
	}
	old := moved.Position
	if newPosition == old {
		return out
	}
	for i := range out {
		if out[i].ID == id {
			continue
		}
		p := out[i].Position
		if newPosition < old && p >= newPosition && p < old {
			out[i].Position++
		}
		if newPosition > old && p > old && p <= newPosition {
			out[i].Position--
		}
	}
	moved.Position = newPosition
	return out
}

func ruleAt(name string, pos int) DatasetRuleTiny {
	return DatasetRuleTiny{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)), InstanceName: name, Position: pos}
}

// order is the instance names in execution order.
func order(rules []DatasetRuleTiny) []string {
	s := make([]DatasetRuleTiny, len(rules))
	copy(s, rules)
	sort.Slice(s, func(i, j int) bool { return s[i].Position < s[j].Position })
	names := make([]string, len(s))
	for i, r := range s {
		names[i] = r.InstanceName
	}
	return names
}

func positionOf(rules []DatasetRuleTiny, id uuid.UUID) int {
	for _, r := range rules {
		if r.ID == id {
			return r.Position
		}
	}
	return -1
}

// The tables the properties run over: contiguous, gapped at the front, in the
// middle and between every pair — the shapes a cascade delete of a revision
// leaves behind.
var moveTables = map[string][]DatasetRuleTiny{
	"contiguous":  {ruleAt("a", 1), ruleAt("b", 2), ruleAt("c", 3), ruleAt("d", 4), ruleAt("e", 5)},
	"gap-front":   {ruleAt("a", 3), ruleAt("b", 4), ruleAt("c", 5)},
	"gap-middle":  {ruleAt("a", 1), ruleAt("b", 2), ruleAt("c", 4), ruleAt("d", 7)},
	"gap-between": {ruleAt("a", 2), ruleAt("b", 4), ruleAt("c", 6), ruleAt("d", 8)},
	"two":         {ruleAt("a", 1), ruleAt("b", 9)},
}

// checkPlan asserts the three things a plan claims about the server: To is
// where the rule lands, Shifted is exactly the set of other rules whose
// position changed, and sending From back restores the table.
//
// "Restores" is exact for the order and for every OTHER rule's position, and
// exact for the moved rule's own number except when it held the highest
// position with no rule at From-1: moving that rule up then lowers
// max(position), so sending its old number back is clamped to the new max —
// the right place in the order under a smaller number. This test is what found
// that; MovePlan's doc once claimed the reversal was exact outright, and later
// claimed the exception for every last rule, gapped or not.
func checkPlan(t *testing.T, rules []DatasetRuleTiny, plan MovePlan) []DatasetRuleTiny {
	t.Helper()
	after := serverReorder(rules, plan.Rule.ID, plan.To)
	if got := positionOf(after, plan.Rule.ID); got != plan.To {
		t.Errorf("server put the rule at %d, plan says %d", got, plan.To)
	}
	changed := map[uuid.UUID]bool{}
	for _, r := range after {
		if r.ID != plan.Rule.ID && positionOf(rules, r.ID) != r.Position {
			changed[r.ID] = true
		}
	}
	if len(changed) != len(plan.Shifted) {
		t.Errorf("server shifted %d rule(s), plan lists %d", len(changed), len(plan.Shifted))
	}
	for _, r := range plan.Shifted {
		if !changed[r.ID] {
			t.Errorf("plan lists %s as shifted, server did not move it", r.InstanceName)
		}
	}
	back := serverReorder(after, plan.Rule.ID, plan.From)
	if fmt.Sprint(order(back)) != fmt.Sprint(order(rules)) {
		t.Errorf("reversal order = %v, want %v", order(back), order(rules))
	}
	gapBelowLast := plan.From == plan.Last && plan.To < plan.From
	for _, r := range rules {
		if r.Position == plan.From-1 {
			gapBelowLast = false
		}
	}
	for _, r := range rules {
		if r.ID == plan.Rule.ID && gapBelowLast {
			continue
		}
		if got := positionOf(back, r.ID); got != r.Position {
			t.Errorf("reversal left %s at %d, was %d", r.InstanceName, got, r.Position)
		}
	}
	return after
}

// The rule list is not trusted to arrive in position order: the plan must be
// the one the sorted list gives.
func TestPlanMove_UnsortedInputPlansAsSorted(t *testing.T) {
	sorted := moveTables["gap-middle"] // a=1 b=2 c=4 d=7
	shuffled := []DatasetRuleTiny{sorted[3], sorted[1], sorted[0], sorted[2]}
	for _, dest := range []MoveDestination{{Position: 4}, {Before: &sorted[1]}, {After: &sorted[2]}} {
		want, err := PlanMove(sorted, sorted[3], dest)
		if err != nil {
			t.Fatalf("PlanMove sorted: %v", err)
		}
		got, err := PlanMove(shuffled, sorted[3], dest)
		if err != nil {
			t.Fatalf("PlanMove shuffled: %v", err)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("shuffled plan = %+v\nsorted plan   = %+v", got, want)
		}
		checkPlan(t, shuffled, got)
	}
}

func TestPlanMove_PositionMatchesTheServer(t *testing.T) {
	for name, rules := range moveTables {
		for _, rule := range rules {
			for want := 1; want <= 12; want++ {
				t.Run(fmt.Sprintf("%s/%s->%d", name, rule.InstanceName, want), func(t *testing.T) {
					plan, err := PlanMove(rules, rule, MoveDestination{Position: want})
					if err != nil {
						t.Fatalf("PlanMove: %v", err)
					}
					checkPlan(t, rules, plan)
					if plan.Clamped() != (want > plan.Last) {
						t.Errorf("Clamped() = %v for %d with last %d", plan.Clamped(), want, plan.Last)
					}
				})
			}
		}
	}
}

// before/after are statements about ORDER, so they are checked on the order
// the server produces: the moved rule must sit immediately next to its target,
// and nothing else may change relative order.
func TestPlanMove_BeforeAndAfterLandNextToTheTarget(t *testing.T) {
	for name, rules := range moveTables {
		for _, rule := range rules {
			for _, target := range rules {
				if target.ID == rule.ID {
					continue
				}
				for _, before := range []bool{true, false} {
					dest := MoveDestination{After: &target}
					flag := "after"
					if before {
						dest = MoveDestination{Before: &target}
						flag = "before"
					}
					t.Run(fmt.Sprintf("%s/%s-%s-%s", name, rule.InstanceName, flag, target.InstanceName), func(t *testing.T) {
						plan, err := PlanMove(rules, rule, dest)
						if err != nil {
							t.Fatalf("PlanMove: %v", err)
						}
						after := checkPlan(t, rules, plan)
						got := order(after)
						var expect []string
						for _, n := range order(rules) {
							switch n {
							case rule.InstanceName:
							case target.InstanceName:
								if before {
									expect = append(expect, rule.InstanceName, n)
								} else {
									expect = append(expect, n, rule.InstanceName)
								}
							default:
								expect = append(expect, n)
							}
						}
						if fmt.Sprint(got) != fmt.Sprint(expect) {
							t.Errorf("order = %v, want %v", got, expect)
						}
					})
				}
			}
		}
	}
}

// Already adjacent is unchanged even across a gap: renumbering the rule from 2
// to 3 next to a rule at 4 changes no order and would still print a move.
func TestPlanMove_AdjacentAcrossAGapIsUnchanged(t *testing.T) {
	rules := moveTables["gap-between"] // a=2 b=4 c=6 d=8
	plan, err := PlanMove(rules, rules[0], MoveDestination{Before: &rules[1]})
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	if !plan.Unchanged() {
		t.Errorf("a is already before b; plan moves it %d -> %d", plan.From, plan.To)
	}
	plan, err = PlanMove(rules, rules[2], MoveDestination{After: &rules[1]})
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	if !plan.Unchanged() {
		t.Errorf("c is already after b; plan moves it %d -> %d", plan.From, plan.To)
	}
}

func TestPlanMove_TargetIsTheRuleItselfIsBadUsage(t *testing.T) {
	rules := moveTables["contiguous"]
	for _, dest := range []MoveDestination{{Before: &rules[1]}, {After: &rules[1]}} {
		_, err := PlanMove(rules, rules[1], dest)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("exit code = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
		}
	}
}

func TestFindRuleInList(t *testing.T) {
	rules := []DatasetRuleTiny{
		{ID: uuid.MustParse("aaaa1111-0000-0000-0000-000000000001"), InstanceName: "email_check"},
		{ID: uuid.MustParse("aaaa2222-0000-0000-0000-000000000002"), InstanceName: "phone_check"},
		{ID: uuid.MustParse("bbbb1111-0000-0000-0000-000000000003"), InstanceName: "cafe"},
	}
	cases := []struct {
		input string
		want  string
		code  int
	}{
		{"email_check", "email_check", 0},
		{"aaaa2222", "phone_check", 0},
		{"AAAA2222-0000-0000-0000-000000000002", "phone_check", 0},
		// An exact name wins over a hex prefix it also happens to be.
		{"cafe", "cafe", 0},
		{"aaaa", "", qerrors.ExitBadUsage},
		// No substring fallback: a write must not pick a rule from a fragment.
		{"email", "", qerrors.ExitNotFound},
		// A full UUID that is not on this table is not found, not trusted.
		{"cccc1111-0000-0000-0000-000000000009", "", qerrors.ExitNotFound},
		{"", "", qerrors.ExitBadUsage},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := FindRuleInList(rules, tc.input)
			if tc.code != 0 {
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Errorf("exit code = %d (%v), want %d", code, err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatalf("FindRuleInList: %v", err)
			}
			if got.InstanceName != tc.want {
				t.Errorf("got %q, want %q", got.InstanceName, tc.want)
			}
		})
	}
}
