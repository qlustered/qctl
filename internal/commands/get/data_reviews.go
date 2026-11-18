package get

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/dataview"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

const dataReviewRowColumns = "id,row,status,requester,approvals,held,undecided,blocking,comments,cannot_approve"

// noActiveReview fills every review-derived cell of a row the by-rows overlay
// did not return: the row has no open or changes_requested review, so there is
// no id to act on and no count to report. It also fills the approvals cell of
// an overlay that lacks either approval count.
const noActiveReview = "-"

var dataReviewRowStates = map[string]api.ReviewStatusFilter{
	"awaiting_decision": api.AwaitingDecision,
	"rejected":          api.Rejected,
	"accepted":          api.Accepted,
}

type dataReviewRow struct {
	ID            string `json:"id"`
	Row           int    `json:"row"`
	Status        string `json:"status"`
	Requester     string `json:"requester"`
	Requests      string `json:"requests"`
	Approvals     string `json:"approvals"`
	Held          string `json:"held"`
	Undecided     string `json:"undecided"`
	Blocking      string `json:"blocking"`
	Comments      string `json:"comments"`
	CannotApprove string `json:"cannot_approve"`
}

// NewDataReviewsCommand creates the get data-reviews command.
func NewDataReviewsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data-reviews",
		Short: "List a table's quarantined rows in one review bucket, with their active review ids",
		Long: `List one page of a table's quarantined rows in one review bucket, each joined
with its active data review.

--state picks the bucket (awaiting_decision by default, the reviewer's queue).
The id column is the active review's UUID, the id every data-review verb takes;
it is - when the row has no open or changes_requested review, which is most of
the accepted bucket. approvals is <current>/<required>: the distinct
reviewers who approved the row's current version, not counting anyone who
edited the row or requested its review, against the approvals the review
requires. undecided counts the row's changes that carry neither an approve nor
a blocking verdict; blocking counts reject and request_changes. requester
is who asked for the review; the requests column, off by default, lists the
ids of every request (submission group) the review is in, for
qctl data-review-group.

cannot_approve is self when you edited the row or submitted it for review;
another approver must approve it. A dash does not establish eligibility:
approval permission and reviewer designation are separate checks.

Rows are listed in ascending row id, page by page. -o json and -o yaml emit a
List whose items are the by-rows overlays of the page's rows that have an
active review, approvals as current_approvals and required_approvals, and
whose total counts the bucket's rows, not reviews; -o name prints
data-review/<uuid> for the rows that have an active review.`,
		Annotations: catalog.For("data-review"),
		Args:        cobra.NoArgs,
		RunE:        runDataReviews,
	}
	cmdutil.AddTableFlags(cmd, "Review queue's table")
	cmd.Flags().String("state", "awaiting_decision", "Review bucket (awaiting_decision, rejected, accepted)")
	cmdutil.RefString(cmd.Flags(), "group", "data-review-group", "Only rows in this submission group (full UUID)")
	cmd.Flags().Int("page", 1, "Page number to fetch")
	cmd.Flags().Int("limit", 20, "Maximum rows to return")
	cmdutil.DeclareColumns(cmd, []dataReviewRow(nil))
	return cmd
}

