package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/review_policies"
	"github.com/spf13/cobra"
)

// NewChangeReviewPolicyCommand creates the get change-review-policy command.
func NewChangeReviewPolicyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change-review-policy",
		Short: "Show the change review policy, or a template to start one from",
		Long: `Show the organization's change review policy, the policy in force for a
table, or a template granting every registered key to the roles allowed it.

A change review policy is a whitelist: for each governed operation key it
lists the roles that may resolve the change directly, propose it for review,
and approve a proposal. While a policy is in force, a key it does not list is
denied to every role.

Without a table selector this prints the organization policy. With --table or
--table-id it prints the effective policy for that table (the organization
policy with the table's entries laid over it, each table entry replacing the
organization's whole entry for that key) and the keys it does not list.

The table view prints one row per key. -o yaml prints an apply-compatible
manifest; --template prints the server's template as one, in YAML unless -o
is given.

Examples:
  qctl get change-review-policy
  qctl get change-review-policy --table orders
  qctl get change-review-policy --table-id 42 -o yaml
  qctl get change-review-policy --template > change-review-policy.yaml`,
		Annotations: cmdutil.WithFormats(catalog.For("change-review-policy"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runGetChangeReviewPolicy,
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().Bool("template", false, "Print the server's policy template: every registered key granted to the roles allowed it")
	cmdutil.DeclareColumns(cmd, []review_policies.ChangeReviewEntryDisplay(nil))
	return cmd
}

func runGetChangeReviewPolicy(cmd *cobra.Command, args []string) error {
	if err := cmdutil.ValidateConflictingFlags(cmd,
		[]string{"template", "table"}, []string{"template", "table-id"}); err != nil {
		return err
	}
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken
	client := review_policies.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	outputFormat, _ := cmd.Flags().GetString("output")

	if template, _ := cmd.Flags().GetBool("template"); template {
		raw, err := client.GetChangePolicyTemplate(token)
		if err != nil {
			return fmt.Errorf("failed to get change review policy template: %w", err)
		}
		manifest, err := review_policies.ChangeTemplateToManifest(raw)
		if err != nil {
			return err
		}
		if !cmd.Flags().Changed("output") {
			outputFormat = "yaml" //nolint:goconst // Keep output format handling local to this command.
		}
		return printChangeReviewPolicy(cmd, ctx, outputFormat, manifest, nil,
			fmt.Sprintf("Template: require_distinct_approver: %t", *manifest.Spec.RequireDistinctApprover))
	}

	// The "was it set" return is discarded deliberately: no selector means
	// the organization policy, which datasetID == 0 already selects.
	datasetID, _, err := cmdutil.OptionalTableID(ctx, cmd, "")
	if err != nil {
		return err
	}
	if datasetID == 0 {
		policy, err := client.GetOrgChangePolicy(token)
		if err != nil {
			return fmt.Errorf("failed to get change review policy: %w", err)
		}
		if policy == nil {
			return qerrors.NewNotFound(apierror.WithScopeNote("the organization has no change review policy; " +
				"start one from `qctl get change-review-policy --template`"))
		}
		return printChangeReviewPolicy(cmd, ctx, outputFormat, review_policies.ChangePolicyToManifest(policy), nil,
			fmt.Sprintf("Source: organization | require_distinct_approver: %t", derefBool(policy.RequireDistinctApprover)))
	}

	eff, err := client.GetEffectiveChangePolicy(token, datasetID)
	if err != nil {
		return fmt.Errorf("failed to get change review policy: %w", err)
	}
	if !eff.InForce {
		return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("no change review policy is in force for table %d: "+
			"neither the organization nor the table has one, so every change is allowed", datasetID)))
	}
	manifest := review_policies.EffectiveChangePolicyToManifest(eff)
	return printChangeReviewPolicy(cmd, ctx, outputFormat, manifest, eff.Uncovered,
		fmt.Sprintf("Source: %s | require_distinct_approver: %t | unlisted keys: %d",
			manifest.Status.Source, derefBool(eff.RequireDistinctApprover),
			review_policies.UncoveredKeyCount(eff.Uncovered)))
}

// printChangeReviewPolicy writes the manifest under -o json|yaml; under the
// table view it writes summary to stderr, then one row per key. A non-nil
// uncovered adds the LISTED column and a row per unlisted key.
func printChangeReviewPolicy(cmd *cobra.Command, ctx *cmdutil.CommandContext, outputFormat string,
	manifest *review_policies.ChangeReviewPolicyManifest, uncovered map[string][][]string, summary string) error {
	switch outputFormat {
	case "yaml", "json": //nolint:goconst // Keep output format handling local to this command.
		return encodeStructured(cmd, outputFormat, manifest)
	}
	printContextBanner(cmd, ctx)
	columns := "operation,key,resolve,propose,approve"
	if uncovered != nil {
		columns = "operation,key,listed,resolve,propose,approve"
	}
	setDefaultColumns(cmd, columns)
	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	fmt.Fprintln(cmd.ErrOrStderr(), summary)
	return printer.Print(review_policies.ToChangeReviewEntryDisplays(manifest.Spec.Operations, uncovered))
}

func derefBool(b *bool) bool {
	return b != nil && *b
}
