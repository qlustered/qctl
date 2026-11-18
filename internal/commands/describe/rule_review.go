package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/spf13/cobra"
)

// NewRuleReviewCommand creates the describe rule-review command.
func NewRuleReviewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-review <review-id>",
		Short: "Show full detail of a rule review",
		Long: `Show the full detail of a rule review: status, effective policy,
reviewers, linked dry run (with the enable-gate check), and status-aware next
actions.

The default output is a self-describing text payload designed for both
humans and AI agents; the review id must be the full UUID (as printed by
'qctl get rule-reviews' or 'qctl review request').

Use -o json|yaml for the raw API response.

New to rule reviews? Run:
  qctl guide review-rules`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-review"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateUUIDArgs(args, "review id"); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			detail, err := client.Get(ctx.Credential.AccessToken, args[0])
			if err != nil {
				return fmt.Errorf("failed to get rule review: %w", err)
			}

			outputFormat, _ := cmd.Flags().GetString("output")
			if cmd.Flags().Changed("output") {
				switch outputFormat {
				case outputFormatJSON, outputFormatYAML:
					return output.WriteRaw(cmd.OutOrStdout(), outputFormat, detail)
				}
			}

			// Default: agent-oriented markdown payload.
			_, err = fmt.Fprint(cmd.OutOrStdout(), rule_reviews.RenderDetail(detail))
			return err
		},
	}
	return cmd
}
