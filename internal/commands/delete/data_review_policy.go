package delete

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/spf13/cobra"
)

// NewDataReviewPolicyCommand creates the delete data-review-policy command.
func NewDataReviewPolicyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data-review-policy",
		Short: "Delete a table-level data review policy override",
		Long: `Delete a table-level data review policy override.

The table reverts to the organization policy (or the system default).
Only table-level overrides can be deleted; the organization-level policy
cannot be deleted, only updated via "qctl apply".

Examples:
  qctl delete data-review-policy --table orders
  qctl delete data-review-policy --table-id 42`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review-policy"), "table"),
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
			if err := client.DeleteDatasetDataPolicy(ctx.Credential.AccessToken, datasetID); err != nil {
				return fmt.Errorf("failed to delete data review policy: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "data-review-policy/table-%d deleted\n", datasetID)
			return nil
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}
