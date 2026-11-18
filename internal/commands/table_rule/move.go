//nolint:revive // Package name intentionally matches the table-rule command directory.
package table_rule

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// The three destination flags, exactly one of which a move takes.
var moveDestinationFlags = []string{"position", "before", "after"}

func newMoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "move <name-or-id>",
		Short: "Change where a table rule runs in its table's rule order",
		Long: `Change where a table rule runs in its table's rule order.

Rules on a table run in position order, lowest first. Moving one rule shifts
every rule between its old and new positions by one place to make room, and
prints which rules those were and the line that moves it back.

Name the destination with exactly one of:
  --position N      the POSITION number 'qctl get table-rules' prints
  --before <rule>   immediately before another rule on the same table
  --after <rule>    immediately after another rule on the same table

Positions are the stored numbers, not a count, and they can have gaps: deleting
a rule revision (allowed once none of its rules is enabled) removes its rules
from every table, their numbers stay unused
and nothing renumbers the rest, so on a table whose rules sit at 1, 2 and 4,
"--position 3" is not the third rule. --before and --after are the way to say
where a rule goes without reading the numbers.

--position past the last position moves the rule last; the result says so. The
server clamps rather than refusing, so --position 999 is how "last" is spelled.

Every rule on the table holds a position, whatever its state — disabled,
errored and deleted rules included — and moves with the others.

The rule is named the way 'qctl describe table-rule' names it: an instance
name, a short ID or a full UUID with --table <name> or --table-id <id>, or a
cached r-* alias alone. An alias given WITH a table flag must name a rule on
that table, or nothing moves (exit 3). --before and --after take the same forms,
resolved on the same table.

A move that changes nothing sends no request and says so.

Examples:
  qctl table-rule move email_check --table orders --position 1
  qctl table-rule move email_check --table-id 42 --after phone_check
  qctl table-rule move r-3 --before zip_check
  qctl table-rule move 550e8400 --table orders --position 999

-o json|yaml prints {id, instance_name, table_id, from, to, requested,
clamped, unchanged, shifted}, where requested is null unless --position was
given and shifted lists the {id, instance_name} of every rule that moved one
place; the prose moves to stderr. -o name prints table-rule/<uuid>.`,
		Annotations: cmdutil.WithFormats(catalog.For("table-rule"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runMove,
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().Int("position", 0, "Move the rule to this position number (past the last position moves it last)")
	cmd.Flags().String("before", "", "Move the rule immediately before this rule (name, short ID, UUID or r-* alias)")
	cmd.Flags().String("after", "", "Move the rule immediately after this rule (name, short ID, UUID or r-* alias)")
	return cmd
}

// moveFlags is the destination as the command line gave it, once every refusal
// decidable without a request has already fired.
type moveFlags struct {
	position int
	before   string
	after    string
}

// readMoveFlags is every usage refusal of the verb, and none costs a request.
func readMoveFlags(cmd *cobra.Command) (moveFlags, error) {
	var f moveFlags
	if err := cmdutil.RequireOneOfFlags(cmd, moveDestinationFlags); err != nil {
		return f, err
	}
	if err := cmdutil.ValidateConflictingFlags(cmd, moveDestinationFlags); err != nil {
		return f, err
	}
	if cmd.Flags().Changed("position") {
		// Refused rather than left to the server, which would clamp 0 or a
		// negative number to 1 and move the rule FIRST: a script whose
		// arithmetic went wrong would reorder a table without an error.
		n, err := cmdutil.PositiveCountFlag(cmd, "position")
		if err != nil {
			return f, err
		}
		f.position = n
	}
	for _, name := range []string{"before", "after"} {
		if !cmd.Flags().Changed(name) {
			continue
		}
		v, _ := cmd.Flags().GetString(name)
		if v == "" {
			return f, qerrors.NewBadUsagef("--%s cannot be empty: name the rule to move next to", name)
		}
		if name == "before" {
			f.before = v
		} else {
			f.after = v
		}
	}
	return f, nil
}

func runMove(cmd *cobra.Command, args []string) error {
	input := args[0]
	// The table selector first, as 'describe table-rule' checks it, then the
	// destination: both are decided from the command line alone, so either
	// order keeps every refusal at exit 2 before a request.
	if err := validateRuleSelector(cmd, input); err != nil {
		return err
	}
	flags, err := readMoveFlags(cmd)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	// ONE read serves the whole verb: the rule and any --before/--after target
	// are resolved in it, and PlanMove reads the current position, the last
	// position and the rules that will shift from it. Soft-deleted rules are in
	// it too, which is what makes the clamp and the shift count match the
	// server's — its max(position) has no state filter either.
	found, err := resolveRuleOnTable(ctx, cmd, input)
	if err != nil {
		return err
	}
	client, tableID, rules, rule := found.client, found.tableID, found.rules, found.rule
	token := ctx.Credential.AccessToken
	dest, err := moveDestination(ctx, rules, flags)
	if err != nil {
		return err
	}

	plan, err := dataset_rules.PlanMove(rules, rule, dest)
	if err != nil {
		return err
	}
	table := tableLabel(cmd, tableID)
	w := cmdutil.NewResultWriter(cmd)
	if plan.Unchanged() {
		fmt.Fprintf(w.Human, "table-rule/%s unchanged: %s in %s\n", terminal.Sanitize(rule.InstanceName), unchangedReason(plan, dest), table)
		return w.Write(newMoveResult(plan, tableID), "table-rule/"+rule.ID.String())
	}

	if err := client.ReorderDatasetRule(token, tableID, rule.ID.String(), plan.To); err != nil {
		return err
	}
	reportMove(w.Human, plan, table, tableID)
	return w.Write(newMoveResult(plan, tableID), "table-rule/"+rule.ID.String())
}

// moveResult is the -o json|yaml document for a move.
type moveResult struct {
	ID           string        `json:"id"`
	InstanceName string        `json:"instance_name"`
	TableID      int           `json:"table_id"`
	From         int           `json:"from"`
	To           int           `json:"to"`
	Requested    *int          `json:"requested"`
	Clamped      bool          `json:"clamped"`
	Unchanged    bool          `json:"unchanged"`
	Shifted      []shiftedRule `json:"shifted"`
}

// shiftedRule is one rule a move shifted by one place.
type shiftedRule struct {
	ID           string `json:"id"`
	InstanceName string `json:"instance_name"`
}

// newMoveResult builds the document for plan on table tableID.
func newMoveResult(plan dataset_rules.MovePlan, tableID int) moveResult {
	result := moveResult{
		ID:           plan.Rule.ID.String(),
		InstanceName: plan.Rule.InstanceName,
		TableID:      tableID,
		From:         plan.From,
		To:           plan.To,
		Clamped:      plan.Clamped(),
		Unchanged:    plan.Unchanged(),
		Shifted:      make([]shiftedRule, len(plan.Shifted)),
	}
	if plan.Requested > 0 {
		requested := plan.Requested
		result.Requested = &requested
	}
	for i, r := range plan.Shifted {
		result.Shifted[i] = shiftedRule{ID: r.ID.String(), InstanceName: r.InstanceName}
	}
	return result
}

// moveTableID names the table a move acts on.
//
// A table flag always decides it, even beside an r-* alias: the rule is then
// looked up in THAT table's list, so an alias for a rule on another table is
// not found rather than moved on a table the user never named. Only an alias
// ALONE names its table, through the rule's detail.
func moveTableID(ctx *cmdutil.CommandContext, cmd *cobra.Command, client *dataset_rules.Client, input string) (int, error) {
	// Resolved first either way, so an unknown alias is refused before the
	// table lookup and the rule list rather than after them.
	id, wasAlias, err := cmdutil.ResolveTableRuleAlias(ctx, input)
	if err != nil {
		return 0, err
	}
	if !cmd.Flags().Changed("table") && !cmd.Flags().Changed("table-id") {
		if wasAlias {
			detail, err := client.GetDatasetRuleDetail(ctx.Credential.AccessToken, id)
			if err != nil {
				return 0, fmt.Errorf("failed to get table rule: %w", err)
			}
			return detail.DatasetID, nil
		}
	}
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return 0, fmt.Errorf("%w (or pass an r-* alias)", err)
	}
	return tableID, nil
}

