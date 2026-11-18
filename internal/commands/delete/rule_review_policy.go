package delete

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/spf13/cobra"
)

// NewRuleReviewPolicyCommand creates the delete rule-review-policy command.
func NewRuleReviewPolicyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-review-policy",
		Short: "Delete a table-level rule review policy override",
		Long: `Delete a table-level rule review policy override.

The table reverts to the organization policy (or the system default).
Only table-level overrides can be deleted; the organization-level policy
cannot be deleted, only updated via "qctl apply".

Examples:
  qctl delete rule-review-policy --table orders
  qctl delete rule-review-policy --table-id 42`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-review-policy"), "table"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePolicyTableFlag(cmd); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, err := resolvePolicyTableFlag(ctx, cmd)
			if err != nil {
				return err
			}
			client := review_policies.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err := client.DeleteDatasetRulePolicy(ctx.Credential.AccessToken, datasetID); err != nil {
				return fmt.Errorf("failed to delete rule review policy: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rule-review-policy/table-%d deleted\n", datasetID)
			return nil
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}

// resolvePolicyTableFlag resolves the required table selector shared by both
// policy delete commands. Only table-level overrides can be deleted, so an
// absent selector is an error with that reason, not the generic one — which is
// why this exists rather than cmdutil.ResolveTableIDFromFlags.
func resolvePolicyTableFlag(ctx *cmdutil.CommandContext, cmd *cobra.Command) (int, error) {
	if err := validatePolicyTableFlag(cmd); err != nil {
		return 0, err
	}
	id, ok, err := cmdutil.OptionalTableID(ctx, cmd, "")
	if err != nil {
		return 0, err
	}
	if !ok {
		// Bad usage, not a generic error: a script needs exit 2 to tell "you
		// typed the flags wrong" from a retryable server failure, and every
		// other flag validator on this branch already exits 2.
		return 0, qerrors.NewBadUsage("a table is required (--table <name> or --table-id <id>): " +
			"only table-level policy overrides can be deleted, the organization-level policy cannot")
	}
	return id, nil
}

func validatePolicyTableFlag(cmd *cobra.Command) error {
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	tableName, _ := cmd.Flags().GetString("table")
	_, haveTableID, _ := cmdutil.PositiveIDFlag(cmd, "table-id")
	if tableName == "" && !haveTableID {
		return qerrors.NewBadUsage("a table is required (--table <name> or --table-id <id>): " +
			"only table-level policy overrides can be deleted, the organization-level policy cannot")
	}
	return nil
}
