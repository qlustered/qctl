package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/spf13/cobra"
)

// NewRuleReviewPolicyCommand creates the get rule-review-policy command.
func NewRuleReviewPolicyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-review-policy",
		Short: "Show the effective rule review policy",
		Long: `Show the effective rule review policy for the organization or a specific table.

The effective policy is resolved through the table -> organization -> system
default chain; the "source" column tells you which level it came from.

Use -o yaml to get an apply-compatible manifest for editing:

  qctl get rule-review-policy -o yaml > policy.yaml
  # edit policy.yaml
  qctl apply -f policy.yaml

Examples:
  qctl get rule-review-policy
  qctl get rule-review-policy --table orders
  qctl get rule-review-policy --table-id 42 -o yaml`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-review-policy"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runGetRuleReviewPolicy,
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmdutil.DeclareColumns(cmd, []review_policies.RulePolicyDisplay(nil))
	return cmd
}

func runGetRuleReviewPolicy(cmd *cobra.Command, args []string) error {
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
	var eff *review_policies.EffectiveRulePolicy
	if datasetID > 0 {
		eff, err = client.GetDatasetRulePolicy(token, datasetID)
	} else {
		eff, err = client.GetOrgRulePolicy(token)
	}
	if err != nil {
		return fmt.Errorf("failed to get rule review policy: %w", err)
	}
	if eff == nil {
		return fmt.Errorf("no rule review policy found")
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	switch outputFormat {
	case "yaml", "json": //nolint:goconst // Keep output format handling local to this command.
		manifest := review_policies.RuleEffectiveToManifest(eff, datasetID)
		return encodeStructured(cmd, outputFormat, manifest)
	default:
		printContextBanner(cmd, ctx)
		setDefaultColumns(cmd, "source,requires_review,min_reviewers,designated_reviewers,dismiss_stale_reviews,require_dry_run,require_dry_run_success,auto_enable_on_approval")
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return fmt.Errorf("failed to create output printer: %w", err)
		}
		return printer.Print(review_policies.ToRulePolicyDisplay(eff))
	}
}
