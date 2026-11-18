package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// errorCodePayloadMismatch is record-action's one coded refusal: the
// history id is not a change of the review's table (atlas
// _validate_history_containment_async).
const errorCodePayloadMismatch = "REVIEW_PAYLOAD_MISMATCH"

// cellVerdict describes a per-cell decision command.
type cellVerdict struct {
	verb     string                         // the subcommand name
	decision api.RecordActionSchemaDecision // the wire decision
	done     string                         // the action label on the result line
	// forbidden explains a 403. The route's 403s carry no error_code, so the
	// explanation is chosen by verb and never by the message text.
	forbidden string
}

const cellVerdictOutputDoc = `

-o json|yaml prints the server's {ledger_id, decision, affected_data_review_ids,
affected_review_statuses, affected_row_ids, released_row_ids} and moves the
result lines to stderr.`

func newCellVerdictCommand(v cellVerdict, short, long string) *cobra.Command {
	cmd := &cobra.Command{
		Use:         v.verb + " <review-id> (--field <name> | --history-id <id>)",
		Short:       short,
		Long:        long + cellVerdictOutputDoc,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCellVerdict(cmd, args[0], v)
		},
	}
	cmd.Flags().String("field", "",
		"The cell to decide, by field name (exact, case-sensitive; resolved against the review's changes)")
	cmdutil.RefInt(cmd.Flags(), "history-id", "history",
		"The cell to decide, by the change's history id (the [h: <id>] in describe data-review); skips the lookup")
	if v.decision != api.RecordActionSchemaDecisionWithdraw {
		cmd.Flags().String("comment", "", "Comment recorded with the verdict (replaces the cell's previous comment)")
	}
	return cmd
}

// cellSelector is the cell a verdict names: always a history id, plus the
// field name when the user gave one.
type cellSelector struct {
	field     string
	historyID int
}

func runCellVerdict(cmd *cobra.Command, rawID string, v cellVerdict) error {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return qerrors.NewBadUsagef("invalid data review id %q: expected a full UUID", rawID)
	}
	reviewID := id.String()
	sel, err := cellFlags(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	if sel.historyID == 0 {
		if sel.historyID, err = resolveField(client, token, reviewID, sel.field); err != nil {
			return err
		}
	}

	opts := data_reviews.ActionOptions{Decision: v.decision}
	if v.decision != api.RecordActionSchemaDecisionWithdraw {
		opts.Comment, _ = cmd.Flags().GetString("comment")
	}
	result, err := client.RecordAction(token, reviewID, sel.historyID, opts)
	if err != nil {
		return mapCellVerdictError(err, v, sel.historyID)
	}
	out := cmdutil.NewResultWriter(cmd)
	printCellResult(cmd, out.Human, v, id, sel, result)
	return out.Write(result)
}

// cellFlags enforces exactly one of --field and --history-id, both before any
// request: neither, both, an empty field and a non-positive id all exit 2.
func cellFlags(cmd *cobra.Command) (cellSelector, error) {
	names := []string{"field", "history-id"}
	if err := cmdutil.ValidateConflictingFlags(cmd, names); err != nil {
		return cellSelector{}, err
	}
	if err := cmdutil.RequireOneOfFlags(cmd, names); err != nil {
		return cellSelector{}, err
	}
	historyID, haveHistoryID, err := cmdutil.PositiveIDFlag(cmd, "history-id")
	if err != nil {
		return cellSelector{}, err
	}
	if haveHistoryID {
		return cellSelector{historyID: historyID}, nil
	}
	field, _ := cmd.Flags().GetString("field")
	if field == "" {
		return cellSelector{}, qerrors.NewBadUsage("--field must name a field")
	}
	return cellSelector{field: field}, nil
}

