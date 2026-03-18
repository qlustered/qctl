package tableui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// PrintFromCmd renders data using lipgloss for table format, or delegates to
// the existing output.Printer for json/yaml/name formats.
// defaultColumns is a comma-separated list of column names used when the user
// has not specified --columns.
func PrintFromCmd(cmd *cobra.Command, data interface{}, defaultColumns string) error {
	outputFormat, _ := cmd.Flags().GetString("output")

	// For non-table formats, delegate to the existing printer
	if outputFormat == "json" || outputFormat == "yaml" || outputFormat == "name" {
		setDefaultColumns(cmd, defaultColumns)
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return fmt.Errorf("failed to create output printer: %w", err)
		}
		return printer.Print(data)
	}

	// Table format: use lipgloss
	setDefaultColumns(cmd, defaultColumns)

	// Build a printer to leverage the existing reflection-based struct-to-row logic
	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	rows := printer.DataToRows(data)
	if len(rows) == 0 {
		return nil
	}

	// Determine columns
	columnsStr, _ := cmd.Flags().GetString("columns")
	var columns []string
	if columnsStr != "" {
		for _, col := range strings.Split(columnsStr, ",") {
			col = strings.TrimSpace(col)
			if col != "" {
				columns = append(columns, col)
			}
		}
	}
	if len(columns) == 0 {
		return nil
	}

	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	maxColWidth := 80
	if cmd.Flags().Lookup("max-column-width") != nil {
		maxColWidth, _ = cmd.Flags().GetInt("max-column-width")
	}

	w := cmd.OutOrStdout()
	return Render(w, columns, rows, noHeaders, maxColWidth)
}

// Render writes a lipgloss table to the given writer.
func Render(w io.Writer, columns []string, rows []map[string]string, noHeaders bool, maxColWidth int) error {
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
			if maxColWidth > 0 && len(val) > maxColWidth {
				if maxColWidth < 3 {
					val = val[:maxColWidth]
				} else {
					val = val[:maxColWidth-3] + "..."
				}
			}
			vals[j] = val
		}
		stringRows[i] = vals
	}

	// Create lipgloss table
	t := table.New().
		Border(lipgloss.HiddenBorder()).
		StyleFunc(func(row, col int) lipgloss.Style {
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
func setDefaultColumns(cmd *cobra.Command, defaultColumns string) {
	outputFormat, _ := cmd.Flags().GetString("output")
	if outputFormat == string(output.FormatTable) || outputFormat == "" {
		columnsFlag, _ := cmd.Flags().GetString("columns")
		if columnsFlag == "" {
			cmd.Flags().Set("columns", defaultColumns)
		}
	}
}
