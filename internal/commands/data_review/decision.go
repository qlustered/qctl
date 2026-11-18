package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// maxBatchReviewIDs is the backend's bound on both bulk selectors
// (MAX_BATCH_DECISION_IDS, atlas capi/capi/schemas/data_review_schema.py).
const maxBatchReviewIDs = 500

// The skip error codes the bulk routes emit (atlas _execute_bulk_decision).
// REVIEW_NOT_OPEN is the backend's catch-all, so its reason is the only
// reliable account of what happened.
const (
	skipNotFound              = "NOT_FOUND"
	skipViewOnly              = "VIEW_ONLY"
	skipApproverNotDesignated = "APPROVER_NOT_DESIGNATED"
	skipReviewNotOpen         = "REVIEW_NOT_OPEN"
)

// maxReasonsInError caps how many distinct skip reasons an error message
// quotes; every skipped item already has its own stdout line.
const maxReasonsInError = 3

// bulkDecision describes one of approve/withdraw — two verbs over one runner,
// because the two routes share a selector, a response and a partition rule.
type bulkDecision struct {
	verb       string // the subcommand name
	done       string // the action label on a processed line
	ask        string // the --group prompt's opening words
	comment    bool   // registers --comment; withdraw's route has no comment
	warnIfHeld bool   // a processed changes_requested item earns a stderr line
	send       func(c *data_reviews.Client, token string, sel data_reviews.BatchSelector, comment string) (*data_reviews.BatchResult, error)
}

func newBulkDecisionCommand(d bulkDecision, short, long string) *cobra.Command {
	cmd := &cobra.Command{
		Use:         d.verb + " [<review-id>...]",
		Short:       short,
		Long:        long,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml"),
		Args:        cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBulkDecision(cmd, args, d)
		},
	}
	cmdutil.RefString(cmd.Flags(), "group", "data-review-group",
		"Decide every open or changes_requested review in this submission-group UUID instead of listing ids (prompts unless --yes)")
	if d.comment {
		cmd.Flags().String("comment", "", "Comment recorded with the decision on every review in the call")
	}
	cmd.Flags().Bool("yes", false, "Skip the --group confirmation prompt (explicit ids never prompt)")
	return cmd
}

func runBulkDecision(cmd *cobra.Command, args []string, d bulkDecision) error {
	selector, err := bulkSelector(cmd, args)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	// Only --group is gated. An id list is exact, as on `review approve` and in
	// the web UI's bulk approve; a group expands server-side to rows the caller
	// has not seen. The prompt is unsized because sizing needs the group's
	// table, which the group UUID does not carry.
	if selector.GroupID != "" {
		yes, _ := cmd.Flags().GetBool("yes")
		if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, groupNonInteractive(d, selector.GroupID), groupPrompt(d, selector.GroupID)); err != nil {
			return err
		}
	}

	comment := ""
	if d.comment {
		comment, _ = cmd.Flags().GetString("comment")
	}
	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	result, err := d.send(client, ctx.Credential.AccessToken, selector, comment)
	if err != nil {
		return mapBulkDecisionError(err)
	}

	if format, ok := structuredOutput(cmd); ok {
		if err := output.WriteRaw(cmd.OutOrStdout(), format, result); err != nil {
			return err
		}
	} else {
		printBulkResult(cmd, d, selector, result)
	}
	return bulkOutcome(result)
}

// bulkSelector turns the positional ids or --group into the one selector the
// body carries, refusing everything the backend would 422 before a request is
// built: both or neither, a malformed UUID, more than the cap.
func bulkSelector(cmd *cobra.Command, args []string) (data_reviews.BatchSelector, error) {
	hasGroup := cmd.Flags().Changed("group")
	switch {
	case hasGroup && len(args) > 0:
		return data_reviews.BatchSelector{}, qerrors.NewBadUsage(
			"give data review ids or --group, not both")
	case !hasGroup && len(args) == 0:
		return data_reviews.BatchSelector{}, qerrors.NewBadUsage(
			"give at least one data review id, or --group <uuid> for a whole submission")
	case hasGroup:
		groupID, err := groupFlag(cmd)
		if err != nil {
			return data_reviews.BatchSelector{}, err
		}
		return data_reviews.BatchSelector{GroupID: groupID}, nil
	}

	seen := make(map[uuid.UUID]bool, len(args))
	ids := make([]string, 0, len(args))
	for _, raw := range args {
		id, err := uuid.Parse(raw)
		if err != nil {
			return data_reviews.BatchSelector{}, qerrors.NewBadUsagef(
				"invalid data review id %q: expected a full UUID", raw)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id.String())
		}
	}
	if len(ids) > maxBatchReviewIDs {
		return data_reviews.BatchSelector{}, qerrors.NewBadUsagef(
			"%d distinct data review ids given; one call takes at most %d, so split the list",
			len(ids), maxBatchReviewIDs)
	}
	return data_reviews.BatchSelector{ReviewIDs: ids}, nil
}

