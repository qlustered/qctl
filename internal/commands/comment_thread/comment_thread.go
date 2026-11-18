// Package comment_thread implements the `qctl comment-thread` command group —
// discussions anchored to cells or rows of a table.
//
// Rule-review discussions are NOT here. They live on their own routes and keep
// their own verbs (qctl review comment|edit-comment|delete-comment); the
// backend answers 404 for a rule-review thread on every route this package
// calls. Do not reroute them.
//
//nolint:revive // Package name intentionally matches the comment-thread command directory.
package comment_thread

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `comment-thread` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment-thread",
		Short: "Discuss the cells and rows of a table",
		Long: `Discuss the cells and rows of a table.

A thread is anchored to targets — whole rows, or named fields of named rows —
and is created with its first comment. Every subcommand is dataset-scoped, so
the table is named with --table <name> or --table-id <id>.

Threads are read with the get/describe verbs, which need the ids up front:
  qctl get data orders --include-comment-threads   # the rows' threads
  qctl get comment-threads --table orders <id>...  # summary rows
  qctl describe comment-thread <id> --table orders # the full transcript

Rule review discussions are NOT here — they keep their own verbs
('qctl review comment|edit-comment|delete-comment'), and these routes answer
404 for them by design.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newCreateCommand())
	cmd.AddCommand(newReplyCommand())
	cmd.AddCommand(newEditCommentCommand())
	cmd.AddCommand(newDeleteCommentCommand())
	cmd.AddCommand(newResolveCommand())
	cmd.AddCommand(newUnresolveCommand())
	return cmd
}
