package review

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newUnassignCommand() *cobra.Command {
	var reviewers []string

	cmd := &cobra.Command{
		Use:   "unassign <review-id>",
		Short: "Unassign reviewers from a rule review",
		Long: `Unassign one or more reviewers. Works on open, changes_requested, and
approved reviews — removing an approver from an approved review retracts
their approval and can reopen it (the printed status comes from the
backend response).

-o json|yaml prints the review and moves the result line to stderr; -o name
prints rule-review/<uuid>.`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-review"), "table", "json", "yaml", "name"),
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
			ids, _, err := cmdutil.ResolveUsers(client.FetchUsers, ctx.Credential.AccessToken, reviewers)
			if err != nil {
				return err
			}
			detail, err := client.Unassign(ctx.Credential.AccessToken, args[0], ids)
			if err != nil {
				return mapReviewerError(err)
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "rule-review/%s reviewers unassigned (status: %s)\n",
				detail.ID, terminal.Sanitize(string(detail.Status)))
			return out.Write(detail, "rule-review/"+detail.ID.String())
		},
	}

	cmd.Flags().StringArrayVar(&reviewers, "reviewer", nil, "Reviewer: email, \"First Last\", or UUID (repeatable, required)")
	_ = cmd.MarkFlagRequired("reviewer")
	return cmd
}
