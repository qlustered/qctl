package rows

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_row_ignores"
	"github.com/spf13/cobra"
)

const (
	ignoreRuleVerb   = "ignore-rule"
	unignoreRuleVerb = "unignore-rule"
)

// ignoreRuleSupport is ignore-rule's row, and unignoreRuleSupport below differs
// from it in the row-id population alone: one selector, at ONE scope, and that
// scope is the cloud source.
//
// cloud is the only non-nil list because data_source_model_id is a REQUIRED
// parameter of all three rule-row-ignores routes — a body field on create, a
// query parameter on list and delete — and an ignore is keyed on (table, cloud
// source, row, rule). There is no table-wide form to fall back to, so `table` stays nil
// and requireCloudSourceScope refuses a missing narrowing before resolveTarget
// would otherwise answer "needs a row selector: (none)".
//
// ignore-rule's --row-id names a live row of the cloud source, clean or
// quarantined: create splits the ids with live_row_ids_for_data_source, a
// select FROM the clean and the bad-rows table WHERE data_source_model_id
// matches and is_deleted_ IS DISTINCT FROM TRUE (atlas
// cettings/.../data_controller/quarantine.py), and answers 404 naming the ids
// in neither (ignore_dataset_rule_for_rows).
//
// --signature, --status and --all are not registered at all, as on
// search-replace: the routes take ids and nothing else, and there is no bulk
// form to point a refusal at.
var ignoreRuleSupport = support{
	cloud:  []selectorKind{selRowIDs},
	rowIDs: rowIDsLive,
}

// unignoreRuleSupport differs from ignoreRuleSupport in the population alone.
// An ignore outlives its row's stay in quarantine: a row the ignore let through
// moves to the clean table under the SAME id (clean and quarantine share one
// sequence, atlas cettings/.../data_controller/row_id_remap.py), and nothing
// discards the ignore when it does. So the rows unignore-rule can name are the
// row_ids of the ignores themselves, clean or quarantined, and pointing at the
// quarantine listing would hide the commonest case.
var unignoreRuleSupport = support{
	cloud:  []selectorKind{selRowIDs},
	rowIDs: rowIDsIgnored,
}

// The consequences of confirming, printed before the prompt and under --yes.
//
// The reprocess notes are the ones nobody would predict from the verb: both
// writes call reprocess_bad_rows_for_dataset_rule — un-ignore every time,
// create whenever a named row is quarantined, even when every pair was already
// ignored, after taking the rule's unresolved warnings of the source off the
// named clean rows (capi/capi/controllers/dataset_rule_row_ignore_dispatch.py
// ignore_dataset_rule_for_rows) — and it queues every
// non-deleted bad row whose dataset_rule_ids_ carries the rule, in every data
// source of the dataset, since find_bad_rows_for_rule has no row or source
// clause (cettings/.../data_controller/quarantine.py).
//
// A mirror-loaded table queues the same rows: the fan-out has no loading-process
// branch since atlas-wo1g removed its sync skip. The phantom run the reprocess
// publishes looks a row's ignores up by its id. An upload reads ignores only on
// a mirror, append or append_skip_identical_rows table (the gate is
// Ingestor._steward_carry_source), and looks each incoming line up by the
// initial_signature_ of the row an ignore names: the key hash on a keyed
// table, a hash of the line's values on a keyless one, and on append only
// among rows a prior upload of the same file linked. Of the copies of a key
// the upload repeats or the cloud source stores more than once, only the one
// the loader pairs with the ignored row by content keeps the ignore
// (steward_carry_keepers), and that copy is moved to quarantine. So a later
// upload can flag an ignored row again when it is an edited keyless line,
// another file's row, or an unpaired copy of a duplicated key.
// Nothing here says how to re-apply the ignore after an upload flags the row
// again: it is keyed on a row id, and keyless mirror can re-mint a row's id (on a
// content change, and on a partitioned clean/bad move, atlas-vomz), so
// re-validating "the row" is not promised to reach it.
//
// The existing note is create_ignore's early return: a (row, rule) pair that
// is already ignored answers the EXISTING record, and the reason sent is
// dropped for it.
//
// The un-ignore notes follow from the same select: an ignored row's
// suppressed issue is dropped before its issue record is built, so once it has
// been re-validated its dataset_rule_ids_ no longer carries the rule and
// find_bad_rows_for_rule cannot find it to queue. The un-ignore reprocess note
// therefore says "still flags", so the two lines do not contradict each other.
const (
	ignoreReprocessNote   = "When a named row is quarantined, every quarantined row this rule flags, in every cloud source of the table, is queued for re-validation — not only the rows named, and even if every row was already ignored. A named clean row instead loses this rule's unresolved warnings from this cloud source, its cells unchanged. A later upload keeps the ignore only on a mirror table, for a line with the same key (on a keyless table, the same unchanged line), and on an append table, for the same key (without key_columns, the same unchanged line) in a new version of the same file; any other upload that carries an ignored row again can flag it again. Of the copies of a key the upload repeats or the stored rows hold more than once, only the one paired with the ignored row by content keeps the ignore, and lands in quarantine."
	ignoreExistingNote    = "A row already ignored for this rule keeps its existing ignore and reason."
	unignoreReprocessNote = "Every quarantined row this rule still flags, in every cloud source of the table, is queued for re-validation."
	unignoreSkipNote      = "A row the ignore already let through no longer carries this rule, so that queue skips it: the rule applies to it again only when it is next re-validated."
)

func newIgnoreRuleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   ignoreRuleVerb + " [table-id-or-name]",
		Short: "Stop one table rule flagging named rows",
		Long: `Stop one table rule flagging the named rows of one cloud source, clean or
quarantined.

  qctl rows ignore-rule orders --cloud-source s3-nightly --rule email_check --row-id 1042
  qctl rows ignore-rule orders --cloud-source-id 7 --rule r-3 --row-id 1042 --row-id 1043 --reason "known test accounts"

This is the "this row is fine, stop flagging it" escape hatch for ONE rule: the
other rules on the table still run on these rows. An ignore is recorded with
who decided it and why, and 'qctl rows unignore-rule' reverses it.

Row ids come from the clean or the quarantine listing of that cloud source:
  qctl get data <table> [--is-bad-data] --data-source-model-id <cloud-source-id>
Every id must be a live row of the cloud source, or nothing is ignored and the
unknown ids are named (exit 3).

--cloud-source or --cloud-source-id is required: an ignore belongs to one cloud
source of the table, and there is no table-wide form.

--rule takes an instance name, a short ID, a full UUID or an r-* alias, and the
rule must be on this table.

When a named row is quarantined, ignoring queues every quarantined row the rule
flags, in every cloud source of the table, for re-validation, mirror-loaded
tables included, so rows can leave quarantine as a result. A named clean row
instead loses the rule's unresolved warnings from this cloud source, its cells
unchanged. A row already ignored for the rule keeps its existing ignore and
reason.

A later upload keeps the ignore only on a mirror table, for a line with the same
key (on a keyless table, the same unchanged line), and on an append table, for
the same key (without key_columns, the same unchanged line) in a new version of
the same file. Any other upload that carries an ignored row again can flag it
again. Of the copies of a key the upload repeats or the stored rows hold more
than once, only the one paired with the ignored row by content keeps the
ignore, and lands in quarantine.

See what is ignored with 'qctl get rule-ignores'.

-o json|yaml prints a list of the ignore records, one per row id (the existing
record for a row already ignored), and moves the result lines to stderr.`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIgnoreRule(cmd, firstArg(args))
		},
	}
	addScopeFlags(cmd, ignoreRuleSupport)
	addSelectorFlags(cmd, ignoreRuleSupport, selRowIDs)
	cmdutil.RefString(cmd.Flags(), "rule", "table-rule", "Table rule to ignore (instance name, short ID, UUID or r-* alias; required)")
	cmd.Flags().String("reason", "", "Why these rows are fine, recorded on the ignore")
	addYesFlag(cmd)
	return cmd
}

func newUnignoreRuleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   unignoreRuleVerb + " [table-id-or-name]",
		Short: "Let a table rule flag rows it was ignoring again",
		Long: `Remove the ignore of one table rule on the named rows of one cloud source.

  qctl rows unignore-rule orders --cloud-source s3-nightly --rule email_check --row-id 1042

The reverse of 'qctl rows ignore-rule', addressed the same way: the cloud
source, the rule and the row ids. qctl looks the ignores up first and removes
only those it found; a row with no active ignore of that rule is reported and
skipped, and exit 3 means none of the named rows had one.

Row ids are the ROW-ID column of 'qctl get rule-ignores', not the quarantine
listing: an ignore stays on its row after the row leaves quarantine, and the row
keeps its id in the clean table.

Removing an ignore does not re-flag a row the ignore already let through. The
re-validation it queues covers the quarantined rows the rule still flags, and a
row that was re-validated while ignored no longer carries the rule, so the rule
applies to it again only when that row is next re-validated.

See what is ignored with 'qctl get rule-ignores'.`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnignoreRule(cmd, firstArg(args))
		},
	}
	addScopeFlags(cmd, unignoreRuleSupport)
	addSelectorFlags(cmd, unignoreRuleSupport, selRowIDs)
	cmdutil.RefString(cmd.Flags(), "rule", "table-rule", "Table rule to stop ignoring (instance name, short ID, UUID or r-* alias; required)")
	addYesFlag(cmd)
	return cmd
}

// requireCloudSourceScope is the scope refusal the support row cannot express
// on its own: a verb with NO table-scope selectors would otherwise be answered
// "needs a row selector: (none)" by resolveSelector, which names neither the
// missing flag nor why. Decided from the flags alone, so exit 2 and no request.
func requireCloudSourceScope(cmd *cobra.Command, verb string) error {
	if hasFlag(cmd, "cloud-source") || hasFlag(cmd, "cloud-source-id") {
		return nil
	}
	return qerrors.NewBadUsagef(
		"'qctl rows %s' needs --cloud-source <name> or --cloud-source-id <id>: an ignore belongs to one cloud source of the table", verb)
}

// ruleFlag reads --rule, required AND non-empty: `--rule ""` marks the flag
// Changed while naming nothing.
func ruleFlag(cmd *cobra.Command, verb string) (string, error) {
	if !cmd.Flags().Changed("rule") {
		return "", qerrors.NewBadUsagef("'qctl rows %s' needs --rule <name-or-id>: the table rule", verb)
	}
	rule, _ := cmd.Flags().GetString("rule")
	if rule == "" {
		return "", qerrors.NewBadUsage("--rule cannot be empty: name the table rule")
	}
	return rule, nil
}

