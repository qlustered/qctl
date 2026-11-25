package ruletemplate

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/rule_templates"
	"github.com/qlustered/qctl/internal/terminal"
)

const notInTemplate = api.RuleMergeItemReasonNotInTemplate

// Plan is a template merge's items by what applying does with them.
type Plan struct {
	Created, Replaced, Kept, Skipped, Failed []rule_merges.Item
	// Unmapped are the conflicts of reason unmapped; Conflicts every other.
	Unmapped, Conflicts []rule_merges.Item
	// NotInTemplate are the table rules the template does not name.
	NotInTemplate []rule_merges.Item
}

// Classify sorts items into a Plan, keeping their order.
func Classify(items []rule_merges.Item) Plan {
	var p Plan
	for _, item := range items {
		if reasonOf(item) == notInTemplate {
			p.NotInTemplate = append(p.NotInTemplate, item)
			continue
		}
		switch item.Action {
		case api.DatasetCopyItemActionCreated:
			p.Created = append(p.Created, item)
		case api.DatasetCopyItemActionReplaced:
			p.Replaced = append(p.Replaced, item)
		case api.DatasetCopyItemActionKept:
			p.Kept = append(p.Kept, item)
		case api.DatasetCopyItemActionSkipped:
			p.Skipped = append(p.Skipped, item)
		case api.DatasetCopyItemActionFailed:
			p.Failed = append(p.Failed, item)
		case api.DatasetCopyItemActionConflict:
			if reasonOf(item) == api.RuleMergeItemReasonUnmapped {
				p.Unmapped = append(p.Unmapped, item)
			} else {
				p.Conflicts = append(p.Conflicts, item)
			}
		}
	}
	return p
}

// HasChanges reports an item applying creates or replaces a rule for.
func (p Plan) HasChanges() bool {
	return len(p.Created)+len(p.Replaced) > 0
}

// HasConflicts reports a conflict item.
func (p Plan) HasConflicts() bool {
	return len(p.Unmapped)+len(p.Conflicts) > 0
}

// Counts is "N kept, M skipped" style text naming every non-empty group of
// the template's entries.
func (p Plan) Counts() string {
	groups := []struct {
		n    int
		name string
	}{
		{len(p.Created), "created"}, {len(p.Replaced), "replaced"}, {len(p.Kept), "kept"},
		{len(p.Skipped), "skipped"}, {len(p.Unmapped) + len(p.Conflicts), "conflict"}, {len(p.Failed), "failed"},
	}
	var parts []string
	for _, g := range groups {
		if g.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", g.n, g.name))
		}
	}
	if len(parts) == 0 {
		return "no template entries"
	}
	return strings.Join(parts, ", ")
}

func reasonOf(item rule_merges.Item) api.RuleMergeItemReason {
	if item.Reason == nil {
		return ""
	}
	return *item.Reason
}

// itemKey is item's template key, "-" without one.
func itemKey(item rule_merges.Item) string {
	if item.TemplateKey == nil || *item.TemplateKey == "" {
		return "-"
	}
	return *item.TemplateKey
}

// uuidPattern matches a UUID in a note.
var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// NextStep is what a skipped item of a template merge on table tableID waits
// on, or the command that moves it on; "" for an item of another action or
// reason. entry is the item's template entry; nil falls back to its template
// key for the rule.
func NextStep(item rule_merges.Item, entry *rule_templates.Entry, tableID int) string {
	if item.Action != api.DatasetCopyItemActionSkipped {
		return ""
	}
	rule, release := itemKey(item), ""
	if entry != nil {
		rule, release = entry.Rule, entry.Release
	}
	ruleRelease := cmdutil.ShellArg(rule)
	if release != "" {
		ruleRelease += " --release " + cmdutil.ShellArg(release)
	}
	note := ""
	if item.Note != nil {
		note = *item.Note
	}
	switch reasonOf(item) {
	case api.RuleMergeItemReasonNotSubmitted:
		return "the release is a draft: request a review with 'qctl review request " + ruleRelease +
			"', or, where no review is required, enable it with 'qctl enable rule " + ruleRelease + "'"
	case api.RuleMergeItemReasonInReview:
		return "wait for its review to be approved ('qctl get rule-reviews --rule " + ruleRelease +
			"'); if none is open, request one with 'qctl review request " + ruleRelease + "'"
	case api.RuleMergeItemReasonNeedsTableReview:
		scope := ""
		if strings.Contains(note, "dataset_id=") {
			scope = fmt.Sprintf(" --table-id %d", tableID)
		}
		return "this table's review policy needs an approved review of the release: 'qctl review request " +
			ruleRelease + scope + "'"
	case api.RuleMergeItemReasonReleaseDisabled:
		return "the release is disabled: name an enabled release of " + cmdutil.ShellArg(rule) + " in the template"
	case api.RuleMergeItemReasonWaitingValidation:
		return "the table rule waits for validation; run this again once it has finished"
	case api.RuleMergeItemReasonUpgradeInProgress:
		return "the table rule's upgrade waits for validation; run this again once it has finished"
	case api.RuleMergeItemReasonSuggestionPending:
		if id := uuidPattern.FindString(note); id != "" {
			return fmt.Sprintf("a pending rule suggestion creates it: 'qctl suggestion accept %s --table-id %d' "+
				"(or dismiss it with 'qctl suggestion dismiss')", id, tableID)
		}
		return fmt.Sprintf("a pending rule suggestion creates it: accept or dismiss it "+
			"('qctl get rule-suggestions --table-id %d')", tableID)
	case api.RuleMergeItemReasonDeletedOnTable:
		return "deleted on the table; it stays deleted until its template entry changes"
	}
	return ""
}

