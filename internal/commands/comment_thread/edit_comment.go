//nolint:revive // Package name intentionally matches the comment-thread command directory.
package comment_thread

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comment_threads"
	"github.com/spf13/cobra"
)

func newEditCommentCommand() *cobra.Command {
	var (
		body     string
		bodyFile string
	)

	cmd := &cobra.Command{
		Use:   "edit-comment <thread-id> <comment-id>",
		Short: "Edit your own comment on a thread",
		Long: `Replace the body of a comment you authored.

Author-only — nobody else can edit your comment and you cannot edit theirs
(403 otherwise), and there is no time window. The comment is flagged (edited)
in the transcript, and mentions added by the new body notify.

Both ids are full UUIDs: the thread id, then the comment id, which is the
[c: <uuid>] tag 'qctl describe comment-thread' prints.

-o json|yaml prints the edited comment and moves the result line to stderr.

Examples:
  qctl comment-thread edit-comment <thread-id> <comment-id> --table orders --body "fixed, PTAL"
  qctl comment-thread edit-comment <thread-id> <comment-id> --table-id 42 --body-file notes.md`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("comment-thread"), "table", "json", "yaml"), "comment-thread", "-"),
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateThreadAndCommentIDs(args); err != nil {
				return err
			}
			text, err := cmdutil.ReadBodyFlags(cmd, body, bodyFile)
			if err != nil {
				return err
			}
			if err := cmdutil.ValidateCommentBody(text); err != nil {
				return err
			}
			if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
			if err != nil {
				return err
			}
			client := comment_threads.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			edited, err := client.EditComment(ctx.Credential.AccessToken, datasetID, args[0], args[1], text)
			if err != nil {
				return mapCommentChangeError(err, args[0], args[1])
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "comment-thread/%s comment %s edited\n", args[0], edited.ID)
			return out.Write(edited)
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().StringVar(&body, "body", "", "New comment body (markdown, 1-10000 chars)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Read the body from a file (\"-\" = stdin)")
	return cmd
}

// validateThreadAndCommentIDs settles both positional ids before Bootstrap and
// the table lookup, both of which cost a request. The two are named separately
// so the message says which one is wrong.
func validateThreadAndCommentIDs(args []string) error {
	if err := cmdutil.ValidateUUIDArgs(args[:1], "comment thread id"); err != nil {
		return err
	}
	return cmdutil.ValidateUUIDArgs(args[1:], "comment id")
}

// mapCommentChangeError adds advice for a rejected comment edit or deletion.
func mapCommentChangeError(err error, threadID, commentID string) error {
	var apiErr *comment_threads.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w\n(editing or deleting a comment needs comment:write and access to this table or its assigned sources; "+
			"only the comment's author can edit or delete it)", err)
	}
	hint := fmt.Sprintf("no comment %s in this thread, or %s",
		commentID, fmt.Sprintf(cmdutil.CommentThreadNotFoundHint, threadID))
	return cmdutil.HintOnNotFound(err, hint)
}
