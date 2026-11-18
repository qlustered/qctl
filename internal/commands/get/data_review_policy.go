package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/spf13/cobra"
)

// NewDataReviewPolicyCommand creates the get data-review-policy command.
func NewDataReviewPolicyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data-review-policy",
		Short: "Show the data review policy",
		Long: `Show the data review policy for the organization or a specific table.

The "source" column tells you which level the policy comes from
(dataset, organization, or system_default).

Use -o yaml to get an apply-compatible manifest for editing:

  qctl get data-review-policy -o yaml > policy.yaml
  # edit policy.yaml
  qctl apply -f policy.yaml

Examples:
  qctl get data-review-policy
  qctl get data-review-policy --table orders
  qctl get data-review-policy --table-id 42 -o yaml`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review-policy"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runGetDataReviewPolicy,
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmdutil.DeclareColumns(cmd, []review_policies.DataPolicyDisplay(nil))
	return cmd
}

func runGetDataReviewPolicy(cmd *cobra.Command, args []string) error {
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken

	// No table selector means the organization-level policy, which is what the
	// `if datasetID > 0` branch below already keys on — so the "was it set"
	// return is discarded deliberately, not overlooked.
	datasetID, _, err := cmdutil.OptionalTableID(ctx, cmd, "")
	if err != nil {
		return err
	}

	client := review_policies.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	var policy *review_policies.DataPolicy
	if datasetID > 0 {
		policy, err = client.GetDatasetDataPolicy(token, datasetID)
	} else {
		policy, err = client.GetOrgDataPolicy(token)
	}
	if err != nil {
		return fmt.Errorf("failed to get data review policy: %w", err)
	}
	if policy == nil {
		return fmt.Errorf("no data review policy found")
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	switch outputFormat {
	case "yaml", "json": //nolint:goconst // Keep output format handling local to this command.
		manifest := review_policies.DataPolicyToManifest(policy, datasetID)
		return encodeStructured(cmd, outputFormat, manifest)
	default:
		printContextBanner(cmd, ctx)
		setDefaultColumns(cmd, "source,requires_review,min_reviewers,designated_reviewers,auto_release_on_approval")
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return fmt.Errorf("failed to create output printer: %w", err)
		}
		return printer.Print(review_policies.ToDataPolicyDisplay(policy))
	}
}