func groupPreview(groupID string) string {
	return "qctl get data-reviews --table <name> --group " + groupID + " [--state rejected|accepted]"
}

func groupPrompt(d bulkDecision, groupID string) string {
	return fmt.Sprintf("%s every open or changes_requested review in submission %s (up to %d rows)?\nPreview: %s\nProceed?",
		d.ask, groupID, maxBatchReviewIDs, groupPreview(groupID))
}

// groupNonInteractive names --yes, because that is the fix, and the preview,
// because a script should look before it passes --yes.
func groupNonInteractive(d bulkDecision, groupID string) string {
	return fmt.Sprintf("%s --group needs confirmation but stdin is not an interactive terminal; "+
		"pass --yes (preview first: %s)", d.verb, groupPreview(groupID))
}

// mapBulkDecisionError explains the route-level refusal. The two routes
// share one door, so a permission 403 names that permission; the skip codes
// never reach here because they ride a 200.
func mapBulkDecisionError(err error) error {
	var apiErr *data_reviews.APIError
	if errors.As(err, &apiErr) && refusedForPermission(apiErr) {
		return fmt.Errorf("%w\n(whole-row decisions, including withdraw, require the DATA_REVIEW_APPROVE "+
			"permission; the editor and agent roles do not hold it)", err)
	}
	return err
}

func printBulkResult(cmd *cobra.Command, d bulkDecision, selector data_reviews.BatchSelector, result *data_reviews.BatchResult) {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	for _, p := range result.Processed {
		fmt.Fprintf(out, "data-review/%s %s (status: %s, approvals: %d/%d, released: %d row(s))\n",
			p.DataReviewID, d.done, terminal.Sanitize(string(p.Status)),
			p.CurrentApprovals, p.RequiredApprovals, len(p.ReleasedRowIds))
		if d.warnIfHeld && p.Status == api.ReviewRequestStatusChangesRequested {
			fmt.Fprintf(errOut, "warning: data-review/%s approval recorded, but the row is still held"+
				" — a cell needs a fix or the row was rejected\n", p.DataReviewID)
		}
	}
	for _, s := range result.Skipped {
		fmt.Fprintf(out, "data-review/%s skipped: %s [%s]\n",
			s.DataReviewID, terminal.Sanitize(s.Reason), terminal.Sanitize(s.ErrorCode))
	}
	if len(result.Processed) == 0 && len(result.Skipped) == 0 && selector.GroupID != "" {
		fmt.Fprintf(errOut, "submission %s has no open reviews; nothing to %s\n", selector.GroupID, d.verb)
	}
	if len(selector.ReviewIDs) == 1 && len(result.Processed) == 1 {
		fmt.Fprintf(out, "Next: qctl describe data-review %s\n", result.Processed[0].DataReviewID)
	}
}

// bulkOutcome grades a 200. A partial is not a success: like `share table`,
// any skip exits non-zero. When nothing was processed and every skip agrees on
// why, the exit code says why; any other mix is exit 1 and names the split.
func bulkOutcome(result *data_reviews.BatchResult) error {
	skipped := result.Skipped
	if len(skipped) == 0 {
		return nil
	}
	if len(result.Processed) == 0 {
		if code, ok := sharedSkipCode(skipped); ok {
			reasons := skipReasons(skipped)
			switch code {
			case skipNotFound:
				// The backend also answers NOT_FOUND for a review whose table
				// the caller cannot reach, so the organization is worth naming.
				return qerrors.New(qerrors.ExitNotFound, apierror.WithScopeNote(reasons))
			case skipViewOnly, skipApproverNotDesignated:
				return qerrors.New(qerrors.ExitUnauthorized, reasons)
			case skipReviewNotOpen:
				return qerrors.New(qerrors.ExitBadUsage, reasons)
			default:
				return qerrors.New(qerrors.ExitGenericError, reasons)
			}
		}
	}
	return qerrors.New(qerrors.ExitGenericError, fmt.Sprintf(
		"%d of %d review(s) skipped; the other %d processed",
		len(skipped), len(skipped)+len(result.Processed), len(result.Processed)))
}

func sharedSkipCode(skipped []data_reviews.BatchSkipped) (string, bool) {
	code := skipped[0].ErrorCode
	for _, s := range skipped[1:] {
		if s.ErrorCode != code {
			return "", false
		}
	}
	return code, true
}

// skipReasons quotes the distinct reasons in served order, capped.
func skipReasons(skipped []data_reviews.BatchSkipped) string {
	seen := map[string]bool{}
	var reasons []string
	for _, s := range skipped {
		if !seen[s.Reason] {
			seen[s.Reason] = true
			reasons = append(reasons, terminal.Sanitize(s.Reason))
		}
	}
	msg := strings.Join(reasons[:min(len(reasons), maxReasonsInError)], "; ")
	if extra := len(reasons) - maxReasonsInError; extra > 0 {
		msg += fmt.Sprintf("; and %d more", extra)
	}
	return msg
}
