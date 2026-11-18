package review

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/spf13/cobra"
)

func newDeleteCommentCommand() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete-comment <review-id> <comment-id>",
		Short: "Delete a comment on a rule review",
		Long: `Delete a comment (author or org admin).

Leaf comments are removed outright. Comments with replies are tombstoned —
the body becomes "[comment deleted]" and the thread structure is preserved.

Prompts for confirmation unless --yes.`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("rule-review"), "table"), "rule-review", "-"),
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateReviewAndCommentIDs(args); err != nil {
				return err
			}
			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo(cmd.ErrOrStderr(), fmt.Sprintf("Delete comment %s?", args[1]))
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err := client.DeleteComment(ctx.Credential.AccessToken, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rule-review/%s comment %s deleted\n", args[0], args[1])
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	return cmd
}
