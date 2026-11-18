package review

import (
	"errors"
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newAssignCommand() *cobra.Command {
	var reviewers []string

	cmd := &cobra.Command{
		Use:   "assign <review-id>",
		Short: "Assign reviewers to a rule review",
		Long: `Assign one or more reviewers to an open or changes_requested review.

If the effective policy designates specific reviewers, only those users can
be assigned.

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
			detail, err := client.Assign(ctx.Credential.AccessToken, args[0], ids)
			if err != nil {
				return mapReviewerError(err)
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "rule-review/%s reviewers assigned (status: %s)\n",
				detail.ID, terminal.Sanitize(string(detail.Status)))
			return out.Write(detail, "rule-review/"+detail.ID.String())
		},
	}

	cmd.Flags().StringArrayVar(&reviewers, "reviewer", nil, "Reviewer: email, \"First Last\", or UUID (repeatable, required)")
	_ = cmd.MarkFlagRequired("reviewer")
	return cmd
}

// mapReviewerError handles the codes shared by assign/unassign.
func mapReviewerError(err error) error {
	var apiErr *rule_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.Code == "REVIEWER_NOT_DESIGNATED" {
		return fmt.Errorf(
			"the effective review policy designates specific reviewers, and a requested reviewer is not one of them: %w", err)
	}
	return err
}
