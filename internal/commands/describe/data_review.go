package describe

import (
	"fmt"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comment_threads"
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewDataReviewCommand creates the describe data-review command.
func NewDataReviewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data-review <review-id>",
		Short: "Show full detail of one row's data review",
		Long: `Show the full detail of one row's data review: its status and what is
holding the row, the submissions it belongs to, every changed cell with its
verdict, the reviewers' whole-row decisions, the discussion thread, and
status-aware next actions.

The review id must be the full UUID printed by 'qctl get data-reviews'. The
table is not a flag: the review names it, and the discussion is read through
that table's comment-thread route.

The default output is a self-describing text payload designed for both humans
and AI agents. Use -o json|yaml for the raw review response (with its changes);
the decisions and the thread are not part of it.

Examples:
  qctl describe data-review 0190bbbb-1111-2222-3333-444444444444
  qctl describe data-review 0190bbbb-1111-2222-3333-444444444444 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runDescribeDataReview,
	}
	return cmd
}

func runDescribeDataReview(cmd *cobra.Command, args []string) error {
	// Before Bootstrap: a malformed id is decidable locally, is bad usage
	// (exit 2), and must cost nothing.
	if _, err := cmdutil.ParseUUIDArg(args[0], "data review id"); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken
	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	review, err := client.Get(token, args[0], data_reviews.GetOptions{IncludeChanges: true, ChangesSinceMe: true})
	if err != nil {
		return fmt.Errorf("failed to get data review: %w", err)
	}

	if cmd.Flags().Changed("output") {
		outputFormat, _ := cmd.Flags().GetString("output")
		switch outputFormat {
		case outputFormatJSON, outputFormatYAML:
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, review)
		}
	}

	decisions, err := client.ListDecisions(token, args[0])
	if err != nil {
		return fmt.Errorf("failed to list data review decisions: %w", err)
	}

	view := data_reviews.DetailView{Review: review, Decisions: decisions}
	view.ApprovalProgress = lookUpApprovalProgress(cmd, client, token, review)
	// Nothing on the review's wire says who is asking, and the caller matters
	// only through their own standing decision — so a review nobody has
	// decided yet does not pay for users/me.
	if len(decisions) > 0 {
		view.Me = lookUpCaller(cmd, ctx)
	}
	// The directory only names users the review does not embed, and a role
	// without org:users:list is refused it, so it is swept only when someone
	// is still unnamed.
	if len(view.UnnamedUserIDs()) > 0 {
		view.Directory = cmdutil.FetchDisplayDirectory(cmd, client.FetchUsers, token)
	}
	if review.ThreadID != nil {
		threads := comment_threads.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
		// COMMENT_READ and table access are grants of their own, so the thread
		// is a section a reader of the review can still be refused: its failure
		// is a line in the payload, never the command's exit code.
		view.Thread, view.ThreadErr = threads.Get(token, review.DatasetID, review.ThreadID.String())
	}

	_, err = fmt.Fprint(cmd.OutOrStdout(), data_reviews.RenderDetail(&view))
	return err
}

func lookUpApprovalProgress(cmd *cobra.Command, client *data_reviews.Client, token string, review *data_reviews.Review) *data_reviews.RowOverlay {
	if review.Status != api.ReviewRequestStatusOpen && review.Status != api.ReviewRequestStatusChangesRequested {
		return nil
	}
	overlays, err := client.ByRows(token, review.DatasetID, []int{review.RowID})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: live approval count unavailable: %v\n", err)
		return nil
	}
	for _, overlay := range overlays {
		if overlay.ID == review.ID && overlay.RowID == review.RowID {
			return &overlay
		}
	}
	return nil
}

// lookUpCaller returns the caller's user id, or nil after one stderr warning:
// without it the payload only loses the lines about the caller's own decision.
func lookUpCaller(cmd *cobra.Command, ctx *cmdutil.CommandContext) *openapi_types.UUID {
	me, err := auth.NewClient(ctx.ServerURL, ctx.Verbosity).GetMe(ctx.Credential.AccessToken, ctx.OrganizationID)
	if err == nil {
		var id openapi_types.UUID
		if id, err = uuid.Parse(me.ID); err == nil {
			return &id
		}
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"Warning: could not identify you; omitting the lines about your own decision: %v\n", err)
	return nil
}
