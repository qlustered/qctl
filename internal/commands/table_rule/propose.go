package table_rule //nolint:revive // Matches the command directory.

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/change_proposal"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newProposeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "propose <name-or-id>",
		Short: "Propose a change to a table rule's params for an approver to apply",
		Long: `Propose a change to a table rule's params instead of applying it: the server
stores it as a change proposal, and the params change runs only when an
approver approves it, as the approver. Use it where your table's change review
policy lets you propose a params change but not apply it, or where you want a
second pair of eyes. Params are the only part of a rule a proposal can change;
everything else goes through 'qctl apply -f'.

--set-param key=value and --unset-param key edit the rule's LIVE params, read
once before the proposal is created, and the proposal carries the complete
result: approving it replaces the rule's params as a whole, exactly as
'qctl apply -f' with spec.params does. Values are in the units
'qctl get table-rule -o yaml' shows. A --set-param value is read as JSON when
it parses as one (5 is a number, true is a boolean) and as a plain string
otherwise; quote it to force a string: --set-param threshold='"5"'.

A --unset-param key the rule does not have, and a key both set and unset, are
refused with exit 2 before anything is created. So is an edit that leaves a
param holding masked text (•••••, or ••••• and 4 characters): the server shows
every param of a rule on a masked PII column that way to a viewer who has it
masked, and the proposal would carry that text as the param's value. Such a
viewer must set or unset every param. An edit that leaves the params
as they are is not: the server accepts it, and the result says the params are unchanged.

The rule is named the way 'qctl table-rule move' names it: an instance name, a
short ID or a full UUID with --table <name> or --table-id <id>, or a cached
r-* alias alone.

The proposal is submitted for review unless you pass --draft. Submitting
records the rule's params at that moment as the proposal's base params and
stages a validation dry run of the live rule against the proposed params, so it
can be refused with 429 over your dry-run budget, or with 409 when the table has
no active data source. A draft stages nothing until it is submitted.

--also-alert and --also-warning name alerts and warnings this rule raised that
the change also addresses; a submitted proposal holds them, and another
proposal naming one is refused with 409 CHANGE_PROPOSAL_ACTIVE. --depends-on
<proposal-id> makes approving it wait until that draft, pending_review or
changes_requested proposal of the same table is executed or obsolete.

--note <text> stores your rationale with the proposal, shown to the approver
by 'qctl describe change-proposal' and 'qctl change-proposal approve'.

Examples:
  qctl table-rule propose amount_range --table orders --set-param max=500
  qctl table-rule propose r-3 --set-param threshold=0.2 --unset-param legacy --draft
  qctl table-rule propose 550e8400 --table-id 42 --set-param mode='"strict"'

Follow it with 'qctl describe change-proposal <id>'; change it with
'qctl change-proposal edit <id> --set-param <key>=<value>'.

-o json|yaml prints the created proposal and moves the prose to stderr;
-o name prints change-proposal/<uuid>.`,
		Annotations: cmdutil.WithFormats(catalog.For("change-proposal"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runPropose,
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().StringArray("set-param", nil, "Set one rule parameter as key=value (repeatable; merged into the rule's live params)")
	cmd.Flags().StringArray("unset-param", nil, "Remove one rule parameter by key (repeatable)")
	change_proposal.AddRuleProposeFlags(cmd)
	return cmd
}

// readParamEdit reads --set-param and --unset-param; neither given is bad
// usage (exit 2).
func readParamEdit(cmd *cobra.Command) (dataset_rules.ParamEdit, error) {
	set, _ := cmd.Flags().GetStringArray("set-param")
	unset, _ := cmd.Flags().GetStringArray("unset-param")
	edit, err := dataset_rules.ParseParamEdit(set, unset)
	if err != nil {
		return edit, err
	}
	if edit.IsZero() {
		return edit, qerrors.NewBadUsage("nothing to propose: give --set-param <key>=<value> or --unset-param <key>")
	}
	return edit, nil
}

func runPropose(cmd *cobra.Command, args []string) error {
	input := args[0]
	if err := validateRuleSelector(cmd, input); err != nil {
		return err
	}
	edit, err := readParamEdit(cmd)
	if err != nil {
		return err
	}
	opts, err := change_proposal.ReadProposeOptions(cmd, false)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	found, err := resolveRuleOnTable(ctx, cmd, input)
	if err != nil {
		return err
	}
	ref := "table-rule/" + terminal.Sanitize(found.rule.InstanceName)
	detail, err := found.client.GetDatasetRuleDetailContext(cmd.Context(), ctx.Credential.AccessToken, found.rule.ID.String())
	if err != nil {
		return fmt.Errorf("failed to get %s: %w", ref, err)
	}
	live := detail.Params
	if live == nil {
		live = map[string]interface{}{}
	}
	if err := dataset_rules.RefuseMaskedCarriedParams(ref, live, edit.Set, edit.Unset); err != nil {
		return err
	}
	proposed, err := edit.Apply(live, ref)
	if err != nil {
		return err
	}
	proposal, err := opts.CreateRuleParams(cmd, ctx, found.rule.ID, proposed)
	if err != nil {
		return err
	}
	var b strings.Builder
	if diff := change_proposals.ParamsDiff(live, proposed); len(diff) > 0 {
		change_proposals.WriteParamsDiff(&b, fmt.Sprintf("Params change for %s (its live value -> the proposed one):", ref), diff)
	} else {
		fmt.Fprintf(&b, "Params change for %s: (none — the proposed params equal its live params)\n", ref)
	}
	fmt.Fprint(cmdutil.NewResultWriter(cmd).Human, terminal.Sanitize(b.String()))
	return change_proposal.WriteResult(cmd, proposal, "created")
}
