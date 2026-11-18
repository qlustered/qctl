package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/submissions"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewSubmissionStatusesCommand creates the get submission-statuses command.
func NewSubmissionStatusesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submission-statuses",
		Short: "Show where each broker intake's submission for a period stands",
		Long: `Show the MGA's broker board: where each table-series cloud source (a broker
intake) stands for a reporting period.

  --series <name-or-id>   one row per active intake of the series expected
                          for --period or, without it, for the period the
                          server is collecting
  --cloud-source-id <id>  one row per period that series cloud source is
                          expected for, newest first
  neither                 one row per series: how many intakes are in each
                          status for the period the server is collecting

STATUS is the board status, in the board's order: late, on_hold,
received_not_loaded, received, expected, clean, nil, waived.

JSON and YAML print the API records as qctl's generated client decodes them:
the board object for --series, and a List envelope of rows or series
otherwise. -o name prints cloud-source/<id>
per --series row, table/<id> per --cloud-source-id period with a table, and
table-series/<uuid> per series.`,
		Annotations: catalog.For("submission-status"),
		Args:        cobra.NoArgs,
		RunE:        runSubmissionStatuses,
	}
	cmdutil.RefString(cmd.Flags(), "series", "table-series", "Show this series' board, by exact name or UUID")
	cmd.Flags().String("period", "", "With --series, the period key, e.g. 2026-09 or 2026-Q3 (default: the period being collected)")
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Show this series cloud source's board across its expected periods")
	cmd.Flags().String("columns", "", "Comma-separated table columns. Available: "+strings.Join(
		cmdutil.DeclareColumns(cmd, []submissions.RowDisplay(nil), []submissions.CountsDisplay(nil)), ", "))
	return cmd
}

// submissionScope is which board a get submission-statuses invocation reads.
type submissionScope struct {
	series        string
	period        string
	cloudSourceID int
}

func parseSubmissionScope(cmd *cobra.Command) (submissionScope, error) {
	var s submissionScope
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"series", "cloud-source-id"}); err != nil {
		return s, err
	}
	if cmd.Flags().Changed("period") && !cmd.Flags().Changed("series") {
		return s, qerrors.NewBadUsage("--period requires --series: --cloud-source-id lists every expected period, and the per-series counts are for the period being collected")
	}
	if cmd.Flags().Changed("series") {
		s.series, _ = cmd.Flags().GetString("series")
		if err := table_series.ValidateReference(s.series); err != nil {
			return s, err
		}
	}
	if cmd.Flags().Changed("period") {
		s.period, _ = cmd.Flags().GetString("period")
		if err := table_series.ValidatePeriodKey(s.period); err != nil {
			return s, err
		}
	}
	id, _, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return s, err
	}
	s.cloudSourceID = id
	var sample any = []submissions.RowDisplay(nil)
	if s.series == "" && s.cloudSourceID == 0 {
		sample = []submissions.CountsDisplay(nil)
	}
	return s, validateColumnsFlag(cmd, sample)
}

func runSubmissionStatuses(cmd *cobra.Command, _ []string) error {
	scope, err := parseSubmissionScope(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := submissions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	switch {
	case scope.series != "":
		return printSeriesBoard(cmd, ctx, c, scope)
	case scope.cloudSourceID != 0:
		return printIntakeBoard(cmd, ctx, c, scope.cloudSourceID)
	default:
		return printSeriesCounts(cmd, ctx, c)
	}
}

func printSeriesBoard(cmd *cobra.Command, ctx *cmdutil.CommandContext, c *submissions.Client, scope submissionScope) error {
	token := ctx.Credential.AccessToken
	id, err := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).ResolveID(token, scope.series)
	if err != nil {
		return fmt.Errorf("failed to resolve table series: %w", err)
	}
	board, err := c.SeriesBoard(token, id, scope.period)
	if err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	switch format {
	case string(output.FormatJSON), string(output.FormatYAML):
		return output.WriteRaw(cmd.OutOrStdout(), format, board)
	case string(output.FormatName):
		return printSubmissionNames(cmd, "cloud-source", "data_source_model_id", board.Rows)
	}
	if len(board.Rows) == 0 {
		submissions.RenderBoardHeader(cmd.ErrOrStderr(), board)
		fmt.Fprintln(cmd.ErrOrStderr())
		_, err := printEmptyResult(cmd, ctx, "submission statuses", []submissions.RowDisplay(nil), nil)
		return err
	}
	printContextBanner(cmd, ctx)
	submissions.RenderBoardHeader(cmd.ErrOrStderr(), board)
	fmt.Fprintln(cmd.ErrOrStderr())
	if err := tableui.PrintFromCmd(cmd, submissions.Rows(board.Rows), submissions.SeriesBoardColumns); err != nil {
		return err
	}
	fmt.Fprintln(cmd.ErrOrStderr(), submissions.CountsLine(board.Counts))
	printSubmissionAlertHint(cmd, board.Rows)
	return nil
}

func printIntakeBoard(cmd *cobra.Command, ctx *cmdutil.CommandContext, c *submissions.Client, cloudSourceID int) error {
	rows, err := c.IntakeBoard(ctx.Credential.AccessToken, cloudSourceID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "submission statuses", []submissions.RowDisplay(nil), nil)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, rows, listPage{noun: "submission statuses"}, func(data any) error {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case string(output.FormatJSON), string(output.FormatYAML):
			return output.WriteRaw(cmd.OutOrStdout(), format, data)
		case string(output.FormatName):
			return printSubmissionNames(cmd, "table", "dataset_id", rows)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Cloud source: %s (id: %d)\n\n", terminal.Sanitize(rows[0].BrokerName), cloudSourceID)
		if err := tableui.PrintFromCmd(cmd, submissions.Rows(rows), submissions.IntakeBoardColumns); err != nil {
			return err
		}
		printSubmissionAlertHint(cmd, rows)
		return nil
	})
}

func printSeriesCounts(cmd *cobra.Command, ctx *cmdutil.CommandContext, c *submissions.Client) error {
	items, err := c.SeriesCounts(ctx.Credential.AccessToken)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "table series", []submissions.CountsDisplay(nil), nil)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, items, listPage{noun: "table series"}, func(data any) error {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case string(output.FormatJSON), string(output.FormatYAML):
			return output.WriteRaw(cmd.OutOrStdout(), format, data)
		case string(output.FormatName):
			return printSubmissionNames(cmd, "table-series", "series_id", items)
		}
		if err := tableui.PrintFromCmd(cmd, submissions.CountsRows(items), submissions.CountsColumns); err != nil {
			return err
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Next: qctl get submission-statuses --series <name-or-id> [--period <key>]")
		return nil
	})
}

func printSubmissionNames(cmd *cobra.Command, kind, field string, items any) error {
	p, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return err
	}
	return p.NameAs(kind, field).Print(items)
}

// printSubmissionAlertHint names the command that explains a file that has
// not loaded, when a row carries the alert stopping one.
func printSubmissionAlertHint(cmd *cobra.Command, rows []submissions.Row) {
	if ids := submissions.AlertIDs(rows); len(ids) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "Next: qctl describe alert %d   # what stops a file that has not loaded (ALERT-ID)\n", ids[0])
	}
}
