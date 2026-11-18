package merge

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/spf13/cobra"
)

// NewTableRulesCommand creates the 'merge table-rules' command.
func NewTableRulesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-rules [table]",
		Short: "Merge another table's rules into a table",
		Long: `Merge the rules of one table (the source) into another (the target). The
source table is not changed. On the target, rules are added, replaced or kept,
and a rule whose produced fields add columns proposes them in a draft migration
of the target's schema.

The target is the positional id-or-name, --table <name> or --table-id <id>.
The source is --from <name> or --from-id <id>, exactly one of them; --from is
always a name, even one that looks like a number. The two must be different
tables.

Every run makes a NEW server-side preview: the server plans the merge, stores
the plan as a rule merge, and the command prints it, one item per source rule
with what applying does to it:

  created    the target has no matching rule; applying adds it
  replaced   applying replaces the target's rule with the source's
  kept       applying leaves it alone
  skipped    applying leaves it alone; the NOTE says why, or, after an
             apply, the CHOICE of an item an entry declined
  conflict   needs a resolution before anything is applied

After an apply an item can also be failed, with the reason in its NOTE.

Resolutions come from -f, a RuleMergeResolution document. Because each run
previews afresh, item ids differ from run to run, so an entry names the SOURCE
table rule -- by name, SHORT-ID, r- alias or UUID -- never the item:

  apiVersion: qluster.ai/v1
  kind: RuleMergeResolution
  spec:
    resolutions:
      - rule: price_range
        choice: take_source
      - rule: zip_check
        choice: keep_target
        target_rule: zip_check_eu
      - rule: email_check
        choice: keep_target

The choices for a conflict:

  take_source  the source rule is applied, replacing the target's rule
               when the conflict pairs it with one
  keep_target  the target's rule stays and nothing is added
  edit_source  as take_source, with the entry's column_mapping, params,
               severity and row_filter replacing the source rule's
  keep_both    the source rule is added beside the target's rule

Every created and replaced item is applied unless an entry declines it with
keep_target, the only choice it takes. kept and skipped items take no entry.
target_rule goes only on a conflict the plan shows as ambiguous (TARGET RULE
reads 'ambiguous (N candidates)', even for one) and names one of its
candidates, a TARGET table rule. Every choice there but keep_both needs it,
and keep_both refuses it.

edit_source params are in DISPLAY units, while the params in the plan's
snapshots are the STORED values; the two differ for a percent column, so do
not copy snapshot params into an entry unchanged. An edit_source row_filter
takes the shape of a TableRule's spec.row_filter, description required, and
names the TARGET table's columns; row_filter: null removes the source rule's
filter, and leaving the key out keeps it.

--dry-run prints the plan and stops (exit 0); with -f it also checks each
entry against the plan and refuses choices that would replace a target rule
waiting for validation or replace one target rule twice. When a
conflict has no entry, a fill-in RuleMergeResolution document follows the plan:
copy it into a file, set each choice (and target_rule where it asks for one),
and pass it back with -f. Without --dry-run, a conflict with no entry then
exits 2 before anything is applied.

Applying asks for confirmation first; --yes skips the prompt and is required
whenever stdin is not an interactive terminal, which is checked before the
server stores a preview. The apply is asynchronous: the
server accepts it as applying and finishes in the background, and a
replacement rule created pending validation retires the target's rule only
once it is enabled. --wait polls until the merge settles: applied exits 0, and
partially_applied or failed exits 1. Running out of --wait-timeout exits 124
and leaves the merge applying, as Ctrl-C during the wait does (exit 130).

A merge can stay applying for a long time -- waiting for a draft migration of
the target's schema to be reviewed, or for the target to get an active data
source -- and while it does, applying any other rule merge into that table is
refused (409). The item notes --wait prints include what it is waiting for,
and 'qctl describe rule-merge <id> --table-id <target-id>' reads it back.

The plan and outcome go to stdout, or to stderr under -o json, yaml or name.
-o json and -o yaml print one document as the server sent it: the preview with
--dry-run, the accepted merge after an apply, or the last poll with --wait.
-o name prints rule-merge/<id>. Prompts and --wait progress go to stderr.

Examples:
  # Preview merging orders_eu's rules into orders
  qctl merge table-rules orders --from orders_eu --dry-run

  # Check a resolution document against a fresh preview
  qctl merge table-rules orders --from orders_eu -f resolutions.yaml --dry-run

  # Apply with it
  qctl merge table-rules orders --from orders_eu -f resolutions.yaml

  # By table id, without a prompt, waiting for the outcome
  qctl merge table-rules --table-id 42 --from-id 12 -f resolutions.yaml --yes --wait`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("rule-merge"), "table", "json", "yaml", "name"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runMergeTableRules,
	}

	cmdutil.AddTableFlags(cmd, "Target table")
	cmd.Flags().String("from", "", "Source table name (exact, case-sensitive; always a name, never an ID)")
	cmdutil.RefInt(cmd.Flags(), "from-id", "table", "Source table ID")
	cmd.Flags().StringP("filename", "f", "", `Path to a RuleMergeResolution document ("-" reads stdin)`)
	cmd.Flags().Bool("dry-run", false, "Preview the merge and stop; with a conflict left open, print a fill-in -f document")
	cmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	cmdutil.AddWaitFlags(cmd, "the merge to finish applying")
	return cmd
}