// validateRuleSelector refuses a table selector that cannot go with input: an
// r-* alias takes at most one table flag, and any other rule reference needs
// one.
func validateRuleSelector(cmd *cobra.Command, input string) error {
	if cmdutil.IsRegisteredAlias(input) {
		return cmdutil.ValidateOptionalTableSelector(cmd, "")
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return fmt.Errorf("%w (or pass an r-* alias)", err)
	}
	return nil
}

// ruleOnTable is a rule reference resolved on the table it was looked up in.
type ruleOnTable struct {
	client  *dataset_rules.Client
	tableID int
	rules   []dataset_rules.DatasetRuleTiny
	rule    dataset_rules.DatasetRuleTiny
}

// resolveRuleOnTable finds input in the rule list of the table moveTableID
// names.
func resolveRuleOnTable(ctx *cmdutil.CommandContext, cmd *cobra.Command, input string) (ruleOnTable, error) {
	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return ruleOnTable{}, fmt.Errorf("failed to create client: %w", err)
	}
	tableID, err := moveTableID(ctx, cmd, client, input)
	if err != nil {
		return ruleOnTable{}, err
	}
	rules, err := client.ListAllDatasetRules(ctx.Credential.AccessToken, tableID)
	if err != nil {
		return ruleOnTable{}, fmt.Errorf("failed to list table rules: %w", err)
	}
	// Whatever named the table, the rule has to be on THIS table's list.
	rule, err := cmdutil.FindTableRule(ctx, rules, input, "")
	if err != nil {
		return ruleOnTable{}, err
	}
	return ruleOnTable{client: client, tableID: tableID, rules: rules, rule: rule}, nil
}

