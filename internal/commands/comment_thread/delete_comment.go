//nolint:revive // Package name intentionally matches the comment-thread command directory.
package comment_thread

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comment_threads"
	"github.com/spf13/cobra"
)

func newDeleteCommentCommand() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete-comment <thread-id> <comment-id>",
		Short: "Delete your own comment on a thread",
		Long: `Delete one comment from a thread. Author-only (403 otherwise).

What the backend does depends on the comment: a leaf comment is removed
outright, while one that has replies is tombstoned — it stays in the
transcript with its body rewritten to "[comment deleted]" so the replies
underneath keep their place.

This deletes a COMMENT, never the thread: the backend has no delete-thread
route at all.

Prompts for confirmation unless --yes.

Examples:
  qctl comment-thread delete-comment <thread-id> <comment-id> --table orders
  qctl comment-thread delete-comment <thread-id> <comment-id> --table-id 42 --yes`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("comment-thread"), "table"), "comment-thread", "-"),
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateThreadAndCommentIDs(args); err != nil {
				return err
			}
			if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
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
			datasetID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
			if err != nil {
				return err
			}
			client := comment_threads.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err := client.DeleteComment(ctx.Credential.AccessToken, datasetID, args[0], args[1]); err != nil {
				return mapCommentChangeError(err, args[0], args[1])
			}
			fmt.Fprintf(cmd.OutOrStdout(), "comment-thread/%s comment %s deleted\n", args[0], args[1])
			return nil
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	return cmd
}