func runDataReviews(cmd *cobra.Command, _ []string) error {
	params, err := dataReviewParams(cmd)
	if err != nil {
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

	page, rowIDs, overlays, err := fetchDataReviews(ctx, datasetID, params)
	if err != nil {
		return err
	}
	return printDataReviews(cmd, ctx, params, page, rowIDs, overlays)
}

// fetchDataReviews makes the command's two requests: the data page, then the
// by-rows overlay over its row ids, which ByRows skips for an empty page.
func fetchDataReviews(
	ctx *cmdutil.CommandContext,
	datasetID int,
	params *api.GetDataParams,
) (*datasets.DataPage, []int, []data_reviews.RowOverlay, error) {
	token := ctx.Credential.AccessToken
	page, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		GetData(token, datasetID, params)
	if err != nil {
		return nil, nil, nil, dataReviewEndpointError(err, "data endpoint",
			api.OperationPermissions["get_data"]+" and access to the table or assigned sources")
	}
	rowIDs, err := dataview.RowIDs(page)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := dataview.ValidateSelfExclusion(page); err != nil {
		return nil, nil, nil, err
	}
	overlays, err := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		ByRows(token, datasetID, rowIDs)
	if err != nil {
		return nil, nil, nil, dataReviewEndpointError(err, "by-rows endpoint",
			api.OperationPermissions["data_reviews_by_rows"]+" and access to the table or assigned sources")
	}
	return page, rowIDs, overlays, nil
}

func printDataReviews(
	cmd *cobra.Command,
	ctx *cmdutil.CommandContext,
	params *api.GetDataParams,
	page *datasets.DataPage,
	rowIDs []int,
	overlays []data_reviews.RowOverlay,
) error {
	excluded := make(map[int]bool)
	if page.SentOrChangedByMe != nil {
		for i, rowID := range rowIDs {
			excluded[rowID] = (*page.SentOrChangedByMe)[i]
		}
	}
	slices.Sort(rowIDs)
	byRow := make(map[int]data_reviews.RowOverlay, len(overlays))
	for _, overlay := range overlays {
		byRow[overlay.RowID] = overlay
	}
	if cmd.Flags().Changed("output") {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			lp := listPage{page: params.Page, limit: params.Limit, total: &page.TotalRows}
			return output.WriteRaw(cmd.OutOrStdout(), format,
				output.NewList(overlays, lp.page, lp.limit, lp.total, lp.nextPage()))
		case "name": //nolint:goconst // Keep output format handling local to this command.
			return printDataReviewNames(cmd, rowIDs, byRow)
		}
	}
	if len(rowIDs) == 0 {
		skip, err := printEmptyResultWithHint(cmd, ctx, "data-review rows", []dataReviewRow{},
			&page.TotalRows, func() string { return dataReviewStateHint(cmd) })
		if err != nil || skip {
			return err
		}
	}

	printContextBanner(cmd, ctx)
	if err := tableui.PrintFromCmd(cmd, dataReviewRows(rowIDs, byRow, excluded), dataReviewRowColumns); err != nil {
		return err
	}
	if requestedPage := *params.Page; requestedPage < page.TotalPageCount {
		fmt.Fprintf(cmd.ErrOrStderr(), "More rows in this bucket — continue with --page %d.\n", requestedPage+1)
	}
	return nil
}

// dataReviewParams builds the one GET /data request this command makes, and
// refuses every bad flag before anything else runs. The review filters are
// valid only on the quarantine side, so is_bad_data is always sent; order_by=id
// makes the pages walk the queue in the same ascending row order each page is
// rendered in, instead of the endpoint's newest-first default.
func dataReviewParams(cmd *cobra.Command) (*api.GetDataParams, error) {
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return nil, err
	}
	stateLabel, _ := cmd.Flags().GetString("state")
	state, ok := dataReviewRowStates[stateLabel]
	if !ok {
		return nil, qerrors.NewBadUsagef(
			"invalid --state %q: expected awaiting_decision, rejected, or accepted", stateLabel)
	}
	isBadData := true
	orderBy := []string{"id"}
	params := &api.GetDataParams{
		Page:         &page,
		Limit:        &limit,
		IsBadData:    &isBadData,
		OrderBy:      &orderBy,
		ReviewStatus: &state,
	}
	if cmd.Flags().Changed("group") {
		value, _ := cmd.Flags().GetString("group")
		groupID, err := uuid.Parse(value)
		if err != nil {
			return nil, qerrors.NewBadUsagef("invalid --group %q: expected a full submission-group UUID", value)
		}
		params.ReviewGroupID = &groupID
	}
	return params, nil
}

// dataReviewEndpointError identifies a failed endpoint and preserves its cause.
func dataReviewEndpointError(err error, endpoint, needs string) error {
	if qerrors.GetExitCode(err) == qerrors.ExitUnauthorized {
		return fmt.Errorf("%s refused the request (it needs %s): %w", endpoint, needs, err)
	}
	return fmt.Errorf("%s failed: %w", endpoint, err)
}

