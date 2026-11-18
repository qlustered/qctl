package tableui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// PrintFromCmd renders data using lipgloss for table format, or delegates to
// the existing output.Printer for json/yaml/name formats.
// defaultColumns is a comma-separated list of column names used when the user
// has not specified --columns.
func PrintFromCmd(cmd *cobra.Command, data interface{}, defaultColumns string) error {
	return PrintMessagesFromCmd(cmd, data, defaultColumns, nil)
}

// PrintMessagesFromCmd is PrintFromCmd for data with backend message columns.
// In a table, each column named in messageColumns is rendered as a message by
// markdown.Renderer.RenderCell -- one line, styled on a terminal, fitted to
// --max-column-width by shortening its marked values before clipping the rest
// -- instead of using the ordinary cell truncation. json and yaml output are
// unchanged: they print the message as the server sent it.
func PrintMessagesFromCmd(cmd *cobra.Command, data interface{}, defaultColumns string, messageColumns []string) error {
	outputFormat, _ := cmd.Flags().GetString("output")

	// For non-table formats, delegate to the existing printer
	if outputFormat == "json" || outputFormat == "yaml" || outputFormat == "name" {
		if err := setDefaultColumns(cmd, defaultColumns); err != nil {
			return fmt.Errorf("failed to set default columns: %w", err)
		}
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return fmt.Errorf("failed to create output printer: %w", err)
		}
		return printer.Print(data)
	}

	// Table format: use lipgloss
	if err := setDefaultColumns(cmd, defaultColumns); err != nil {
		return fmt.Errorf("failed to set default columns: %w", err)
	}

	// Build a printer to leverage the existing reflection-based struct-to-row logic
	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	rows := printer.DataToRows(data)

	columnsStr, _ := cmd.Flags().GetString("columns")
	columns := output.ParseColumns(columnsStr)

	// Before the empty short-circuit: see output.AvailableColumns.
	if err := output.ValidateColumns(columns, output.AvailableColumns(data, rows)); err != nil {
		return err
	}

	if len(rows) == 0 || len(columns) == 0 {
		return nil
	}

	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	w := cmd.OutOrStdout()
	return render(w, columns, rows, noHeaders, output.MaxColumnWidth(cmd), newCellRenderer(w, messageColumns))
}

// cellRenderer renders the message columns of a table; the zero value renders
// none.
type cellRenderer struct {
	renderer *markdown.Renderer
	columns  map[string]bool
}

// newCellRenderer renders messageColumns for a table written to w. With no
// message columns, or no renderer, it is the zero value.
func newCellRenderer(w io.Writer, messageColumns []string) cellRenderer {
	if len(messageColumns) == 0 {
		return cellRenderer{}
	}
	r, err := markdown.New(markdown.Options{Writer: w})
	if err != nil {
		return cellRenderer{}
	}
	cells := cellRenderer{renderer: r, columns: map[string]bool{}}
	for _, c := range messageColumns {
		cells.columns[c] = true
	}
	return cells
}

// Render writes a lipgloss table to the given writer.
func Render(w io.Writer, columns []string, rows []map[string]string, noHeaders bool, maxColWidth int) error {
	return render(w, columns, rows, noHeaders, maxColWidth, cellRenderer{})
}

func render(w io.Writer, columns []string, rows []map[string]string, noHeaders bool, maxColWidth int, cells cellRenderer) error {
	// Build headers
	headers := make([]string, len(columns))
	for i, col := range columns {
		if col == "data_source_type" {
			headers[i] = "TYPE"
		} else {
			headers[i] = strings.ToUpper(strings.ReplaceAll(col, "_", "-"))
		}
	}

	// Build string rows
	stringRows := make([][]string, len(rows))
	for i, row := range rows {
		vals := make([]string, len(columns))
		for j, col := range columns {
			val := row[col]
			if cells.columns[col] {
				vals[j] = cells.renderer.RenderCell(val, maxColWidth)
				continue
			}
			val = terminal.Sanitize(val)
			vals[j] = terminal.TruncateCell(val, maxColWidth)
		}
		stringRows[i] = vals
	}

	// Create lipgloss table
	t := table.New().
		Border(lipgloss.HiddenBorder()).
		StyleFunc(func(row, _ int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingRight(2)
			if row == table.HeaderRow {
				s = s.Bold(true)
			}
			return s
		}).
		Rows(stringRows...)

	if !noHeaders {
		t = t.Headers(headers...)
	}

	fmt.Fprintln(w, t.Render())
	return nil
}

// setDefaultColumns sets the --columns flag to defaultColumns if not already set
// and the output format is table (or default).
func setDefaultColumns(cmd *cobra.Command, defaultColumns string) error {
	outputFormat, _ := cmd.Flags().GetString("output")
	if outputFormat == string(output.FormatTable) || outputFormat == "" {
		columnsFlag, _ := cmd.Flags().GetString("columns")
		if columnsFlag == "" {
			return cmd.Flags().Set("columns", defaultColumns)
		}
	}
	return nil
}
