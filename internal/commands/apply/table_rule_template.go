package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/ruletemplate"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/rule_templates"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// templateFlags are the flags only a TableRuleTemplate document takes.
var templateFlags = []string{"table", "table-id", "resolutions"}

// refuseTemplateMisuse refuses, exit 2, a file holding a TableRuleTemplate
// beside any other document, and a flag of templateFlags on a file holding a
// document of another kind.
func refuseTemplateMisuse(cmd *cobra.Command, filePath string, docs []manifestDoc) error {
	for _, doc := range docs {
		if isTemplateDoc(doc) && len(docs) > 1 {
			return qerrors.NewBadUsagef("%s holds %d documents, and a %s must be the only document in its file: "+
				"applying it runs a rule merge, and a table takes one applying rule merge at a time; "+
				"apply each template from its own file, one after another; nothing was applied",
				filePath, len(docs), rule_templates.Kind)
		}
	}
	for _, name := range templateFlags {
		if !cmd.Flags().Changed(name) {
			continue
		}
		for _, doc := range docs {
			if !isTemplateDoc(doc) {
				return qerrors.NewBadUsagef("--%s applies only to kind %s, and %s holds %s; nothing was applied",
					name, rule_templates.Kind, filePath, docLabel(doc))
			}
		}
	}
	return nil
}

func isTemplateDoc(doc manifestDoc) bool {
	return pkgmanifest.CanonicalKind(doc.Kind) == rule_templates.Kind
}

// templateApplyOptions is a TableRuleTemplate apply's command line once every
// check that needs no request has passed.
type templateApplyOptions struct {
	file        *rule_templates.File
	path        string
	resolution  *rule_merges.RuleMergeResolution
	yes         bool
	wait        bool
	waitTimeout time.Duration
}

// readTemplateApplyOptions parses doc and the --resolutions document, checks
// that document's entries against the template's keys, and checks the table
// flags, the wait flags and --yes. Every failure but an unreadable
// --resolutions file exits 2.
func readTemplateApplyOptions(cmd *cobra.Command, doc manifestDoc) (templateApplyOptions, error) {
	opts := templateApplyOptions{path: doc.Path}
	var err error
	if opts.file, err = rule_templates.Parse(doc.Data, doc.Path); err != nil {
		return opts, err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return opts, err
	}
	if opts.waitTimeout, opts.wait, err = cmdutil.WaitFlags(cmd); err != nil {
		return opts, err
	}
	if cmd.Flags().Changed("resolutions") {
		path, _ := cmd.Flags().GetString("resolutions")
		text, err := cmdutil.ReadTextFile(cmd, path, "resolution")
		if err != nil {
			return opts, err
		}
		if opts.resolution, err = rule_merges.ParseResolution([]byte(text)); err != nil {
			return opts, fmt.Errorf("--resolutions %s: %w", path, err)
		}
		if err := opts.resolution.Spec.CheckTemplateKeys(opts.file.Template.Keys()); err != nil {
			return opts, fmt.Errorf("--resolutions %s: %w", path, err)
		}
	}
	opts.yes, _ = cmd.Flags().GetBool("yes")
	if !opts.yes && !cmdutil.CanPrompt() {
		return opts, qerrors.NewBadUsage("confirmation required but stdin is not an interactive terminal -- " +
			"pass --yes to apply the rule template, or run 'qctl diff -f' to preview it")
	}
	return opts, nil
}