// moveDestination resolves --before/--after on the table's own list.
func moveDestination(ctx *cmdutil.CommandContext, rules []dataset_rules.DatasetRuleTiny, flags moveFlags) (dataset_rules.MoveDestination, error) {
	dest := dataset_rules.MoveDestination{Position: flags.position}
	var err error
	if dest.Before, err = moveTarget(ctx, rules, flags.before, "--before"); err != nil {
		return dest, err
	}
	if dest.After, err = moveTarget(ctx, rules, flags.after, "--after"); err != nil {
		return dest, err
	}
	return dest, nil
}

// moveTarget is one --before/--after reference, nil when the flag is unset.
func moveTarget(ctx *cmdutil.CommandContext, rules []dataset_rules.DatasetRuleTiny, ref, flag string) (*dataset_rules.DatasetRuleTiny, error) {
	if ref == "" {
		return nil, nil
	}
	t, err := cmdutil.FindTableRule(ctx, rules, ref, flag)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func unchangedReason(plan dataset_rules.MovePlan, dest dataset_rules.MoveDestination) string {
	switch {
	case dest.Before != nil:
		return fmt.Sprintf("already immediately before %s (position %d)", dest.Before.InstanceName, plan.From)
	case dest.After != nil:
		return fmt.Sprintf("already immediately after %s (position %d)", dest.After.InstanceName, plan.From)
	case plan.Clamped():
		return fmt.Sprintf("already at the last position (%d)", plan.From)
	default:
		return fmt.Sprintf("already at position %d", plan.From)
	}
}

// tableLabel names the table the way the user did when they named it, and by
// id always, so the result line is unambiguous either way.
func tableLabel(cmd *cobra.Command, tableID int) string {
	if name, _ := cmd.Flags().GetString("table"); name != "" {
		return fmt.Sprintf("table %s (id: %d)", name, tableID)
	}
	return fmt.Sprintf("table %d", tableID)
}

// reportMove prints what moved and the line that moves it back.
//
// The reversal names the table by id and the rule by UUID, so it runs as
// printed wherever it is pasted. Not by instance name: names are free-form, and
// a rule named like an alias (`r-2`) is resolved as the alias, which
// FindTableRule tries before any name. It
// restores the ORDER exactly; when the rule was the table's last with a gap
// directly below it, its own number comes back as the new last position rather
// than the old one (see dataset_rules.MovePlan).
func reportMove(out io.Writer, plan dataset_rules.MovePlan, table string, tableID int) {
	fmt.Fprintf(out, "table-rule/%s moved from position %d to %d in %s\n",
		terminal.Sanitize(plan.Rule.InstanceName), plan.From, plan.To, table)
	if plan.Clamped() {
		fmt.Fprintf(out, "--position %d is past the last position, so the rule moved last.\n", plan.Requested)
	}
	if len(plan.Shifted) > 0 {
		names := make([]string, len(plan.Shifted))
		for i, r := range plan.Shifted {
			names[i] = terminal.Sanitize(r.InstanceName)
		}
		direction := "later to make room"
		if plan.To > plan.From {
			direction = "earlier to fill the gap"
		}
		fmt.Fprintf(out, "%d rule(s) moved one place %s: %s\n", len(names), direction, strings.Join(names, ", "))
	}
	fmt.Fprintf(out, "\nMove it back with:\n  qctl table-rule move %s --table-id %s --position %d\n",
		plan.Rule.ID.String(), strconv.Itoa(tableID), plan.From)
}
