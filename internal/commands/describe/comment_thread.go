package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comment_threads"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewCommentThreadCommand creates the describe comment-thread command.
func NewCommentThreadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment-thread <thread-id>",
		Short: "Show one comment thread and its full transcript",
		Long: `Show one comment thread on a table: what it targets, who opened it,
whether it is resolved, its duplicate group when present, and every comment
with its replies.

The default output is a self-describing text payload designed for both humans
and AI agents. Each comment carries a [c: <uuid>] tag, which is the comment id
'qctl comment-thread reply|edit-comment|delete-comment' take verbatim.

The thread id must be a full UUID, and the table it lives on must be named with
--table or --table-id: the thread routes are dataset-scoped, so there is no
org-wide lookup. Rule-review discussions are NOT reachable here — read them
with 'qctl describe rule-review <review-id>'.

Use -o json|yaml for the raw API response.

Examples:
  qctl describe comment-thread 0190bbbb-1111-2222-3333-444444444444 --table orders
  qctl describe comment-thread 0190bbbb-1111-2222-3333-444444444444 --table-id 42 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("comment-thread"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before Bootstrap and the table lookup: a typo in the id is
			// decidable locally, is bad usage (exit 2), and must cost nothing.
			if err := cmdutil.ValidateUUIDArgs(args, "comment thread id"); err != nil {
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
			thread, err := client.Get(ctx.Credential.AccessToken, datasetID, args[0])
			if err != nil {
				return cmdutil.HintOnNotFound(err, fmt.Sprintf(cmdutil.CommentThreadNotFoundHint, args[0]))
			}

			outputFormat, _ := cmd.Flags().GetString("output")
			if cmd.Flags().Changed("output") {
				switch outputFormat {
				case outputFormatJSON, outputFormatYAML:
					return output.WriteRaw(cmd.OutOrStdout(), outputFormat, thread)
				}
			}

			// Default: agent-oriented transcript payload.
			_, err = fmt.Fprint(cmd.OutOrStdout(), comment_threads.RenderThread(thread))
			return err
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}