// dataReviewStateHint names the bucket an empty result came from when the user
// did not pick it: a defaulted --state is not echoed among the active filters.
func dataReviewStateHint(cmd *cobra.Command) string {
	if cmd.Flags().Changed("state") {
		return ""
	}
	return "Searched the awaiting_decision bucket; --state rejected or --state accepted lists the others.\n"
}

func dataReviewRows(rowIDs []int, byRow map[int]data_reviews.RowOverlay, excluded map[int]bool) []dataReviewRow {
	rows := make([]dataReviewRow, 0, len(rowIDs))
	for _, rowID := range rowIDs {
		cannotApprove := noActiveReview
		if excluded[rowID] {
			cannotApprove = "self"
		}
		overlay, ok := byRow[rowID]
		if !ok {
			rows = append(rows, dataReviewRow{
				ID: noActiveReview, Row: rowID, Status: noActiveReview, Requester: noActiveReview,
				Requests: noActiveReview, Approvals: noActiveReview,
				Held: noActiveReview, Undecided: noActiveReview, Blocking: noActiveReview, Comments: noActiveReview,
				CannotApprove: cannotApprove,
			})
			continue
		}
		undecided, blocking := countOverlayChanges(overlay.Changes)
		row := dataReviewRow{
			ID:            overlay.ID.String(),
			Row:           rowID,
			Status:        string(overlay.Status),
			Requester:     noActiveReview,
			Requests:      overlayRequests(overlay),
			Approvals:     approvalProgress(overlay),
			Held:          noActiveReview,
			Undecided:     strconv.Itoa(undecided),
			Blocking:      strconv.Itoa(blocking),
			Comments:      "0",
			CannotApprove: cannotApprove,
		}
		if overlay.Requester != nil {
			row.Requester = comments.UserDisplay(overlay.Requester, overlay.Requester.ID)
		}
		if overlay.ReleaseBlockReason != nil {
			row.Held = string(*overlay.ReleaseBlockReason)
		}
		if overlay.CommentCount != nil {
			row.Comments = strconv.Itoa(*overlay.CommentCount)
		}
		rows = append(rows, row)
	}
	return rows
}

// overlayRequests joins the ids of the requests (submission groups) an
// overlay's review belongs to, or noActiveReview when it names none.
func overlayRequests(overlay data_reviews.RowOverlay) string {
	if overlay.ReviewGroupIds == nil || len(*overlay.ReviewGroupIds) == 0 {
		return noActiveReview
	}
	ids := make([]string, 0, len(*overlay.ReviewGroupIds))
	for _, id := range *overlay.ReviewGroupIds {
		ids = append(ids, id.String())
	}
	return strings.Join(ids, ",")
}

// approvalProgress renders an overlay's approval counts as
// "<current>/<required>", or noActiveReview when either count is absent.
func approvalProgress(overlay data_reviews.RowOverlay) string {
	if overlay.CurrentApprovals == nil || overlay.RequiredApprovals == nil {
		return noActiveReview
	}
	return fmt.Sprintf("%d/%d", *overlay.CurrentApprovals, *overlay.RequiredApprovals)
}

// countOverlayChanges splits a review's changes the way the release gate does:
// reject and request_changes block; approve decides; everything else — no
// decision, comment_only, or a verdict this client does not know — leaves the
// cell undecided.
func countOverlayChanges(changes *[]data_reviews.OverlayChange) (undecided, blocking int) {
	if changes == nil {
		return 0, 0
	}
	for _, change := range *changes {
		var decision api.ReviewDecisionEnum
		if change.Decision != nil {
			decision = *change.Decision
		}
		switch decision {
		case api.ReviewDecisionEnumApprove:
		case api.ReviewDecisionEnumReject, api.ReviewDecisionEnumRequestChanges:
			blocking++
		default:
			undecided++
		}
	}
	return undecided, blocking
}

func printDataReviewNames(cmd *cobra.Command, rowIDs []int, byRow map[int]data_reviews.RowOverlay) error {
	for _, rowID := range rowIDs {
		overlay, ok := byRow[rowID]
		if !ok {
			continue
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "data-review/%s\n", overlay.ID); err != nil {
			return err
		}
	}
	return nil
}