// applyTableRuleTemplate previews the TableRuleTemplate doc on the table
// --table or --table-id names and applies the preview: recordLineage ends
// the apply when nothingToApply reports so, and the apply is refused, exit 2,
// while a conflict has no resolution or applyBlockers lists a blocker. With
// --wait it waits for the merge to settle, or for nothing but the review of a
// draft migration to be left. The result is the last merge read, as
// received.
func applyTableRuleTemplate(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	opts, err := readTemplateApplyOptions(cmd, doc)
	if err != nil {
		return nil, err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	preview, err := ruletemplate.NewPreview(ctx, cmd, opts.file)
	if err != nil {
		return nil, err
	}
	rulesClient, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	t := &templateApply{
		cmd: cmd, ctx: ctx, opts: opts, preview: preview,
		plan: ruletemplate.Classify(preview.Merge.Items), human: humanOut(cmd), rules: rulesClient,
	}
	return t.run()
}

// templateApply is one TableRuleTemplate apply after its preview.
type templateApply struct {
	cmd         *cobra.Command
	ctx         *cmdutil.CommandContext
	opts        templateApplyOptions
	preview     *ruletemplate.Preview
	plan        ruletemplate.Plan
	human       io.Writer
	rules       *dataset_rules.Client
	resolutions []rule_merges.Resolution
}

func (t *templateApply) run() (*applyResult, error) {
	merge := t.preview.Merge
	if t.nothingToApply() {
		fmt.Fprintf(t.human, "Nothing to do: %s (rule merge %d).\n", t.plan.Counts(), merge.ID)
		t.preview.WriteNextSteps(t.human, t.plan)
		return t.recordLineage()
	}
	if err := rule_merges.RenderTemplateMerge(t.human, merge, t.preview.Names); err != nil {
		return nil, err
	}
	t.preview.WriteNextSteps(t.human, t.plan)
	if t.opts.resolution != nil {
		var err error
		t.resolutions, err = t.opts.resolution.Spec.ToRequest(merge.Items, nil, t.tableRuleResolver())
		if err != nil {
			return nil, fmt.Errorf("--resolutions: %w", err)
		}
		if t.nothingToApply() {
			fmt.Fprintf(t.human, "\nNothing to do: --resolutions declines every item that would create a rule, "+
				"and none of them pairs with a table rule (rule merge %d).\n", merge.ID)
			return t.recordLineage()
		}
	}
	if err := t.refuseOpenDecisions(); err != nil {
		return nil, err
	}

	t.warnReprocessing(t.cmd.ErrOrStderr())
	if err := t.confirm(); err != nil {
		return nil, err
	}
	token := t.ctx.Credential.AccessToken
	applied, body, err := t.preview.Client.Apply(token, t.preview.TableID, merge.ID, t.resolutions)
	if err != nil {
		return nil, t.explainApplyError(err)
	}
	return t.report(applied, body)
}

// creatingItems is each item that applying the preview with t.resolutions
// creates a rule for: a created or replaced item, or a conflict, that no
// resolution declines with keep_target. A conflict with no resolution counts.
func (t *templateApply) creatingItems() []rule_merges.Item {
	declined := map[int]bool{}
	for _, r := range t.resolutions {
		declined[r.ItemID] = r.Choice == api.DatasetCopyConflictChoiceKeepTarget
	}
	var out []rule_merges.Item
	for _, item := range t.preview.Merge.Items {
		switch item.Action {
		case api.DatasetCopyItemActionCreated, api.DatasetCopyItemActionReplaced, api.DatasetCopyItemActionConflict:
			if !declined[item.ID] {
				out = append(out, item)
			}
		}
	}
	return out
}

// nothingToApply reports whether applying the preview with t.resolutions
// creates or replaces no rule and declines no item that pairs with a table
// rule.
func (t *templateApply) nothingToApply() bool {
	return len(t.creatingItems()) == 0 && !t.declinesAPairedItem()
}

// declinesAPairedItem reports whether a keep_target resolution of
// t.resolutions names a table rule or declines an item paired with one.
func (t *templateApply) declinesAPairedItem() bool {
	items := make(map[int]rule_merges.Item, len(t.preview.Merge.Items))
	for _, item := range t.preview.Merge.Items {
		items[item.ID] = item
	}
	for _, r := range t.resolutions {
		if r.Choice != api.DatasetCopyConflictChoiceKeepTarget {
			continue
		}
		if r.TargetRuleID != nil || items[r.ItemID].TargetObjectID != nil {
			return true
		}
	}
	return false
}

// nothingToDo is the preview as the apply's result.
func (t *templateApply) nothingToDo() (*applyResult, error) {
	return mergeResult(t.preview.Merge.ID, t.preview.Body), nil
}

// applyBlockers is the preview's apply blockers that refuse the apply with
// t.resolutions: each one, except draft_migration when no item of
// creatingItems has produced fields.
func (t *templateApply) applyBlockers() []api.RuleMergeApplyBlockerSchema {
	produces := slices.ContainsFunc(t.creatingItems(), func(item rule_merges.Item) bool {
		return len(item.RuleSnapshot.ProducedFields) > 0
	})
	var out []api.RuleMergeApplyBlockerSchema
	for _, blocker := range t.preview.Merge.ApplyBlockers {
		if blocker.Code == api.RuleMergeApplyBlockerCodeDraftMigration && !produces {
			continue
		}
		out = append(out, blocker)
	}
	return out
}

// tableRuleResolver resolves a --resolutions target_rule against the table's
// rules through cmdutil.FindTableRule.
func (t *templateApply) tableRuleResolver() rule_merges.RuleResolver {
	return func(ref string) (string, error) {
		rule, err := cmdutil.FindTableRule(t.ctx, t.preview.TableRules, ref, "")
		if err != nil {
			return "", err
		}
		return rule.ID.String(), nil
	}
}

// refuseOpenDecisions writes what each conflict with no resolution needs,
// and refuses, exit 2, while one is left or applyBlockers lists a blocker.
func (t *templateApply) refuseOpenDecisions() error {
	open := ruletemplate.Classify(rule_merges.UnresolvedConflicts(t.preview.Merge.Items, t.resolutions))
	t.preview.WriteMappingsToAdd(t.human, open, fmt.Sprintf("qctl diff -f %s %s --write-mappings",
		cmdutil.ShellArg(t.opts.path), t.preview.TableFlag()))
	t.preview.WriteResolutionSkeleton(t.human, open)

	var reasons []string
	if n := len(open.Unmapped) + len(open.Conflicts); n > 0 {
		keys := make([]string, 0, n)
		for _, item := range append(append([]rule_merges.Item{}, open.Unmapped...), open.Conflicts...) {
			keys = append(keys, templateItemKey(item))
		}
		reason := fmt.Sprintf("%d %s a decision: %s.", n, pluralWord(n, "conflict needs", "conflicts need"),
			strings.Join(keys, ", "))
		if len(open.Unmapped) > 0 {
			reason += fmt.Sprintf(" Set the column_mapping keys COLUMN MAPPINGS TO ADD lists in %s, "+
				"or decline the entry with keep_target in a --resolutions document.", t.opts.path)
		}
		if len(open.Conflicts) > 0 {
			reason += " Fill in the document UNRESOLVED CONFLICTS prints and pass it with --resolutions."
		}
		reasons = append(reasons, reason)
	}
	if blockers := t.applyBlockers(); len(blockers) > 0 {
		reasons = append(reasons, blockerReason(blockers, t.preview.TableID))
	}
	if len(reasons) == 0 {
		return nil
	}
	return qerrors.NewBadUsagef("nothing was applied: %s", terminal.Sanitize(strings.Join(reasons, "\n\n")))
}

// templateNonInteractive refuses a rule template apply's prompt on a stdin
// that is not a terminal.
const templateNonInteractive = "confirmation required but stdin is not an interactive terminal -- " +
	"pass --yes to apply the rule template"

// confirm asks before the apply unless --yes was given; a no is
// errDocumentDeclined.
func (t *templateApply) confirm() error {
	err := cmdutil.Confirm(t.cmd.ErrOrStderr(), t.opts.yes, templateNonInteractive, t.applyPhrase()+"?")
	if errors.Is(err, cmdutil.ErrDeclined) {
		fmt.Fprintf(t.human, "Skipping %s\n", t.opts.path)
		return errDocumentDeclined
	}
	return err
}

// applyPhrase says what the apply does: "Apply rule template monthly-orders
// to orders (id 42): 1 created, 1 replaced, 1 conflict resolved", with the
// created and replaced items a resolution declines last.
func (t *templateApply) applyPhrase() string {
	declines := map[int]bool{}
	for _, r := range t.resolutions {
		if r.Choice == api.DatasetCopyConflictChoiceKeepTarget {
			declines[r.ItemID] = true
		}
	}
	var created, replaced, declined int
	for _, item := range append(append([]rule_merges.Item{}, t.plan.Created...), t.plan.Replaced...) {
		switch {
		case declines[item.ID]:
			declined++
		case item.Action == api.DatasetCopyItemActionCreated:
			created++
		default:
			replaced++
		}
	}
	conflicts := len(t.plan.Unmapped) + len(t.plan.Conflicts)
	phrase := fmt.Sprintf("Apply rule template %s to %s: %d created, %d replaced, %d %s resolved",
		terminal.Sanitize(t.opts.file.Template.Metadata.Name), t.tableLabel(), created, replaced,
		conflicts, pluralWord(conflicts, "conflict", "conflicts"))
	if declined > 0 {
		phrase += fmt.Sprintf(", %d declined", declined)
	}
	return phrase
}

// tableLabel names the target table for a prompt.
func (t *templateApply) tableLabel() string {
	if t.preview.TableName == "" {
		return fmt.Sprintf("table %d", t.preview.TableID)
	}
	return fmt.Sprintf("%s (id %d)", terminal.Sanitize(t.preview.TableName), t.preview.TableID)
}

// report writes the accepted merge and, with --wait unless the 202 already
// settled it, waits. It then writes the summary and returns
// rule_merges.Verdict's error for a settled merge not applied in full.
func (t *templateApply) report(applied *rule_merges.RuleMerge, body []byte) (*applyResult, error) {
	tableID := t.preview.TableID
	describe := fmt.Sprintf("qctl describe rule-merge %d --table-id %d", applied.ID, tableID)
	fmt.Fprintf(t.human, "\nrule-merge/%d %s\n", applied.ID, terminal.Sanitize(string(applied.State)))

	final, finalBody := applied, body
	if t.opts.wait && !rule_merges.Settled(applied) {
		waited, waitedBody, err := t.preview.Client.WaitForApply(t.cmd, t.ctx.Credential.AccessToken, tableID, applied.ID,
			rule_merges.ApplyWait{Timeout: t.opts.waitTimeout, Describe: describe, Until: t.onlyMigrationReviewLeft})
		if waitedBody != nil {
			finalBody = waitedBody
		}
		if err != nil {
			return mergeResult(applied.ID, finalBody), err
		}
		final = waited
	}

	rules, err := t.rules.ListAllDatasetRules(t.ctx.Credential.AccessToken, tableID)
	if err != nil {
		fmt.Fprintf(t.cmd.ErrOrStderr(), "warning: failed to list the table's rules, so the summary has no rule states: %s\n",
			terminal.Sanitize(err.Error()))
	}
	t.writeSummary(final, rules)

	result := mergeResult(final.ID, finalBody)
	if rule_merges.Settled(final) {
		if verdict := rule_merges.Verdict(final); verdict != nil {
			return result, fmt.Errorf("%w\n\nNext: %s", verdict, describe)
		}
		return result, nil
	}
	if t.opts.wait {
		fmt.Fprintf(t.cmd.ErrOrStderr(), "--wait: stopped waiting: rule merge %d is still applying, and what it waits on "+
			"is the review of a draft migration, which only an approver ends\n", final.ID)
	}
	return result, nil
}

// onlyMigrationReviewLeft reports whether every created or replaced item of
// m whose new rule still waits for validation waits only on the review of a
// draft migration, and at least one does. ctx controls the listing of the
// table's rules.
func (t *templateApply) onlyMigrationReviewLeft(ctx context.Context, m *rule_merges.RuleMerge) (bool, error) {
	rules, err := t.rules.ListAllDatasetRulesContext(ctx, t.ctx.Credential.AccessToken, t.preview.TableID)
	if err != nil {
		return false, err
	}
	byID := tableRulesByID(rules)
	waiting := 0
	for _, item := range m.Items {
		rule, ok := newRuleOf(item, byID)
		if !ok || rule.State != api.DatasetRuleStatePendingValidation {
			continue
		}
		if !dataset_rules.AwaitsMigrationReview(rule) {
			return false, nil
		}
		waiting++
	}
	return waiting > 0, nil
}

// mergeResult is the apply result of merge id: body as received under -o
// json|yaml, rule-merge/<id> under -o name.
func mergeResult(id int, body []byte) *applyResult {
	var document any
	if raw, ok := rawJSONDocument(body); ok {
		document = raw
	}
	return namedApplyResult(document, "rule-merge", id)
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
