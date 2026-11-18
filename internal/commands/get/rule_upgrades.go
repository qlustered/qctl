package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/rule_upgrades"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

// ruleUpgradeColumns omits table_id and id, which are addressing detail rather
// than answer; both stay reachable through --columns.
const ruleUpgradeColumns = "table,rule,on,upgrade_to"

// NewRuleUpgradesCommand creates the get rule-upgrades command.
func NewRuleUpgradesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-upgrades <rule-name-or-id>",
		Short: "List the rule instances still running an older revision of a rule",
		Long: `List the rule instances still running an older revision of a rule family.

Answers the question a rule_upgrade notification raises: which tables, and
which rule instances on them, are still on the old code.

Two things this listing is NOT:

  * It is not the family's total blast radius. The backend scopes it to YOU --
    rules you instantiated, plus rules on tables you hold access to if you are
    an editor or admin. Two people asking about the same rule can legitimately
    get different answers.
  * It is not every instance carrying an old revision. Only ENABLED instances
    are listed, so a rule sitting in error or disabled on the old revision does
    not appear here even though it is outdated.

Upgrading is not done from here. Apply a TableRule manifest for the new
revision whose spec.upgradedFrom is the old instance's id (the id column):

  qctl apply -f new-rule.yaml   # see 'qctl explain table-rule.spec.upgradedFrom'

Available columns: table, table_id, rule, id, on, upgrade_to. Use --columns to
pick; -o json and -o yaml emit the raw wire object, the only views carrying
family_id, new_default_revision_id and each item's current_revision_id.
-o name prints table-rule/<id> for each rule instance, which 'qctl describe
table-rule' takes.

Examples:
  qctl get rule-upgrades phone_format
  qctl get rule-upgrades phone_format --columns table,table_id,rule,id,on
  qctl get rule-upgrades phone_format -o json
  qctl get rule-upgrades phone_format -o name`,
		Annotations: output.WithNameKind(cmdutil.WithArgKinds(catalog.For("rule-upgrade"), "rule"), "table-rule"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "" {
				return qerrors.NewBadUsage("rule identifier cannot be empty")
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			return runRuleUpgrades(cmd, ctx, args[0])
		},
	}
	cmdutil.DeclareColumns(cmd, []rule_upgrades.UpgradeRow(nil))
	return cmd
}

func runRuleUpgrades(cmd *cobra.Command, ctx *cmdutil.CommandContext, input string) error {
	rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	family, err := rule_upgrades.ResolveFamily(rvClient, ctx.Credential.AccessToken, input)
	if err != nil {
		return err
	}

	client := rule_upgrades.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	details, err := client.GetUpgradeDetails(ctx.Credential.AccessToken, family.ID)
	if err != nil {
		return fmt.Errorf("failed to get upgrade details for %q: %w", family.Name, err)
	}

	if cmd.Flags().Changed("output") {
		outputFormat, _ := cmd.Flags().GetString("output")
		switch outputFormat {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, details)
		}
	}

	rows := rule_upgrades.ToDisplayList(details)
	if len(rows) == 0 {
		// total is nil on purpose: this endpoint is not paginated, so
		// printEmptyResultWithHint's past-the-last-page branch must not be
		// reachable.
		skip, err := printEmptyResultWithHint(cmd, ctx, "rule upgrades", rows, nil,
			func() string { return emptyUpgradeHint(details) })
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)
	return tableui.PrintFromCmd(cmd, rows, ruleUpgradeColumns)
}

// emptyUpgradeHint distinguishes the two ways the list comes back empty. They
// are different facts and only one of them is good news.
func emptyUpgradeHint(details *rule_upgrades.UpgradeDetails) string {
	if details.NewDefaultRevisionID == nil {
		return fmt.Sprintf(
			"%s has no default revision, so nothing can be outdated.\n"+
				"Set one with: qctl set-default rule %s --release <release>\n",
			details.FamilyName, details.FamilyName)
	}
	release := ""
	if details.NewDefaultRelease != nil {
		release = *details.NewDefaultRelease
	}
	return fmt.Sprintf(
		"Every %s instance you can see is already on %s.\n"+
			"The listing is scoped to you and covers enabled instances only.\n",
		details.FamilyName, release)
}