// tableRulesOptions is the command line of 'merge table-rules' once every
// check that needs no request has passed.
type tableRulesOptions struct {
	positional  string
	fromName    string
	fromID      int
	resolution  *rule_merges.RuleMergeResolution
	dryRun      bool
	yes         bool
	wait        bool
	waitTimeout time.Duration
}

// readTableRulesOptions validates the flags, arguments and -f document. Every
// failure but an unreadable -f file exits 2.
func readTableRulesOptions(cmd *cobra.Command, args []string) (tableRulesOptions, error) {
	var opts tableRulesOptions
	if len(args) > 0 {
		opts.positional = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, opts.positional); err != nil {
		return opts, err
	}
	if err := readMergeSourceFlags(cmd, &opts); err != nil {
		return opts, err
	}
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"dry-run", "yes"}, []string{"dry-run", "wait"}); err != nil {
		return opts, err
	}
	var err error
	if opts.waitTimeout, opts.wait, err = cmdutil.WaitFlags(cmd); err != nil {
		return opts, err
	}
	opts.dryRun, _ = cmd.Flags().GetBool("dry-run")
	opts.yes, _ = cmd.Flags().GetBool("yes")

	if cmd.Flags().Changed("filename") {
		path, _ := cmd.Flags().GetString("filename")
		text, err := cmdutil.ReadTextFile(cmd, path, "resolution")
		if err != nil {
			return opts, err
		}
		doc, err := rule_merges.ParseResolution([]byte(text))
		if err != nil {
			return opts, fmt.Errorf("-f %s: %w", path, err)
		}
		opts.resolution = doc
	}
	return opts, nil
}

// readMergeSourceFlags reads --from and --from-id into opts: exactly one, a
// non-empty name or a positive id.
func readMergeSourceFlags(cmd *cobra.Command, opts *tableRulesOptions) error {
	haveName := cmd.Flags().Changed("from")
	fromID, haveID, err := cmdutil.PositiveIDFlag(cmd, "from-id")
	if err != nil {
		return err
	}
	switch {
	case haveName && haveID:
		return qerrors.NewBadUsage("source table specified more than once (--from, --from-id): use exactly one")
	case !haveName && !haveID:
		return qerrors.NewBadUsage("source table is required: pass --from <name> or --from-id <id>")
	}
	if haveName {
		opts.fromName, _ = cmd.Flags().GetString("from")
		if opts.fromName == "" {
			return qerrors.NewBadUsage("--from cannot be empty: name the source table, or use --from-id <id>")
		}
	}
	opts.fromID = fromID
	return nil
}

// mergeTables is the target and source of a merge once resolved; a name is ""
// when the command line gave only an id.
type mergeTables struct {
	targetID, sourceID     int
	targetName, sourceName string
}

// targetLabel names the target table for a prompt.
func (t mergeTables) targetLabel() string {
	if t.targetName == "" {
		return fmt.Sprintf("table %d", t.targetID)
	}
	return fmt.Sprintf("%s (id %d)", t.targetName, t.targetID)
}

