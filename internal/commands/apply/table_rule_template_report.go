package apply

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
)

// templateItemKey is item's template key, "-" without one.
func templateItemKey(item rule_merges.Item) string {
	if item.TemplateKey == nil || *item.TemplateKey == "" {
		return "-"
	}
	return *item.TemplateKey
}

// applyingMergePattern finds the merge id in atlas's refusal of an apply
// while another merge into the table is applying
// (rule_merge_controller.py _merge_applying_message).
var applyingMergePattern = regexp.MustCompile(`Rule merge (\d+) into this table is applying`)

// staleMergeText is in atlas's 409 refusing a rule template merge whose plan
// moved since the preview (rule_merge_controller.py
// _apply_loaded_rule_merge_async).
const staleMergeText = "changed since the preview"

// blockerReason says why the preview's apply blockers refuse the apply, with
// the next step for each.
func blockerReason(blockers []api.RuleMergeApplyBlockerSchema, tableID int) string {
	var b strings.Builder
	b.WriteString("the server refuses to apply this merge now (APPLY BLOCKERS):")
	for _, blocker := range blockers {
		fmt.Fprintf(&b, "\n  %s: %s", blocker.Code, oneLine(blocker.Message))
		switch blocker.Code {
		case api.RuleMergeApplyBlockerCodeMergeApplying:
			b.WriteString("\n    " + applyingMergeNext(blocker.Message, tableID))
		case api.RuleMergeApplyBlockerCodeDraftMigration:
			fmt.Fprintf(&b, "\n    Next: 'qctl get alerts --table-id %d --issue-type needs_review_migration' finds the "+
				"review alert that applies or discards the draft; run this again after it is resolved", tableID)
		}
	}
	return b.String()
}

// applyingMergeNext is the next step while another merge into tableID is
// applying, naming that merge when msg, atlas's message, does.
func applyingMergeNext(msg string, tableID int) string {
	if m := applyingMergePattern.FindStringSubmatch(msg); m != nil {
		return fmt.Sprintf("Next: a table takes one applying rule merge at a time; check it with "+
			"'qctl describe rule-merge %s --table-id %d' and run this again once it has settled", m[1], tableID)
	}
	return "Next: a table takes one applying rule merge at a time; run this again once the other merge has settled"
}

// explainApplyError adds the next step to an apply's 409 refusal while
// another merge is applying or after the plan moved since the preview. Any
// other error is returned unchanged.
func (t *templateApply) explainApplyError(err error) error {
	tableID := t.preview.TableID
	var apiErr *rule_merges.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		return err
	}
	msg := responseMessage(err)
	switch {
	case strings.Contains(msg, staleMergeText):
		return fmt.Errorf("%w\n\nNothing was applied. Run 'qctl apply -f %s %s' again: it previews the template afresh",
			err, cmdutil.ShellArg(t.opts.path), t.preview.TableFlag())
	case applyingMergePattern.MatchString(msg):
		return fmt.Errorf("%w\n\nNothing was applied. %s", err, applyingMergeNext(msg, tableID))
	}
	return err
}

// responseMessage is the server's message in err's *apierror.OperationalError,
// "" without one.
func responseMessage(err error) string {
	var opErr *apierror.OperationalError
	if !errors.As(err, &opErr) {
		return ""
	}
	return cmp.Or(opErr.ResponseMsg, opErr.Message)
}

