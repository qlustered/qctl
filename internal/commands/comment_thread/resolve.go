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

func newResolveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolve <thread-id>",
		Short: "Mark a comment thread resolved",
		Long: `Mark a comment thread resolved.

A manual resolution stamps resolved_reason "manual", which is what tells it
apart from the backend sweep's "row_gone" — that one resolves a thread with
nobody having touched it, once every row it targets is gone. 'qctl describe
comment-thread' prints the reason on its Status line.

Resolving hides the thread from the by-rows overlay, which reports unresolved
threads only; the thread stays reachable by its id. Nothing is deleted and
'qctl comment-thread unresolve' puts it back.

Resolving requires comment:write and access to the table or its assigned
sources. External callers must have every target row on their assigned sources.

-o json|yaml prints the thread and moves the result line to stderr; -o name
prints comment-thread/<uuid>.

Examples:
  qctl comment-thread resolve 0190bbbb-1111-2222-3333-444444444444 --table orders
  qctl comment-thread resolve <thread-id> --table-id 42`,
		Annotations: cmdutil.WithFormats(catalog.For("comment-thread"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResolution(cmd, args, true)
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}

func newUnresolveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unresolve <thread-id>",
		Short: "Reopen a resolved comment thread",
		Long: `Reopen a resolved comment thread, clearing resolved_at, resolved_by and
resolved_reason.

This works on a thread the backend's sweep auto-resolved as "row_gone" just as
it does on a manual resolution — though a re-minted row id is not the row the
thread targets, so a swept thread usually stays swept.

Reopening puts the thread back in the by-rows overlay, which reports unresolved
threads only.

Reopening requires comment:write and access to the table or its assigned
sources. External callers must have every target row on their assigned sources.

-o json|yaml prints the thread and moves the result line to stderr; -o name
prints comment-thread/<uuid>.

Examples:
  qctl comment-thread unresolve 0190bbbb-1111-2222-3333-444444444444 --table orders
  qctl comment-thread unresolve <thread-id> --table-id 42`,
		Annotations: cmdutil.WithFormats(catalog.For("comment-thread"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResolution(cmd, args, false)
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}

// runResolution is the shared body of resolve and unresolve. Both answer with
// the thread's STATE and an empty comments list by design
// (_to_thread_response(with_comments=False)), so this prints one line built
// from comment_count and never touches RenderThread — reusing that renderer
// would announce "0 comments" on a thread with twelve.
func runResolution(cmd *cobra.Command, args []string, resolve bool) error {
	// Decided locally, so it runs before Bootstrap and the table lookup: a
	// typo must cost nothing.
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

	verb, action := "resolved", client.Resolve
	if !resolve {
		verb, action = "reopened", client.Unresolve
	}
	thread, err := action(ctx.Credential.AccessToken, datasetID, args[0])
	if err != nil {
		return mapResolutionError(err, args[0])
	}
	count := 0
	if thread.CommentCount != nil {
		count = *thread.CommentCount
	}
	plural := "s"
	if count == 1 {
		plural = ""
	}
	out := cmdutil.NewResultWriter(cmd)
	fmt.Fprintf(out.Human, "comment-thread/%s %s (%d comment%s)\n",
		thread.ID, verb, count, plural)
	return out.Write(thread, "comment-thread/"+thread.ID.String())
}

// mapResolutionError adds advice for a rejected thread resolution or reopening.
func mapResolutionError(err error, threadID string) error {
	var apiErr *comment_threads.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w\n(resolving or reopening a thread needs comment:write and access to this table or its assigned sources; "+
			"external callers must have every target row on their assigned sources)", err)
	}
	return cmdutil.HintOnNotFound(err, fmt.Sprintf(cmdutil.CommentThreadNotFoundHint, threadID))
}
