package rule_merges //nolint:revive // Match the established package under test.

import (
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// templateTestItem is a rule template merge item: no source rule, a key.
func templateTestItem(id int, action api.DatasetCopyItemAction, key string, reason api.RuleMergeItemReason) api.RuleMergeItemSchema {
	item := api.RuleMergeItemSchema{ID: id, Action: action, TemplateKey: resolutionTestStr(key)}
	if reason != "" {
		item.Reason = &reason
	}
	return item
}

// templateTestItems is a rule template merge with one item per reason that
// restricts the choices, an unrestricted conflict, an ambiguous match, a
// created and a skipped item, and a table rule not in the template.
func templateTestItems() []api.RuleMergeItemSchema {
	changed := templateTestItem(509, api.DatasetCopyItemActionConflict, "Changed Both", api.RuleMergeItemReasonChangedBoth)
	changed.TargetObjectID = resolutionTestStr(resolutionTestTgtMatched)
	changed.Note = resolutionTestStr("It changed in both the template and the table.")
	ambiguous := templateTestItem(510, api.DatasetCopyItemActionConflict, "dup-rule", api.RuleMergeItemReasonAmbiguousMatch)
	ambiguous.CandidateObjectIds = []string{resolutionTestTgtCandA, resolutionTestTgtCandB}
	family := templateTestItem(508, api.DatasetCopyItemActionConflict, "Family", api.RuleMergeItemReasonFamilyMismatch)
	family.TargetObjectID = resolutionTestStr(resolutionTestTgtMatched)
	unmanaged := api.RuleMergeItemSchema{
		ID: 512, Action: api.DatasetCopyItemActionSkipped, TargetObjectID: resolutionTestStr(resolutionTestTgtCandB),
	}
	reason := api.RuleMergeItemReasonNotInTemplate
	unmanaged.Reason = &reason
	return []api.RuleMergeItemSchema{
		templateTestItem(501, api.DatasetCopyItemActionCreated, "Order Total Check", ""),
		templateTestItem(502, api.DatasetCopyItemActionConflict, "split-rule", api.RuleMergeItemReasonUnmapped),
		templateTestItem(503, api.DatasetCopyItemActionConflict, "Params", api.RuleMergeItemReasonInvalidParams),
		templateTestItem(504, api.DatasetCopyItemActionConflict, "Filter", api.RuleMergeItemReasonInvalidRowFilter),
		templateTestItem(505, api.DatasetCopyItemActionConflict, "Produces", api.RuleMergeItemReasonProducedColumnCollision),
		templateTestItem(506, api.DatasetCopyItemActionSkipped, "in-review-rule", api.RuleMergeItemReasonInReview),
		templateTestItem(507, api.DatasetCopyItemActionReplaced, "Upgrade", ""),
		family,
		changed,
		ambiguous,
		unmanaged,
	}
}

func templateTestToRequest(entries ...RuleMergeResolutionEntry) ([]Resolution, error) {
	spec := RuleMergeResolutionSpec{Resolutions: entries}
	return spec.ToRequest(templateTestItems(), nil, resolutionTestResolver(resolutionTestTargetNames))
}

func TestTemplateChoices(t *testing.T) {
	want := map[int][]api.DatasetCopyConflictChoice{
		501: {api.DatasetCopyConflictChoiceKeepTarget},
		502: {api.DatasetCopyConflictChoiceKeepTarget, api.DatasetCopyConflictChoiceEditSource},
		503: {api.DatasetCopyConflictChoiceKeepTarget, api.DatasetCopyConflictChoiceEditSource},
		504: {api.DatasetCopyConflictChoiceKeepTarget, api.DatasetCopyConflictChoiceEditSource},
		505: {api.DatasetCopyConflictChoiceKeepTarget},
		506: nil,
		507: {api.DatasetCopyConflictChoiceKeepTarget},
		508: {api.DatasetCopyConflictChoiceKeepTarget},
		509: {api.DatasetCopyConflictChoiceTakeSource, api.DatasetCopyConflictChoiceKeepTarget, api.DatasetCopyConflictChoiceEditSource},
		510: {api.DatasetCopyConflictChoiceTakeSource, api.DatasetCopyConflictChoiceKeepTarget, api.DatasetCopyConflictChoiceEditSource},
		512: nil,
	}
	for _, item := range templateTestItems() {
		if got := TemplateChoices(item); !slices.Equal(got, want[item.ID]) {
			t.Errorf("item %d: choices %v, want %v", item.ID, got, want[item.ID])
		}
	}
}

func TestToRequestMatchesATemplateItemByItsKey(t *testing.T) {
	req, err := templateTestToRequest(
		RuleMergeResolutionEntry{Rule: "split-rule", Choice: "edit_source", ColumnMapping: map[string]string{"Purchase Time": "purchased_at"}},
		RuleMergeResolutionEntry{Rule: "Changed Both", Choice: "take_source"},
		RuleMergeResolutionEntry{Rule: "dup-rule", Choice: "keep_target", TargetRule: "range_a"},
		RuleMergeResolutionEntry{Rule: "Order Total Check", Choice: "keep_target"},
		RuleMergeResolutionEntry{Rule: "Produces", Choice: "keep_target"},
		RuleMergeResolutionEntry{Rule: "Params", Choice: "keep_target"},
	)
	if err != nil {
		t.Fatalf("ToRequest: %v", err)
	}
	var ids []int
	for _, r := range req {
		ids = append(ids, r.ItemID)
	}
	if want := []int{502, 509, 510, 501, 505, 503}; !slices.Equal(ids, want) {
		t.Fatalf("item ids = %v, want %v", ids, want)
	}
	if req[0].ColumnMapping == nil || (*req[0].ColumnMapping)["Purchase Time"] != "purchased_at" {
		t.Errorf("column_mapping = %v", req[0].ColumnMapping)
	}
	if req[2].TargetRuleID == nil || req[2].TargetRuleID.String() != resolutionTestTgtCandA {
		t.Errorf("target_rule_id = %v, want %s", req[2].TargetRuleID, resolutionTestTgtCandA)
	}
}

func TestToRequestRefusesForATemplateMerge(t *testing.T) {
	entry := func(rule, choice, target string) RuleMergeResolutionEntry {
		return RuleMergeResolutionEntry{Rule: rule, Choice: choice, TargetRule: target}
	}
	for name, e := range map[string]RuleMergeResolutionEntry{
		"an unknown key":                          entry("no-such-entry", "keep_target", ""),
		"a key in another case":                   entry("order total check", "keep_target", ""),
		"a table rule not in the template":        entry(resolutionTestTgtCandB, "keep_target", ""),
		"keep_both on a conflict":                 entry("Changed Both", "keep_both", ""),
		"keep_both on an ambiguous match":         entry("dup-rule", "keep_both", ""),
		"keep_both on a created item":             entry("Order Total Check", "keep_both", ""),
		"take_source on an unmapped conflict":     entry("split-rule", "take_source", ""),
		"take_source on invalid params":           entry("Params", "take_source", ""),
		"take_source on an invalid row filter":    entry("Filter", "take_source", ""),
		"edit_source on a produced column":        entry("Produces", "edit_source", ""),
		"take_source on a family mismatch":        entry("Family", "take_source", ""),
		"edit_source on a family mismatch":        entry("Family", "edit_source", ""),
		"take_source on a created item":           entry("Order Total Check", "take_source", ""),
		"a skipped item":                          entry("in-review-rule", "keep_target", ""),
		"an ambiguous match without target_rule":  entry("dup-rule", "take_source", ""),
		"target_rule on a single-match conflict":  entry("Changed Both", "keep_target", "range_a"),
		"target_rule that is not among the cands": entry("dup-rule", "keep_target", "price_range_t"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := templateTestToRequest(e)
			resolutionTestWantBadUsage(t, err)
		})
	}
}