// warnReprocessing writes, for each item the apply replaces a table rule
// with, the effects the preview lists for it.
func (t *templateApply) warnReprocessing(w io.Writer) {
	resolutions := make(map[int]rule_merges.Resolution, len(t.resolutions))
	for _, r := range t.resolutions {
		resolutions[r.ItemID] = r
	}
	var lines []string
	for _, item := range t.preview.Merge.Items {
		resolution, resolved := resolutions[item.ID]
		if !replacesATableRule(item, resolution, resolved) {
			continue
		}
		effects := make([]string, 0, len(item.Effects))
		for _, effect := range item.Effects {
			effects = append(effects, oneLine(apierror.ToPlainText(effect)))
		}
		if len(effects) == 0 {
			effects = append(effects, "the preview lists no effects for it")
		}
		lines = append(lines, fmt.Sprintf("  %s: %s\n", oneLine(templateItemKey(item)), strings.Join(effects, " ")))
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprint(w, "\nWarning: applying replaces table rules. When a replacement is enabled, the server reprocesses "+
		"the quarantined rows of the rule it replaces:\n")
	for _, line := range lines {
		fmt.Fprint(w, line)
	}
}

// replacesATableRule reports whether applying item, with resolution when
// resolved, replaces a table rule: a replaced item not declined, or a
// conflict whose choice takes the template's rule over a matched table rule.
func replacesATableRule(item rule_merges.Item, resolution rule_merges.Resolution, resolved bool) bool {
	switch item.Action {
	case api.DatasetCopyItemActionReplaced:
		return !resolved || resolution.Choice != api.DatasetCopyConflictChoiceKeepTarget
	case api.DatasetCopyItemActionConflict:
		if !resolved || (resolution.Choice != api.DatasetCopyConflictChoiceTakeSource &&
			resolution.Choice != api.DatasetCopyConflictChoiceEditSource) {
			return false
		}
		return item.TargetObjectID != nil || resolution.TargetRuleID != nil
	}
	return false
}

// tableRulesByID indexes rules by lower-case UUID.
func tableRulesByID(rules []dataset_rules.DatasetRuleTiny) map[string]dataset_rules.DatasetRuleTiny {
	byID := make(map[string]dataset_rules.DatasetRuleTiny, len(rules))
	for _, rule := range rules {
		byID[strings.ToLower(rule.ID.String())] = rule
	}
	return byID
}

// newRuleOf is the table rule an applied merge's created or replaced item
// made, and false for another item or a rule byID lacks.
func newRuleOf(item rule_merges.Item, byID map[string]dataset_rules.DatasetRuleTiny) (dataset_rules.DatasetRuleTiny, bool) {
	switch item.Action {
	case api.DatasetCopyItemActionCreated, api.DatasetCopyItemActionReplaced:
	default:
		return dataset_rules.DatasetRuleTiny{}, false
	}
	if item.TargetObjectID == nil {
		return dataset_rules.DatasetRuleTiny{}, false
	}
	rule, ok := byID[strings.ToLower(*item.TargetObjectID)]
	return rule, ok
}

// writeSummary writes the SUMMARY of merge m: one row per template entry's
// item with its table rule and that rule's state in rules, then the draft
// migrations the new rules wait on with their review alerts, and the
// follow-ups apply never runs.
func (t *templateApply) writeSummary(m *rule_merges.RuleMerge, rules []dataset_rules.DatasetRuleTiny) {
	byID := tableRulesByID(rules)
	var b bytes.Buffer
	fmt.Fprintf(&b, "\nSUMMARY (rule merge %d: %s)\n", m.ID, m.State)
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  KEY\tACTION\tTABLE RULE\tSTATE\tNOTE")
	replaced := false
	waits := migrationWaits{keys: map[int][]string{}}
	for _, item := range m.Items {
		if item.Reason != nil && *item.Reason == api.RuleMergeItemReasonNotInTemplate {
			continue
		}
		replaced = replaced || item.Action == api.DatasetCopyItemActionReplaced
		if rule, ok := newRuleOf(item, byID); ok && rule.PendingMigrationModelID != nil {
			waits.add(*rule.PendingMigrationModelID, templateItemKey(item))
		}
		fmt.Fprintln(tw, summaryRow(item, byID))
	}
	_ = tw.Flush()
	if replaced {
		b.WriteString("\nWhen a replacement is enabled, the server reprocesses the quarantined rows of the rule it " +
			"replaces; there is nothing to run for that.\n")
	}
	var followUps []breadcrumb
	if len(waits.ids) > 0 {
		b.WriteString("\nDRAFT MIGRATIONS (the new rules' columns wait on their review)\n")
		followUps = t.migrationFollowUps(&b, waits)
	}
	if m.State == api.DatasetCopyStateApplying {
		followUps = append(followUps, breadcrumb{
			fmt.Sprintf("qctl describe rule-merge %d --table-id %d", m.ID, t.preview.TableID), "what the merge still waits on",
		})
	}
	if len(followUps) > 0 {
		b.WriteString("\nFOLLOW-UPS (apply runs none of these)\n")
		renderBreadcrumbs(&b, followUps)
	}
	_, _ = io.WriteString(t.human, terminal.Sanitize(b.String()))
}

// summaryRow is item's tab-separated SUMMARY row: its key, action, table rule
// (named from byID when it holds the rule), that rule's state and the note.
func summaryRow(item rule_merges.Item, byID map[string]dataset_rules.DatasetRuleTiny) string {
	ruleCell, stateCell := "-", "-"
	if item.TargetObjectID != nil && *item.TargetObjectID != "" {
		ruleCell = rule_versions.ShortID(*item.TargetObjectID)
		if rule, ok := byID[strings.ToLower(*item.TargetObjectID)]; ok {
			ruleCell = fmt.Sprintf("%s (%s)", rule.InstanceName, ruleCell)
			stateCell = string(rule.State)
		}
	}
	note := "-"
	if item.Note != nil && *item.Note != "" {
		note = apierror.ToPlainText(*item.Note)
	}
	return fmt.Sprintf("  %s\t%s\t%s\t%s\t%s", oneLine(templateItemKey(item)), oneLine(string(item.Action)),
		oneLine(ruleCell), oneLine(stateCell), oneLine(note))
}

// migrationWaits is each draft migration new rules wait on, in first-seen
// order, with the template keys of those rules.
type migrationWaits struct {
	ids  []int
	keys map[int][]string
}

func (w *migrationWaits) add(migrationID int, key string) {
	if _, seen := w.keys[migrationID]; !seen {
		w.ids = append(w.ids, migrationID)
	}
	w.keys[migrationID] = append(w.keys[migrationID], key)
}

// migrationFollowUps writes one line per draft migration of waits with
// its review alert and the template keys whose rules wait on it, and returns
// the commands that resolve each review.
func (t *templateApply) migrationFollowUps(b *bytes.Buffer, waits migrationWaits) []breadcrumb {
	tableID := t.preview.TableID
	client := alerts.NewClient(t.ctx.ServerURL, t.ctx.OrganizationID, t.ctx.Verbosity)
	applyMigration := string(api.AlertActionApplyMigration)
	var followUps []breadcrumb
	lookup := false
	for _, migrationID := range waits.ids {
		keys := strings.Join(waits.keys[migrationID], ", ")
		alertID, found := client.FindMigrationReviewAlert(t.ctx.Credential.AccessToken, tableID, migrationID)
		if !found {
			fmt.Fprintf(b, "  draft migration %d: no unresolved needs_review_migration alert names it; waiting: %s\n",
				migrationID, oneLine(keys))
			lookup = true
			continue
		}
		fmt.Fprintf(b, "  draft migration %d: alert %d (needs_review_migration); waiting: %s\n", migrationID, alertID, oneLine(keys))
		followUps = append(followUps, breadcrumb{
			fmt.Sprintf("qctl alert resolve %d --action %s%s", alertID, applyMigration, alerts.YesFlag(applyMigration, cmdutil.CanPrompt())),
			"apply the table's WHOLE draft migration; the rules waiting on it need it",
		})
	}
	if lookup {
		followUps = append(followUps, breadcrumb{
			fmt.Sprintf("qctl get alerts --table-id %d --issue-type needs_review_migration", tableID), "find the review alert",
		})
	}
	return append(followUps, breadcrumb{
		fmt.Sprintf("qctl describe table-schema --table-id %d", tableID), "the draft carrying the columns",
	})
}
