package get

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

const dataReviewChangeColumns = "history_id,field,before,after,rows,flagged,decision,decided_by"

// dataReviewChangeFlagged is the flagged cell of a change with an open flag.
const dataReviewChangeFlagged = "yes"

var dataReviewChangeStates = map[string]api.DecisionState{
	"unresolved":        api.DecisionStateUnresolved,
	"decided":           api.DecisionStateDecided,
	"all":               api.DecisionStateAll,
	"undecided":         api.DecisionStateUndecided,
	"changes_requested": api.DecisionStateChangesRequested,
	"accepted":          api.DecisionStateAccepted,
	"rejected":          api.DecisionStateRejected,
}

// dataReviewChangeRow is the table projection of one ranked change. before_type
// and after_type are the wire's old_value_type/value_type under this row's own
// before/after vocabulary; both are off by default and reachable with
// --columns. Flagged is "yes" when the wire's has_open_flag is true and blank
// otherwise.
type dataReviewChangeRow struct {
	HistoryID  int    `json:"history_id"`
	Field      string `json:"field"`
	Before     string `json:"before"`
	BeforeType string `json:"before_type"`
	After      string `json:"after"`
	AfterType  string `json:"after_type"`
	Rows       int    `json:"rows"`
	Flagged    string `json:"flagged"`
	Decision   string `json:"decision"`
	DecidedBy  string `json:"decided_by"`
}

// NewDataReviewChangesCommand creates the get data-review-changes command.
func NewDataReviewChangesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data-review-changes",
		Short: "List a table's highest-leverage data-review changes",
		Long: `List a table's submitted cell changes ranked by affected rows.

The default state is unresolved, matching the web review inbox: a change stays
listed while it carries no decision or only a comment. A stored request_changes
is a blocking verdict, so it sits in --state decided.

--group scopes changes and affected-row counts to a submission's snapshot.
--offset skips ranked live changes; use --offset 20 for the second 20-item page.

FLAGGED reads "yes" when a live row the change affects carries an active
user-submitted alert or warning, on any cell; with --group, only that
submission's rows count.

-o json and -o yaml emit a List of the ranked changes with page, total and
next_page null; the table footer's "up to" count is not fetched for them.`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runDataReviewChanges,
	}
	cmdutil.AddTableFlags(cmd, "Table whose highest-leverage review changes to list")
	cmd.Flags().String("state", "unresolved", "Decision state (unresolved, decided, all, undecided, changes_requested, accepted, rejected)")
	cmd.Flags().Int("limit", 20, "Maximum changes to return (1-20)")
	cmdutil.RefString(cmd.Flags(), "group", "data-review-group", "Only changes in this submission group (full UUID)")
	cmd.Flags().Int("offset", 0, "Number of ranked changes to skip (0 or greater)")
	cmdutil.DeclareColumns(cmd, []dataReviewChangeRow(nil))
	return cmd
}

func runDataReviewChanges(cmd *cobra.Command, _ []string) error {
	opts, stateLabel, err := dataReviewChangeFlags(cmd)
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
	opts.DatasetID, err = cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}

	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	changes, err := client.TopChanges(ctx.Credential.AccessToken, opts)
	if err != nil {
		return fmt.Errorf("failed to list data-review changes: %w", err)
	}
	if cmd.Flags().Changed("output") {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			limit := opts.Limit
			return output.WriteRaw(cmd.OutOrStdout(), format, output.NewList(changes, nil, &limit, nil, nil))
		}
	}
	// The count feeds the stderr footer and nothing else, so it is fetched
	// only on the path that prints one.
	total, err := client.TopChangesCount(ctx.Credential.AccessToken, opts)
	if err != nil {
		return fmt.Errorf("failed to count data-review changes: %w", err)
	}
	if len(changes) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "data-review changes", []dataReviewChangeRow{}, &total)
		if err != nil {
			return err
		}
		if skip {
			fmt.Fprintf(cmd.ErrOrStderr(), "Showing 0 of up to %d %s change(s).\n", total, stateLabel)
			return nil
		}
	}

	directory := cmdutil.FetchDisplayDirectory(cmd, client.FetchUsers, ctx.Credential.AccessToken)
	printContextBanner(cmd, ctx)
	if err := tableui.PrintFromCmd(cmd, dataReviewChangeRows(changes, directory), dataReviewChangeColumns); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d of up to %d %s change(s).\n", len(changes), total, stateLabel)
	if len(changes) == opts.Limit && opts.Offset < total-len(changes) {
		fmt.Fprintf(cmd.ErrOrStderr(), "More changes may be available — continue with --offset %d.\n",
			opts.Offset+len(changes))
	}
	return nil
}

func dataReviewChangeFlags(cmd *cobra.Command) (data_reviews.TopChangesOptions, string, error) {
	stateLabel, _ := cmd.Flags().GetString("state")
	state, ok := dataReviewChangeStates[stateLabel]
	if !ok {
		return data_reviews.TopChangesOptions{}, "", qerrors.NewBadUsagef(
			"invalid --state %q: expected unresolved, decided, all, undecided, changes_requested, accepted, or rejected",
			stateLabel)
	}
	limit, err := cmdutil.PositiveCountFlag(cmd, "limit")
	if err != nil {
		return data_reviews.TopChangesOptions{}, "", err
	}
	if limit > 20 {
		return data_reviews.TopChangesOptions{}, "", qerrors.NewBadUsagef("--limit must be between 1 and 20 (got %d)", limit)
	}
	offset, err := cmdutil.NonNegativeCountFlag(cmd, "offset")
	if err != nil {
		return data_reviews.TopChangesOptions{}, "", err
	}
	opts := data_reviews.TopChangesOptions{State: state, Limit: limit, Offset: offset}
	if cmd.Flags().Changed("group") {
		value, _ := cmd.Flags().GetString("group")
		groupID, err := uuid.Parse(value)
		if err != nil {
			return data_reviews.TopChangesOptions{}, "", qerrors.NewBadUsagef(
				"invalid --group %q: expected a full submission-group UUID", value,
			)
		}
		opts.ReviewGroupID = &groupID
	}
	return opts, stateLabel, nil
}

func dataReviewChangeRows(changes []data_reviews.TopChange, directory *data_reviews.Directory) []dataReviewChangeRow {
	rows := make([]dataReviewChangeRow, 0, len(changes))
	for _, change := range changes {
		row := dataReviewChangeRow{
			HistoryID:  change.HistoryID,
			Field:      change.FieldName,
			Before:     nullableReviewValue(change.OriginalValue),
			BeforeType: data_reviews.UnitTag(change.OldValueType),
			After:      nullableReviewValue(change.EditedValue),
			AfterType:  data_reviews.UnitTag(change.ValueType),
			Rows:       change.AffectedRowCount,
		}
		if change.HasOpenFlag {
			row.Flagged = dataReviewChangeFlagged
		}
		if change.Decision != nil {
			row.Decision = string(*change.Decision)
		}
		if change.DecidedByID != nil {
			row.DecidedBy = directory.DisplayName(*change.DecidedByID)
		}
		rows = append(rows, row)
	}
	return rows
}

func nullableReviewValue(value *string) string {
	if value == nil {
		return "∅"
	}
	return *value
}