func TestToRequestTemplateRefusalNamesTheEntryAndItsChoices(t *testing.T) {
	_, err := templateTestToRequest(
		RuleMergeResolutionEntry{Rule: "Order Total Check", Choice: "keep_target"},
		RuleMergeResolutionEntry{Rule: "split-rule", Choice: "take_source"},
	)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	for _, want := range []string{"spec.resolutions[1]", `"split-rule"`, "unmapped", "keep_target, edit_source"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

// A dataset merge resolves its entries through the source resolver, not by
// template key, even when a template item is among the items.
func TestToRequestWithASourceResolverIgnoresTemplateKeys(t *testing.T) {
	spec := RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{{Rule: "Changed Both", Choice: "take_source"}}}
	_, err := spec.ToRequest(templateTestItems(),
		resolutionTestResolver(resolutionTestSourceNames), resolutionTestResolver(resolutionTestTargetNames))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want the source resolver's %d (err: %v)", code, qerrors.ExitNotFound, err)
	}
}

func TestTemplateSkeletonListsNonUnmappedConflictsByKey(t *testing.T) {
	skeleton := TemplateSkeleton(templateTestItems(), resolutionTestNamer(resolutionTestTargetNames))
	if got := Skeleton(templateTestItems(), nil, resolutionTestNamer(resolutionTestTargetNames)); got != skeleton {
		t.Errorf("Skeleton does not hand a template merge to TemplateSkeleton:\n%s", got)
	}
	for _, want := range []string{
		"    - rule: Params\n",
		"    # reason: invalid_params\n    # choice: one of keep_target, edit_source\n    - rule: Params\n",
		"    # choice: one of keep_target\n    - rule: Produces\n",
		"    # matched table rule: price_range_t\n    # choice: one of keep_target\n    - rule: Family\n",
		"    # note: It changed in both the template and the table.\n",
		"    # choice: one of take_source, keep_target, edit_source\n    - rule: Changed Both\n",
		"      # target_rule: one of range_a, 'range b: x'\n      target_rule:\n",
	} {
		if !strings.Contains(skeleton, want) {
			t.Errorf("skeleton lacks %q:\n%s", want, skeleton)
		}
	}
	for _, absent := range []string{"split-rule", "Order Total Check", "Upgrade", "in-review-rule", "keep_both"} {
		if strings.Contains(skeleton, absent) {
			t.Errorf("skeleton names %s:\n%s", absent, skeleton)
		}
	}
}