// resolveField finds the field's history id in the review's own change set.
// A field appears at most once there — the backend folds the change set by
// field name — so an exact match is the whole algorithm.
func resolveField(client *data_reviews.Client, token, reviewID, field string) (int, error) {
	review, err := client.Get(token, reviewID, data_reviews.GetOptions{IncludeChanges: true})
	if err != nil {
		var apiErr *data_reviews.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
			return 0, fmt.Errorf("%w\n(resolving --field reads the review, which requires DATA_REVIEW_LIST)", err)
		}
		return 0, err
	}
	var changes []data_reviews.Change
	if review.Changes != nil {
		changes = *review.Changes
	}
	if len(changes) == 0 {
		// The backend answers 200 with changes: [] to a caller without access
		// to the table's data, so the empty set cannot tell the two apart.
		return 0, qerrors.NewBadUsagef("no changes are visible on data-review/%s — the row has no live edits, "+
			"or you lack access to this table's data; pass --history-id if you know it", reviewID)
	}
	names := make([]string, 0, len(changes))
	for _, c := range changes {
		if c.FieldName == field {
			return c.HistoryID, nil
		}
		names = append(names, terminal.Sanitize(c.FieldName))
	}
	return 0, qerrors.NewBadUsagef("data-review/%s has no change to field %q; its changed fields are: %s",
		reviewID, terminal.Sanitize(field), strings.Join(names, ", "))
}

// mapCellVerdictError explains the route's refusals. %w keeps the exit code:
// 403 stays 4, 422 stays 2, 404 stays 3.
func mapCellVerdictError(err error, v cellVerdict, historyID int) error {
	var apiErr *data_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case refusedForPermission(apiErr):
		return fmt.Errorf("%w\n(%s)", err, v.forbidden)
	case apiErr.Code == errorCodePayloadMismatch:
		return fmt.Errorf("%w\n(history id %d is not a change on this review's table)", err, historyID)
	}
	return err
}

func printCellResult(cmd *cobra.Command, out io.Writer, v cellVerdict, reviewID uuid.UUID, sel cellSelector, result *data_reviews.ActionResult) {
	errOut := cmd.ErrOrStderr()

	cell := fmt.Sprintf("[h: %d]", sel.historyID)
	if sel.field != "" {
		cell = terminal.Sanitize(sel.field) + " " + cell
	}
	released := 0
	if result.ReleasedRowIds != nil {
		released = len(*result.ReleasedRowIds)
	}
	status, recomputed := ownStatus(result, reviewID)
	counts := fmt.Sprintf("affected: %d review(s) / %d row(s), released: %d row(s)",
		len(result.AffectedDataReviewIds), len(result.AffectedRowIds), released)
	if recomputed {
		counts = "status: " + terminal.Sanitize(string(status)) + ", " + counts
	}
	fmt.Fprintf(out, "data-review/%s cell %s %s (%s)\n", reviewID, cell, v.done, counts)

	if !recomputed {
		// The recompute skips closed reviews and any row not currently held in
		// quarantine: released, deleted, or a clean-table row that was
		// submitted from clean data. The ledger write happens regardless.
		note := "the verdict is still recorded on the change"
		if v.decision == api.RecordActionSchemaDecisionWithdraw {
			note = "your decision was still withdrawn from the change"
		}
		fmt.Fprintf(errOut, "note: data-review/%s was not recomputed: it is closed or its row is not held"+
			" in quarantine; %s\n", reviewID, note)
	}
	if others := otherReviews(result, reviewID); others > 0 {
		action := "verdict"
		if v.decision == api.RecordActionSchemaDecisionWithdraw {
			action = "withdrawal"
		}
		fmt.Fprintf(errOut, "this change is shared: the %s also applied to %d other review(s)\n", action, others)
	}
	fmt.Fprintf(out, "Next: qctl describe data-review %s\n", reviewID)
}

// ownStatus reads this review's entry out of affected_review_statuses, whose
// keys are UUID strings; a key that is not a UUID matches nothing.
func ownStatus(result *data_reviews.ActionResult, reviewID uuid.UUID) (api.ReviewRequestStatus, bool) {
	if result.AffectedReviewStatuses == nil {
		return "", false
	}
	for key, status := range *result.AffectedReviewStatuses {
		if id, err := uuid.Parse(key); err == nil && id == reviewID {
			return status, true
		}
	}
	return "", false
}

func otherReviews(result *data_reviews.ActionResult, reviewID uuid.UUID) int {
	others := 0
	for _, id := range result.AffectedDataReviewIds {
		if id != reviewID {
			others++
		}
	}
	return others
}
