package rule_merges //nolint:revive // Public package name matches the established resource domain.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// TemplateChoices returns the choices a rule template merge item takes, in
// DatasetCopyConflictChoice order: keep_target alone for a created or replaced
// item; for a conflict, edit_source and keep_target when its reason is
// unmapped, invalid_params or invalid_row_filter, keep_target alone when it is
// produced_column_collision or family_mismatch, and every choice but keep_both
// otherwise; none for any other action.
func TemplateChoices(item api.RuleMergeItemSchema) []api.DatasetCopyConflictChoice {
	switch item.Action {
	case api.DatasetCopyItemActionCreated, api.DatasetCopyItemActionReplaced:
		return []api.DatasetCopyConflictChoice{api.DatasetCopyConflictChoiceKeepTarget}
	case api.DatasetCopyItemActionConflict:
	default:
		return nil
	}
	var allowed func(api.DatasetCopyConflictChoice) bool
	switch itemReason(item) {
	case api.RuleMergeItemReasonUnmapped, api.RuleMergeItemReasonInvalidParams, api.RuleMergeItemReasonInvalidRowFilter:
		allowed = func(c api.DatasetCopyConflictChoice) bool {
			return c == api.DatasetCopyConflictChoiceEditSource || c == api.DatasetCopyConflictChoiceKeepTarget
		}
	case api.RuleMergeItemReasonProducedColumnCollision, api.RuleMergeItemReasonFamilyMismatch:
		allowed = func(c api.DatasetCopyConflictChoice) bool { return c == api.DatasetCopyConflictChoiceKeepTarget }
	default:
		allowed = func(c api.DatasetCopyConflictChoice) bool { return c != api.DatasetCopyConflictChoiceKeepBoth }
	}
	var out []api.DatasetCopyConflictChoice
	for _, choice := range api.EnumValues[api.DatasetCopyConflictChoice]() {
		if c := api.DatasetCopyConflictChoice(choice); allowed(c) {
			out = append(out, c)
		}
	}
	return out
}

// isTemplateItem reports an item of a rule template entry: no source rule and
// a template key.
func isTemplateItem(item api.RuleMergeItemSchema) bool {
	return item.SourceObjectID == nil && item.TemplateKey != nil
}

// templateItemFor finds the item whose template_key equals entry's rule,
// refusing, exit 2, a rule that is no item's key; i is the entry's index.
func templateItemFor(i int, entry RuleMergeResolutionEntry, items []api.RuleMergeItemSchema) (api.RuleMergeItemSchema, error) {
	var keys []string
	for _, item := range items {
		if item.TemplateKey == nil {
			continue
		}
		if *item.TemplateKey == entry.Rule {
			return item, nil
		}
		keys = append(keys, strconv.Quote(*item.TemplateKey))
	}
	if len(keys) == 0 {
		return api.RuleMergeItemSchema{}, qerrors.NewBadUsagef(
			"spec.resolutions[%d]: rule %q has no item in this merge: it has no rule template entries", i, entry.Rule)
	}
	return api.RuleMergeItemSchema{}, qerrors.NewBadUsagef(
		"spec.resolutions[%d]: rule %q is not the key of any rule template entry in this merge "+
			"(an entry's key is its instance_name, else its rule); the keys are %s",
		i, entry.Rule, strings.Join(keys, ", "))
}

// CheckTemplateKeys refuses, exit 2, the entries a rule template merge
// refuses whatever its preview plans: a keep_both choice, and a rule that is
// none of keys, the template's entry keys.
func (s RuleMergeResolutionSpec) CheckTemplateKeys(keys []string) error {
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = strconv.Quote(key)
	}
	for i, entry := range s.Resolutions {
		if !slices.Contains(keys, entry.Rule) {
			return qerrors.NewBadUsagef("spec.resolutions[%d]: rule %q is not the key of any entry of the rule template "+
				"(an entry's key is its instance_name, else its rule); the keys are %s",
				i, entry.Rule, strings.Join(quoted, ", "))
		}
		if entry.Choice == string(api.DatasetCopyConflictChoiceKeepBoth) {
			return qerrors.NewBadUsagef("spec.resolutions[%d] (rule %q): %s is not available for a rule template merge",
				i, entry.Rule, api.DatasetCopyConflictChoiceKeepBoth)
		}
	}
	return nil
}

