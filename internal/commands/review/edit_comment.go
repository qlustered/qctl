package review

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/spf13/cobra"
)

func newEditCommentCommand() *cobra.Command {
	var (
		body     string
		bodyFile string
	)

	cmd := &cobra.Command{
		Use:   "edit-comment <review-id> <comment-id>",
		Short: "Edit your own comment on a rule review",
		Long: `Replace the body of a comment you authored.

Author-only — admins cannot edit others' comments (403 otherwise). There is
no time window. The comment is flagged (edited) in the thread; mentions added
by the new body notify.

-o json|yaml prints the edited comment and moves the result line to stderr.

Examples:
  qctl review edit-comment <review-id> <comment-uuid> --body "Fixed, PTAL"
  qctl review edit-comment <review-id> <comment-uuid> --body-file notes.md`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("rule-review"), "table", "json", "yaml"), "rule-review", "-"),
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateReviewAndCommentIDs(args); err != nil {
				return err
			}
			text, err := cmdutil.ReadBodyFlags(cmd, body, bodyFile)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireCommentBody(text); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			edited, err := client.EditComment(ctx.Credential.AccessToken, args[0], args[1], text)
			if err != nil {
				return err
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "rule-review/%s comment %s edited\n", args[0], edited.ID)
			return out.Write(edited)
		},
	}

	cmd.Flags().StringVar(&body, "body", "", "New comment body (markdown, 1-10000 chars)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Read the body from a file (\"-\" = stdin)")
	return cmd
}

// validateReviewAndCommentIDs rejects a <review-id> <comment-id> pair unless
// both are full UUIDs.
func validateReviewAndCommentIDs(args []string) error {
	if err := cmdutil.ValidateUUIDArgs(args[:1], "review id"); err != nil {
		return err
	}
	return cmdutil.ValidateUUIDArgs(args[1:], "comment id")
}