// resolveMergeTables resolves the target through the table selector and a
// --from name through the exact-name lookup --table uses, refusing (exit 2) a
// source that is the target.
func resolveMergeTables(ctx *cmdutil.CommandContext, cmd *cobra.Command, opts tableRulesOptions) (mergeTables, error) {
	var t mergeTables
	var err error
	if t.targetID, err = cmdutil.ResolveTableID(ctx, cmd, opts.positional); err != nil {
		return t, err
	}
	t.targetName = mergeTargetName(cmd, opts.positional)
	t.sourceID = opts.fromID
	if opts.fromName != "" {
		source, err := cmdutil.ResolveTableByName(ctx, opts.fromName, "--from-id")
		if err != nil {
			return t, fmt.Errorf("--from: %w", err)
		}
		t.sourceID, t.sourceName = source.ID, source.Name
	}
	if t.sourceID == t.targetID {
		return t, qerrors.NewBadUsagef(
			"the source and target are the same table (id %d): name another table with --from or --from-id", t.targetID)
	}
	return t, nil
}

// mergeTargetName is the target's name as the command line gave it: --table,
// or a positional that is not an integer; "" otherwise.
func mergeTargetName(cmd *cobra.Command, positional string) string {
	if name, _ := cmd.Flags().GetString("table"); name != "" {
		return name
	}
	if _, err := strconv.Atoi(positional); positional != "" && err != nil {
		return positional
	}
	return ""
}

func runMergeTableRules(cmd *cobra.Command, args []string) error {
	opts, err := readTableRulesOptions(cmd, args)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tables, err := resolveMergeTables(ctx, cmd, opts)
	if err != nil {
		return err
	}
	if !opts.dryRun && !opts.yes && !cmdutil.CanPrompt() {
		return qerrors.NewBadUsage("confirmation required but stdin is not an interactive terminal -- " +
			"pass --yes to apply the merge, or --dry-run to preview it")
	}
	client, err := rule_merges.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	plan, err := previewMergePlan(ctx, client, tables, opts.resolution)
	if err != nil {
		return err
	}
	out := newMergeOutput(cmd)
	if err := checkMergePlanBeforeApply(plan, opts); err != nil {
		return err
	}
	unresolved, err := writeMergePlan(out, plan, opts)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return out.writeDocument(plan.merge.ID, plan.body)
	}
	if len(unresolved) > 0 {
		return unresolvedConflictsError(unresolved, plan.names)
	}

	phrase := mergeApplyPhrase(plan.merge, plan.resolutions, tables)
	nonInteractive := "confirmation required but stdin is not an interactive terminal -- pass --yes to " + phrase
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), opts.yes, nonInteractive, capitalize(phrase)+"?"); err != nil {
		return err
	}
	return applyMergePlan(cmd, out, client, ctx.Credential.AccessToken, tables, plan, opts)
}

// mergePlan is a fresh preview, its body as received, the -f document mapped
// onto it (nil without one), and the names to render it with.
type mergePlan struct {
	merge       *rule_merges.RuleMerge
	body        []byte
	resolutions []rule_merges.Resolution
	names       rule_merges.RenderNames
	targetRules []dataset_rules.DatasetRuleTiny
}