// checkTemplateChoice refuses, exit 2, a choice a rule template merge's
// conflict item does not take.
func (e RuleMergeResolutionEntry) checkTemplateChoice(i int, item api.RuleMergeItemSchema) error {
	if item.Action != api.DatasetCopyItemActionConflict {
		return nil
	}
	allowed := TemplateChoices(item)
	if slices.Contains(allowed, api.DatasetCopyConflictChoice(e.Choice)) {
		return nil
	}
	names := choiceList(allowed)
	if e.Choice == string(api.DatasetCopyConflictChoiceKeepBoth) {
		return qerrors.NewBadUsagef("spec.resolutions[%d] (rule %q): %s is not available for a rule template merge; choose %s",
			i, e.Rule, api.DatasetCopyConflictChoiceKeepBoth, names)
	}
	return qerrors.NewBadUsagef("spec.resolutions[%d] (rule %q): its %s conflict takes only %s, not %s",
		i, e.Rule, itemReason(item), names, e.Choice)
}

// choiceList joins choices with ", ".
func choiceList(choices []api.DatasetCopyConflictChoice) string {
	parts := make([]string, len(choices))
	for i, c := range choices {
		parts[i] = string(c)
	}
	return strings.Join(parts, ", ")
}

// TemplateSkeleton renders a fill-in RuleMergeResolution document with one
// entry per conflict item of items whose reason is not unmapped: rule set to
// the item's template key, choice left empty, and for an ambiguous conflict an
// empty target_rule. Comments above an entry carry the item's reason and note,
// its matched table rule and its TemplateChoices; a target_rule's comment
// lists the candidates. targetRuleName names a table rule by UUID; nil or an
// empty name falls back to the UUID. Keys are written as YAML scalars, and
// every server string inside a comment goes through pkgmanifest.CommentSafe.
func TemplateSkeleton(items []api.RuleMergeItemSchema, targetRuleName func(id string) string) string {
	var b strings.Builder
	b.WriteString("# Set choice on every entry (and target_rule where there is one), then pass\n")
	b.WriteString("# this file to qctl apply --resolutions with the same rule template and table.\n")
	fmt.Fprintf(&b, "# %s also takes column_mapping (it replaces the planned mapping whole),\n",
		api.DatasetCopyConflictChoiceEditSource)
	b.WriteString("# params (display units), severity and row_filter (null removes it).\n")
	fmt.Fprintf(&b, "apiVersion: %s\nkind: %s\nspec:\n", pkgmanifest.APIVersionV1, RuleMergeResolutionKind)

	var conflicts []api.RuleMergeItemSchema
	for _, item := range items {
		if item.Action == api.DatasetCopyItemActionConflict && item.TemplateKey != nil &&
			itemReason(item) != api.RuleMergeItemReasonUnmapped {
			conflicts = append(conflicts, item)
		}
	}
	if len(conflicts) == 0 {
		b.WriteString("  resolutions: []\n")
		return b.String()
	}
	b.WriteString("  resolutions:\n")
	for _, item := range conflicts {
		if reason := itemReason(item); reason != "" {
			fmt.Fprintf(&b, "    # reason: %s\n", pkgmanifest.CommentSafe(string(reason)))
		}
		if item.Note != nil && *item.Note != "" {
			fmt.Fprintf(&b, "    # note: %s\n", pkgmanifest.CommentSafe(apierror.ToPlainText(*item.Note)))
		}
		if item.TargetObjectID != nil {
			fmt.Fprintf(&b, "    # matched table rule: %s\n",
				pkgmanifest.CommentSafe(skeletonRuleName(targetRuleName, *item.TargetObjectID)))
		}
		fmt.Fprintf(&b, "    # choice: one of %s\n", choiceList(TemplateChoices(item)))
		fmt.Fprintf(&b, "    - rule:%s\n", skeletonValue(*item.TemplateKey))
		b.WriteString("      choice:\n")
		if isAmbiguousConflict(item) {
			candidates := make([]string, len(item.CandidateObjectIds))
			for k, id := range item.CandidateObjectIds {
				candidates[k] = pkgmanifest.CommentSafe(skeletonScalar(skeletonRuleName(targetRuleName, id)))
			}
			fmt.Fprintf(&b, "      # target_rule: one of %s\n", strings.Join(candidates, ", "))
			b.WriteString("      target_rule:\n")
		}
	}
	return b.String()
}
