package get

import (
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

const (
	droppedLineColumns = "line,values,delete_id,deleted,deleted_by,reason,undone"

	// maxDroppedLinesLimit is the route's largest page.
	maxDroppedLinesLimit = 100
)

// NewDroppedLinesCommand creates the get dropped-lines command.
func NewDroppedLinesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dropped-lines <file-id>",
		Short: "List a file's lines that did not load because an earlier delete excluded them",
		Long: `List the lines of a file that did not load because a row with the same
identity was deleted earlier, in line order: each line's number in the file as
sent (the header counts), the deleted row's values, the delete that excluded
it, who ran that delete, when, and the reason given.

  qctl get dropped-lines 311
  qctl get dropped-lines file/311 --page 2 --limit 100
  qctl get dropped-lines 311 -o yaml

VALUES are the deleted row's key values, or its first three non-empty values
when the table has no key, as field=value; a PII column's value is masked. An
undone delete stays listed, with UNDONE true. A line whose delete record is
gone shows '-' for the delete.

describe file's status.deleted_lines_count can be larger than this list: a
delete made through the file also adds to it.

--page and --limit select one page (default: page 1, limit 50); --limit is at
most 100.`,
		Annotations: cmdutil.WithFormats(catalog.For("file"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDroppedLines(cmd, args[0])
		},
	}
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 50, "Lines per page (1-100)")
	cmdutil.DeclareColumns(cmd, []droppedLineRow(nil))
	return cmd
}

func runDroppedLines(cmd *cobra.Command, fileArg string) error {
	fileID, err := strconv.Atoi(fileArg)
	if err != nil || fileID <= 0 {
		return qerrors.NewBadUsagef("invalid file ID %q: file IDs are positive integers", fileArg)
	}
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return err
	}
	if limit > maxDroppedLinesLimit {
		return qerrors.NewBadUsagef("--limit must be at most %d, got %d", maxDroppedLinesLimit, limit)
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return err
	}
	list, err := client.ListDroppedLines(ctx.Credential.AccessToken, fileID, page, limit)
	if err != nil {
		return err
	}

	lp := listPage{noun: "dropped lines", page: &page, limit: &limit, total: list.TotalRows, more: list.Next != nil, noFilters: true}
	if len(list.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "dropped lines", []droppedLineRow{}, list.TotalRows)
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
		if lines, ok := data.([]stored_items.DroppedLine); ok {
			data = droppedLineRows(lines)
		}
		return tableui.PrintFromCmd(cmd, data, droppedLineColumns)
	})
}

// droppedLineRow is one dropped line as the table view shows it.
type droppedLineRow struct {
	Line      int    `json:"line"`
	Values    string `json:"values"`
	DeleteID  string `json:"delete_id"`
	Deleted   string `json:"deleted"`
	DeletedBy string `json:"deleted_by"`
	Reason    string `json:"reason"`
	Undone    bool   `json:"undone"`
}

func droppedLineRows(lines []stored_items.DroppedLine) []droppedLineRow {
	out := make([]droppedLineRow, len(lines))
	for i := range lines {
		l := &lines[i]
		row := droppedLineRow{
			Line:      l.RowNumber,
			Values:    droppedLineValues(l.Values),
			DeleteID:  "-",
			Deleted:   "-",
			DeletedBy: deleterLabel(l.DeletedBy),
			Reason:    "-",
			Undone:    l.Undone,
		}
		if l.RowDeleteID != nil {
			row.DeleteID = strconv.Itoa(*l.RowDeleteID)
		}
		if l.DeletedAt != nil {
			row.Deleted = timeutil.FormatRelative(*l.DeletedAt)
		}
		if l.Comment != nil {
			row.Reason = strings.Join(strings.Fields(*l.Comment), " ")
		}
		out[i] = row
	}
	return out
}

// droppedLineValues renders the values as field=value pairs, an empty value
// as field=, and "-" when there are none.
func droppedLineValues(values []stored_items.DroppedLineValue) string {
	if len(values) == 0 {
		return "-"
	}
	parts := make([]string, len(values))
	for i, v := range values {
		value := ""
		if v.Value != nil {
			value = strings.Join(strings.Fields(*v.Value), " ")
		}
		parts[i] = v.FieldName + "=" + value
	}
	return strings.Join(parts, ", ")
}