// WriteNextSteps writes the NEXT STEPS section: one line per skipped item
// with its key, reason and next step. That step names the review alert of the
// draft migration the item's table rule waits on once its validation passed,
// else is NextStep, else the item's note. Nothing is written without a skipped
// item.
func (p *Preview) WriteNextSteps(w io.Writer, plan Plan) {
	if len(plan.Skipped) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\nNEXT STEPS (skipped items; applying leaves them alone)\n")
	for _, item := range plan.Skipped {
		step := NextStep(item, p.Entry(item), p.TableID)
		if review, ok := p.migrationReviews[item.ID]; ok {
			step = review.step(p.TableID)
		}
		if step == "" && item.Note != nil {
			step = apierror.ToPlainText(*item.Note)
		}
		label := itemKey(item)
		if reason := reasonOf(item); reason != "" {
			label += " (" + string(reason) + ")"
		}
		fmt.Fprintf(&b, "  %s: %s\n", oneLine(label), oneLine(step))
	}
	_, _ = io.WriteString(w, b.String())
}

// WriteMappingsToAdd writes the COLUMN MAPPINGS TO ADD section for the
// plan's unmapped items: the template lines rule_templates.Mappings finds,
// and the key and note of each unmapped item it finds none for. command is
// the command line that writes the lines into the file; "" leaves it out.
func (p *Preview) WriteMappingsToAdd(w io.Writer, plan Plan, command string) {
	if len(plan.Unmapped) == 0 {
		return
	}
	adds := rule_templates.Mappings(p.File, plan.Unmapped, p.Enriches)
	var b strings.Builder
	b.WriteString("\nCOLUMN MAPPINGS TO ADD\n")
	if len(adds) > 0 {
		fmt.Fprintf(&b, "Add these keys to the entries of %s and set each to a column of the table:\n", oneLine(p.File.Source))
		if command != "" {
			fmt.Fprintf(&b, "('%s' inserts them with empty values.)\n", oneLine(command))
		}
		b.WriteString("\n" + rule_templates.RenderMappings(adds))
	}
	writeUncovered(&b, plan.Unmapped, adds)
	_, _ = io.WriteString(w, b.String())
}

// WriteMappingsAdded writes what rule_templates.WriteMappings added to the
// template file.
func (p *Preview) WriteMappingsAdded(w io.Writer, plan Plan, adds []rule_templates.MappingsToAdd) {
	var b strings.Builder
	if len(adds) == 0 {
		fmt.Fprintf(&b, "\nNo column_mapping keys to add to %s.\n", oneLine(p.File.Source))
	} else {
		fmt.Fprintf(&b, "\nADDED TO %s\n", oneLine(p.File.Source))
		b.WriteString("Each value is empty: set it to a column of the table before apply.\n\n")
		b.WriteString(rule_templates.RenderMappings(adds))
	}
	writeUncovered(&b, plan.Unmapped, rule_templates.Mappings(p.File, plan.Unmapped, p.Enriches))
	_, _ = io.WriteString(w, b.String())
}

// writeUncovered writes the key and note of each unmapped item adds has no
// entry for: an unmapped item whose fix is not a column_mapping key to add.
func writeUncovered(b *strings.Builder, unmapped []rule_merges.Item, adds []rule_templates.MappingsToAdd) {
	covered := map[string]bool{}
	for _, add := range adds {
		key := add.Rule
		if add.InstanceName != nil {
			key = *add.InstanceName
		}
		covered[key] = true
	}
	var lines []string
	for _, item := range unmapped {
		if covered[itemKey(item)] {
			continue
		}
		note := "-"
		if item.Note != nil {
			note = apierror.ToPlainText(*item.Note)
		}
		lines = append(lines, fmt.Sprintf("  %s: %s\n", oneLine(itemKey(item)), oneLine(note)))
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("\nFix these entries by hand, as their notes say:\n")
	for _, line := range lines {
		b.WriteString(line)
	}
}

// WriteResolutionSkeleton writes the UNRESOLVED CONFLICTS section for the
// plan's conflicts other than unmapped: a fill-in RuleMergeResolution document
// for qctl apply --resolutions. Nothing is written without such a conflict.
func (p *Preview) WriteResolutionSkeleton(w io.Writer, plan Plan) {
	if len(plan.Conflicts) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nUNRESOLVED CONFLICTS\n%d %s a decision. Save the document below, set each choice,\n"+
		"and pass it with 'qctl apply -f %s %s --resolutions <file>'.\n\n",
		len(plan.Conflicts), plural(len(plan.Conflicts), "item needs", "items need"),
		cmdutil.ShellArg(p.File.Source), p.TableFlag())
	b.WriteString(rule_merges.TemplateSkeleton(plan.Conflicts, p.Names.TargetRule))
	_, _ = io.WriteString(w, terminal.Sanitize(b.String()))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// oneLine sanitizes s for a terminal and joins its lines with spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(terminal.Sanitize(s)), " ")
}