func TestTemplateSkeletonFilledInParsesAndMaps(t *testing.T) {
	skeleton := TemplateSkeleton(templateTestItems(), resolutionTestNamer(resolutionTestTargetNames))
	filled := resolutionTestFill(t, skeleton, "choice",
		"keep_target", "keep_target", "keep_target", "keep_target", "take_source", "edit_source")
	filled = resolutionTestFill(t, filled, "target_rule", "range_a")
	doc, err := ParseResolution([]byte(filled))
	if err != nil {
		t.Fatalf("ParseResolution: %v\n%s", err, filled)
	}
	req, err := doc.Spec.ToRequest(templateTestItems(), nil, resolutionTestResolver(resolutionTestTargetNames))
	if err != nil {
		t.Fatalf("ToRequest: %v\n%s", err, filled)
	}
	var ids []int
	for _, r := range req {
		ids = append(ids, r.ItemID)
	}
	if want := []int{503, 504, 505, 508, 509, 510}; !slices.Equal(ids, want) {
		t.Errorf("item ids = %v, want %v", ids, want)
	}
	left := UnresolvedConflicts(templateTestItems(), req)
	if len(left) != 1 || left[0].ID != 502 {
		t.Errorf("unresolved = %v, want only the unmapped item 502", left)
	}
}

func TestTemplateSkeletonWithOnlyUnmappedConflicts(t *testing.T) {
	items := []api.RuleMergeItemSchema{templateTestItem(502, api.DatasetCopyItemActionConflict, "split-rule", api.RuleMergeItemReasonUnmapped)}
	if skeleton := TemplateSkeleton(items, nil); !strings.HasSuffix(skeleton, "  resolutions: []\n") {
		t.Errorf("skeleton:\n%s", skeleton)
	}
}

// CheckTemplateKeys refuses keep_both and a rule that is no entry key, from
// the template's keys alone, and passes every other entry to ToRequest.
func TestCheckTemplateKeys(t *testing.T) {
	keys := []string{"Order Total Check", "split-rule", "Changed Both"}
	entry := func(rule, choice string) RuleMergeResolutionEntry {
		return RuleMergeResolutionEntry{Rule: rule, Choice: choice}
	}
	for name, e := range map[string]RuleMergeResolutionEntry{
		"an unknown key":        entry("no-such-entry", "keep_target"),
		"a key in another case": entry("order total check", "keep_target"),
		"keep_both":             entry("Changed Both", "keep_both"),
	} {
		t.Run(name, func(t *testing.T) {
			spec := RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{entry("split-rule", "keep_target"), e}}
			resolutionTestWantBadUsage(t, spec.CheckTemplateKeys(keys))
		})
	}
	spec := RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{
		entry("Order Total Check", "keep_target"), entry("split-rule", "edit_source"), entry("Changed Both", "take_source"),
	}}
	if err := spec.CheckTemplateKeys(keys); err != nil {
		t.Errorf("CheckTemplateKeys = %v, want nil", err)
	}
}