// previewMergePlan previews the merge, lists both tables' rules, and maps doc
// onto the preview's items when it is not nil.
func previewMergePlan(ctx *cmdutil.CommandContext, client *rule_merges.Client, tables mergeTables,
	doc *rule_merges.RuleMergeResolution,
) (mergePlan, error) {
	var plan mergePlan
	var err error
	plan.merge, plan.body, err = client.Preview(ctx.Credential.AccessToken, tables.targetID, tables.sourceID)
	if err != nil {
		return plan, err
	}
	rules, err := fetchMergeRules(ctx, tables)
	if err != nil {
		return plan, err
	}
	plan.names = rules.renderNames(tables)
	plan.targetRules = rules.target
	if doc != nil {
		plan.resolutions, err = doc.Spec.ToRequest(plan.merge.Items,
			mergeRuleResolver(ctx, rules.source), mergeRuleResolver(ctx, rules.target))
		if err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// checkMergePlanBeforeApply checks replacements on a complete apply plan.
func checkMergePlanBeforeApply(plan mergePlan, opts tableRulesOptions) error {
	if opts.dryRun && opts.resolution == nil {
		return nil
	}
	if len(rule_merges.UnresolvedConflicts(plan.merge.Items, plan.resolutions)) > 0 {
		return nil
	}
	return validateMergeReplacements(plan)
}

// replacementTargetID returns the target rule an item would retire.
func replacementTargetID(item api.RuleMergeItemSchema, resolution rule_merges.Resolution, hasResolution bool) string {
	switch item.Action {
	case api.DatasetCopyItemActionReplaced:
		if hasResolution && resolution.Choice == api.DatasetCopyConflictChoiceKeepTarget {
			return ""
		}
	case api.DatasetCopyItemActionConflict:
		if !hasResolution || resolution.Choice == api.DatasetCopyConflictChoiceKeepTarget || resolution.Choice == api.DatasetCopyConflictChoiceKeepBoth {
			return ""
		}
	default:
		return ""
	}
	if resolution.TargetRuleID != nil {
		return resolution.TargetRuleID.String()
	}
	if item.TargetObjectID != nil {
		return *item.TargetObjectID
	}
	return ""
}

// validateMergeReplacements checks the target rule each applying item retires.
func validateMergeReplacements(plan mergePlan) error {
	resolutions := make(map[int]rule_merges.Resolution, len(plan.resolutions))
	for _, resolution := range plan.resolutions {
		resolutions[resolution.ItemID] = resolution
	}
	targets := make(map[uuid.UUID]dataset_rules.DatasetRuleTiny, len(plan.targetRules))
	for _, rule := range plan.targetRules {
		targets[rule.ID] = rule
	}
	replaced := make(map[uuid.UUID]int)
	for _, item := range plan.merge.Items {
		resolution, hasResolution := resolutions[item.ID]
		targetID := replacementTargetID(item, resolution, hasResolution)
		if targetID == "" {
			continue
		}
		id, err := uuid.Parse(targetID)
		if err != nil {
			return fmt.Errorf("merge item %d has invalid target rule ID %q: %w", item.ID, targetID, err)
		}
		target, ok := targets[id]
		if !ok {
			return fmt.Errorf("merge item %d targets rule %s absent from the target table's rule list", item.ID, id)
		}
		if target.State == api.DatasetRuleStatePendingValidation {
			return qerrors.NewBadUsagef("target rule %q (item %d) waits for validation and cannot be replaced",
				target.InstanceName, item.ID)
		}
		if first, duplicate := replaced[id]; duplicate {
			return qerrors.NewBadUsagef("more than one item replaces target rule %q (items %d and %d)",
				target.InstanceName, first, item.ID)
		}
		replaced[id] = item.ID
	}
	return nil
}

// writeMergePlan writes the plan, the RESOLUTIONS section when opts has a
// document, and under --dry-run the UNRESOLVED CONFLICTS section when a
// conflict has no resolution. It returns those conflicts.
func writeMergePlan(out mergeOutput, plan mergePlan, opts tableRulesOptions) ([]rule_merges.Item, error) {
	if err := rule_merges.RenderMerge(out.human, plan.merge, plan.names); err != nil {
		return nil, err
	}
	if opts.resolution != nil {
		if err := writeMergeResolutions(out.human, plan.merge.Items, plan.resolutions, plan.names); err != nil {
			return nil, err
		}
	}
	unresolved := rule_merges.UnresolvedConflicts(plan.merge.Items, plan.resolutions)
	if len(unresolved) > 0 {
		writeMergeSkeleton(out.human, unresolved, plan.names, opts.resolution != nil)
	}
	return unresolved, nil
}

// applyMergePlan applies the plan and reports it. With --wait it polls until
// the merge settles, and returns rule_merges.Verdict's error, followed by the
// describe line, when the merge is not applied in full.
func applyMergePlan(cmd *cobra.Command, out mergeOutput, client *rule_merges.Client, token string,
	tables mergeTables, plan mergePlan, opts tableRulesOptions,
) error {
	applied, applyBody, err := client.Apply(token, tables.targetID, plan.merge.ID, plan.resolutions)
	if err != nil {
		return err
	}
	describe := fmt.Sprintf("qctl describe rule-merge %d --table-id %d", applied.ID, tables.targetID)
	writeMergeAccepted(out.human, applied, describe, opts.wait)
	if !opts.wait {
		return out.writeDocument(applied.ID, applyBody)
	}

	final, finalBody, err := waitForRuleMerge(cmd, client, token, tables.targetID, applied.ID, opts.waitTimeout, describe)
	if err != nil {
		if finalBody == nil {
			finalBody = applyBody
		}
		if docErr := out.writeDocument(applied.ID, finalBody); docErr != nil {
			return fmt.Errorf("%w\n\nfailed to write the rule merge: %w", err, docErr)
		}
		return err
	}
	fmt.Fprintln(out.human)
	if err := rule_merges.RenderMerge(out.human, final, plan.names); err != nil {
		return err
	}
	if err := out.writeDocument(final.ID, finalBody); err != nil {
		return err
	}
	if verdict := rule_merges.Verdict(final); verdict != nil {
		return fmt.Errorf("%w\n\nNext: %s", verdict, describe)
	}
	return nil
}

// mergeRules is every rule of the source and target tables.
type mergeRules struct {
	source, target []dataset_rules.DatasetRuleTiny
}

// fetchMergeRules lists every rule of both tables.
func fetchMergeRules(ctx *cmdutil.CommandContext, tables mergeTables) (mergeRules, error) {
	var rules mergeRules
	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return rules, fmt.Errorf("failed to create client: %w", err)
	}
	token := ctx.Credential.AccessToken
	if rules.source, err = client.ListAllDatasetRules(token, tables.sourceID); err != nil {
		return rules, fmt.Errorf("failed to list the source table's rules: %w", err)
	}
	if rules.target, err = client.ListAllDatasetRules(token, tables.targetID); err != nil {
		return rules, fmt.Errorf("failed to list the target table's rules: %w", err)
	}
	return rules, nil
}

// renderNames names the tables and each table's rules by UUID.
func (r mergeRules) renderNames(tables mergeTables) rule_merges.RenderNames {
	return rule_merges.RenderNames{
		SourceTable: tables.sourceName,
		TargetTable: tables.targetName,
		SourceRule:  mergeRuleNamer(r.source),
		TargetRule:  mergeRuleNamer(r.target),
	}
}

// mergeRuleResolver resolves a table-rule reference against rules through
// cmdutil.FindTableRule, without a request per reference.
func mergeRuleResolver(ctx *cmdutil.CommandContext, rules []dataset_rules.DatasetRuleTiny) rule_merges.RuleResolver {
	return func(ref string) (string, error) {
		rule, err := cmdutil.FindTableRule(ctx, rules, ref, "")
		if err != nil {
			return "", err
		}
		return rule.ID.String(), nil
	}
}

// waitForRuleMerge polls merge mergeID with rule_merges.Wait, printing
// progress to stderr, inside a cmdutil.StartWait whose banner names describe.
// It returns the merge it settled at with the body of the last successful
// poll (nil when none succeeded), and what Waiter.Ended makes of the wait's
// error.
func waitForRuleMerge(cmd *cobra.Command, client *rule_merges.Client, token string,
	targetID, mergeID int, timeout time.Duration, describe string,
) (*rule_merges.RuleMerge, []byte, error) {
	var lastBody []byte
	poll := func(ctx context.Context) (*rule_merges.RuleMerge, error) {
		m, body, err := client.GetContext(ctx, token, targetID, mergeID)
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		lastBody = body
		return m, nil
	}
	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:      cmdutil.WaitUntilDone,
		Budget:    timeout,
		SelfTimed: true,
		Doing:     fmt.Sprintf("waiting for rule merge %d to finish applying", mergeID),
		Next:      describe,
		NextVerb:  "check with",
	})
	defer w.Stop()
	final, err := rule_merges.Wait(ctx, poll, mergeID, rule_merges.WaitOptions{
		Timeout:  timeout,
		Progress: func(m *rule_merges.RuleMerge) { rule_merges.WriteProgress(cmd.ErrOrStderr(), m) },
		Describe: describe,
	})
	return final, lastBody, w.Ended(err)
}
