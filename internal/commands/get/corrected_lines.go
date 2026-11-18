package get

import (
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

const correctedLineColumns = "line,field,value_sent,value_used"

// NewCorrectedLinesCommand creates the get corrected-lines command.
func NewCorrectedLinesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "corrected-lines [file-id]",
		Short: "List a file's cells corrected by an earlier steward fix on load",
		Long: `List each corrected cell's line number, column, value sent and value used.
Line numbers count the header row. Values in PII columns are masked by the
server. Dropped lines are excluded. A line with several corrected cells
appears once per cell, in line and column order.

  qctl get corrected-lines --file 311
  qctl get corrected-lines file/311 --page 2 --limit 100
  qctl get corrected-lines --file 311 -o yaml

Supply the file ID either positionally or with --file, but not both.
--page and --limit select one page of cells (default: page 1, limit 50);
--limit is at most 100. JSON/YAML retain the server's cell records, with
null values preserved.`,
		Annotations: cmdutil.WithFormats(catalog.For("file"), "table", "json", "yaml"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runCorrectedLines,
	}
	cmdutil.RefInt(cmd.Flags(), "file", "file", "File ID whose corrected cells to list")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 50, "Cells per page (1-100)")
	cmdutil.DeclareColumns(cmd, []correctedLineRow(nil))
	return cmd
}

func correctedLinesFileID(cmd *cobra.Command, args []string) (int, error) {
	if len(args) > 0 && cmd.Flags().Changed("file") {
		return 0, qerrors.NewBadUsage("supply a file ID either positionally or with --file, not both")
	}
	if len(args) > 0 {
		id, err := strconv.Atoi(args[0])
		if err != nil || id <= 0 {
			return 0, qerrors.NewBadUsagef("invalid file ID %q: file IDs are positive integers", args[0])
		}
		return id, nil
	}
	id, set, err := cmdutil.PositiveIDFlag(cmd, "file")
	if err != nil {
		return 0, err
	}
	if !set {
		return 0, qerrors.NewBadUsage("a file ID is required: use --file <id> or a positional file ID")
	}
	return id, nil
}

func runCorrectedLines(cmd *cobra.Command, args []string) error {
	fileID, err := correctedLinesFileID(cmd, args)
	if err != nil {
		return err
	}
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return err
	}
	if limit > 100 {
		return qerrors.NewBadUsagef("--limit must be at most 100, got %d", limit)
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return err
	}
	list, err := client.ListCorrectedLines(ctx.Credential.AccessToken, fileID, page, limit)
	if err != nil {
		return err
	}

	lp := listPage{noun: "corrected cells", page: &page, limit: &limit, total: list.TotalRows, more: list.Next != nil, noFilters: true}
	if len(list.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "corrected cells", []correctedLineRow{}, list.TotalRows)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, list.Results, lp, func(data any) error {
		if l, ok := data.(output.List); ok {
			format, _ := cmd.Flags().GetString("output")
			return output.WriteRaw(cmd.OutOrStdout(), format, l)
		}
		if lines, ok := data.([]stored_items.CorrectedLine); ok {
			data = correctedLineRows(lines)
		}
		return tableui.PrintFromCmd(cmd, data, correctedLineColumns)
	})
}

type correctedLineRow struct {
	Line      int    `json:"line"`
	Field     string `json:"field"`
	ValueSent string `json:"value_sent"`
	ValueUsed string `json:"value_used"`
}

func correctedLineRows(cells []stored_items.CorrectedLine) []correctedLineRow {
	value := func(v *string) string {
		if v == nil {
			return ""
		}
		return strings.Join(strings.Fields(*v), " ")
	}
	rows := make([]correctedLineRow, len(cells))
	for i, cell := range cells {
		rows[i] = correctedLineRow{
			Line: cell.RowNumber, Field: cell.FieldName,
			ValueSent: value(cell.ValueSent), ValueUsed: value(cell.ValueUsed),
		}
	}
	return rows
}
