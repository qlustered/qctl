package delete

import (
	"fmt"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/spf13/cobra"
)

// NewChangeReviewPolicyCommand creates the delete change-review-policy command.
func NewChangeReviewPolicyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change-review-policy",
		Short: "Delete a table-level change review policy override",
		Long: `Delete a table-level change review policy override.

The table reverts to the organization policy, or to no policy at all (every
change allowed) when the organization has none. Only table-level overrides can
be deleted; the organization-level policy can only be replaced via
"qctl apply". A table with no override exits 3 (not found).

Examples:
  qctl delete change-review-policy --table orders
  qctl delete change-review-policy --table-id 42`,
		Annotations: cmdutil.WithFormats(catalog.For("change-review-policy"), "table"),
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
			deleted, err := client.DeleteDatasetChangePolicy(ctx.Credential.AccessToken, datasetID)
			if err != nil {
				return fmt.Errorf("failed to delete change review policy: %w", err)
			}
			if !deleted {
				return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("table %d has no change review policy override", datasetID)))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "change-review-policy/table-%d deleted\n", datasetID)
			return nil
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}