// uniqueRowIDs drops repeats, keeping first-seen order. The create route
// answers one record per id IN THE BODY, so a repeated id would come back
// twice and a count of the response would overstate the rows.
func uniqueRowIDs(ids []int) []int {
	seen := make(map[int]bool, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ignoreTarget is everything both verbs resolve before they act.
type ignoreTarget struct {
	ctx  *cmdutil.CommandContext
	sc   scope
	sel  selector
	rule dataset_rules.DatasetRuleTiny
}

// resolveIgnoreTarget is the front half both verbs share, in the group's
// refusal order: the cloud-source requirement and resolveTarget's steps (the
// verb's own flag checks as step 2b), then the one extra lookup — the rule, in
// the table's full list, strictly (cmdutil.FindTableRule).
func resolveIgnoreTarget(cmd *cobra.Command, tableArg, verb string, sup support, localChecks func(*cobra.Command) error) (ignoreTarget, error) {
	if err := requireCloudSourceScope(cmd, verb); err != nil {
		return ignoreTarget{}, err
	}
	var ruleArg string
	ctx, sc, sel, err := resolveTarget(cmd, tableArg, sup, verb, func(c *cobra.Command) error {
		var rerr error
		if ruleArg, rerr = ruleFlag(c, verb); rerr != nil {
			return rerr
		}
		if localChecks != nil {
			return localChecks(c)
		}
		return nil
	})
	if err != nil {
		return ignoreTarget{}, err
	}
	// Guard the values that reach the wire, the group's rule: every route here
	// needs a cloud source id and a list of ids.
	if sel.kind != selRowIDs || sc.kind != scopeCloudSource {
		return ignoreTarget{}, unroutableSelector(verb, sel, sc)
	}
	sel.rowIDs = uniqueRowIDs(sel.rowIDs)

	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return ignoreTarget{}, fmt.Errorf("failed to create client: %w", err)
	}
	rules, err := client.ListAllDatasetRules(ctx.Credential.AccessToken, sc.tableID)
	if err != nil {
		return ignoreTarget{}, fmt.Errorf("failed to list table rules: %w", err)
	}
	rule, err := cmdutil.FindTableRule(ctx, rules, ruleArg, "--rule")
	if err != nil {
		return ignoreTarget{}, err
	}
	return ignoreTarget{ctx: ctx, sc: sc, sel: sel, rule: rule}, nil
}

func runIgnoreRule(cmd *cobra.Command, tableArg string) error {
	var reason *string
	t, err := resolveIgnoreTarget(cmd, tableArg, ignoreRuleVerb, ignoreRuleSupport, func(c *cobra.Command) error {
		if !c.Flags().Changed("reason") {
			return nil
		}
		r, _ := c.Flags().GetString("reason")
		if strings.TrimSpace(r) == "" {
			return qerrors.NewBadUsage("--reason cannot be empty: omit it to record no reason")
		}
		reason = &r
		return nil
	})
	if err != nil {
		return err
	}

	act := action{
		verb:   ignoreRuleVerb,
		gerund: "Ignore rule " + t.rule.InstanceName + " on",
		extra:  []string{ignoreReprocessNote, ignoreExistingNote},
	}
	if err := confirmAction(cmd, t.ctx, t.sc, t.sel, ignoreRuleSupport, act); err != nil {
		return err
	}

	client := rule_row_ignores.NewClient(t.ctx.ServerURL, t.ctx.OrganizationID, t.ctx.Verbosity)
	records, err := client.Create(t.ctx.Credential.AccessToken, t.sc.tableID, rule_row_ignores.CreateRequest{
		CloudSourceID: t.sc.cloudSourceID,
		RuleID:        t.rule.ID,
		RowIDs:        t.sel.rowIDs,
		Reason:        reason,
	})
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	fmt.Fprintf(w.Human, "%s: rule %s ignored on %d row(s) in %s\n",
		ignoreRuleVerb, t.rule.InstanceName, len(records), t.sc.label())
	fmt.Fprintf(w.Human, "\nSee the ignores with:\n  %s\n", ruleIgnoresLine(t.sc, t.rule))
	return w.Write(records)
}

func runUnignoreRule(cmd *cobra.Command, tableArg string) error {
	t, err := resolveIgnoreTarget(cmd, tableArg, unignoreRuleVerb, unignoreRuleSupport, nil)
	if err != nil {
		return err
	}
	token := t.ctx.Credential.AccessToken
	client := rule_row_ignores.NewClient(t.ctx.ServerURL, t.ctx.OrganizationID, t.ctx.Verbosity)
	// The ignore ids come ONLY from this scoped listing. atlas's DELETE looks
	// an ignore up by id alone (qctl-tas), so an id from anywhere else could
	// discard another table's ignore; the listing is what guarantees each one
	// belongs to this cloud source and this rule. One request covers every
	// named row: the route is unpaginated.
	scoped := rule_row_ignores.ListFilter{CloudSourceID: t.sc.cloudSourceID, RuleID: &t.rule.ID}
	active, err := client.List(token, t.sc.tableID, scoped)
	if err != nil {
		return err
	}
	found, missing := matchIgnores(active, t.sel.rowIDs)
	if len(found) == 0 {
		return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf(
			"rule %s is not ignored on row(s) %s in %s; nothing was changed",
			t.rule.InstanceName, joinInts(missing), t.sc.label())))
	}
	if len(missing) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: rule %s is not ignored on row(s) %s; skipping them\n",
			t.rule.InstanceName, joinInts(missing))
	}

	act := action{
		verb:   unignoreRuleVerb,
		gerund: "Stop ignoring rule " + t.rule.InstanceName + " on",
		extra:  []string{unignoreReprocessNote, unignoreSkipNote},
	}
	// The prompt counts the ignores that will be removed, not the rows typed.
	sel := selector{kind: selRowIDs, rowIDs: ignoreRows(found)}
	if err := confirmAction(cmd, t.ctx, t.sc, sel, unignoreRuleSupport, act); err != nil {
		return err
	}

	removed, gone, err := removeIgnores(client, token, t.sc.tableID, scoped, found)
	if len(gone) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: the ignore on row(s) %s was already removed by the time qctl reached it\n",
			joinInts(gone))
	}
	if err != nil {
		// Not "before this failure": a 404 is only judged after the loop, so
		// deletes that ran after it are in the count.
		return fmt.Errorf("%w\n\n%s: %d of %d ignore(s) were removed by this run",
			err, unignoreRuleVerb, removed, len(found))
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s: rule %s no longer ignored on %d row(s) in %s\n",
		unignoreRuleVerb, t.rule.InstanceName, removed, t.sc.label())
	return nil
}

// matchIgnores splits the named rows into the active ignores the scoped listing
// holds for them, in the order named, and the rows it holds none for.
func matchIgnores(active []rule_row_ignores.Ignore, rowIDs []int) (found []rule_row_ignores.Ignore, missing []int) {
	byRow := make(map[int]rule_row_ignores.Ignore, len(active))
	for _, ig := range active {
		byRow[ig.RowID] = ig
	}
	for _, id := range rowIDs {
		if ig, ok := byRow[id]; ok {
			found = append(found, ig)
		} else {
			missing = append(missing, id)
		}
	}
	return found, missing
}

func ignoreRows(ignores []rule_row_ignores.Ignore) []int {
	rows := make([]int, len(ignores))
	for i, ig := range ignores {
		rows[i] = ig.RowID
	}
	return rows
}

// removeIgnores deletes each ignore in turn and reports how many it removed and
// which rows' ignores were already gone.
//
// A 404 is not trusted to mean "already gone". The DELETE route answers 404 for
// an inactive ignore, but also from its dataset and cloud-source guards
// (build_dataset_rule_row_ignore_controller), so a table deleted mid-run would
// read as every ignore "already removed" at exit 0. Every 404 is therefore
// checked against one fresh scoped listing after the loop — the same guards
// run on the listing, so that case surfaces as the listing's own error. A row
// the listing no longer carries an ignore for was un-ignored by someone else.
// A row it still does is the 404's error, matched by ROW rather than ignore id,
// so an ignore removed and re-created in between is not reported as gone while
// the row is still ignored. Any other failure stops the loop at once.
func removeIgnores(client *rule_row_ignores.Client, token string, tableID int, scoped rule_row_ignores.ListFilter,
	found []rule_row_ignores.Ignore) (removed int, gone []int, err error) {
	type miss struct {
		ig  rule_row_ignores.Ignore
		err error
	}
	var notFound []miss
	for _, ig := range found {
		err := client.UnIgnore(token, tableID, ig)
		var apiErr *rule_row_ignores.APIError
		switch {
		case err == nil:
			removed++
		case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound:
			notFound = append(notFound, miss{ig, err})
		default:
			return removed, nil, err
		}
	}
	if len(notFound) == 0 {
		return removed, nil, nil
	}
	still, err := client.List(token, tableID, scoped)
	if err != nil {
		return removed, nil, err
	}
	stillIgnored := make(map[int]bool, len(still))
	for _, ig := range still {
		stillIgnored[ig.RowID] = true
	}
	var stuck error
	for _, m := range notFound {
		if !stillIgnored[m.ig.RowID] {
			gone = append(gone, m.ig.RowID)
		} else if stuck == nil {
			stuck = fmt.Errorf("%w (row %d is still ignored for this rule)", m.err, m.ig.RowID)
		}
	}
	return removed, gone, stuck
}

// ruleIgnoresLine is the 'qctl get rule-ignores' line that reads the ignores
// back, narrowed to the cloud source and rule the write used. The rule is named
// by UUID: an instance name resolves the same way, but the UUID is the one
// reference no later rename can point at a different rule.
func ruleIgnoresLine(sc scope, rule dataset_rules.DatasetRuleTiny) string {
	return "qctl get rule-ignores " + sc.tableCommandRef() +
		" --cloud-source-id " + strconv.Itoa(sc.cloudSourceID) +
		" --rule " + rule.ID.String()
}

func joinInts(ids []int) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.Itoa(id)
	}
	return strings.Join(s, ", ")
}
