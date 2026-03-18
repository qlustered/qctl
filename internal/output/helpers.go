package output

import (
	"strings"

	"github.com/qlustered/qctl/internal/markdown"
	"github.com/spf13/cobra"
)

// NewPrinterFromCmd creates a Printer from cobra command flags
func NewPrinterFromCmd(cmd *cobra.Command) (*Printer, error) {
	// Get output format
	formatStr, _ := cmd.Flags().GetString("output")
	format := Format(formatStr)

	// Validate format
	switch format {
	case FormatTable, FormatJSON, FormatYAML, FormatName:
		// valid
	default:
		format = FormatTable
	}

	// Get other flags
	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	columnsStr, _ := cmd.Flags().GetString("columns")
	allowPlaintextSecrets, _ := cmd.Flags().GetBool("allow-plaintext-secrets")

	// Parse columns
	var columns []string
	if columnsStr != "" {
		for _, col := range strings.Split(columnsStr, ",") {
			col = strings.TrimSpace(col)
			if col != "" {
				columns = append(columns, col)
			}
		}
	}

	// Get max column width (if flag exists)
	// Default to 80 if not explicitly set, or use the flag value
	maxColWidth := 80 // default
	if cmd.Flags().Lookup("max-column-width") != nil {
		maxColWidth, _ = cmd.Flags().GetInt("max-column-width")
	}

	opts := Options{
		Format:                format,
		NoHeaders:             noHeaders,
		Columns:               columns,
		AllowPlaintextSecrets: allowPlaintextSecrets,
		MaxColumnWidth:        maxColWidth,
		Writer:                cmd.OutOrStdout(), // Use command's output writer
	}

	return NewPrinter(opts), nil
}

// NewPrinterFromCmdWithMarkdown creates a Printer from cobra command flags with markdown rendering
// for specified fields. The markdown rendering only applies to table format output.
func NewPrinterFromCmdWithMarkdown(cmd *cobra.Command, markdownFields []string) (*Printer, error) {
	// Get output format
	formatStr, _ := cmd.Flags().GetString("output")
	format := Format(formatStr)

	// Validate format
	switch format {
	case FormatTable, FormatJSON, FormatYAML, FormatName:
		// valid
	default:
		format = FormatTable
	}

	// Get other flags
	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	columnsStr, _ := cmd.Flags().GetString("columns")
	allowPlaintextSecrets, _ := cmd.Flags().GetBool("allow-plaintext-secrets")

	// Parse columns
	var columns []string
	if columnsStr != "" {
		for _, col := range strings.Split(columnsStr, ",") {
			col = strings.TrimSpace(col)
			if col != "" {
				columns = append(columns, col)
			}
		}
	}

	// Get max column width (if flag exists)
	// Default to 80 if not explicitly set, or use the flag value
	maxColWidth := 80 // default
	if cmd.Flags().Lookup("max-column-width") != nil {
		maxColWidth, _ = cmd.Flags().GetInt("max-column-width")
	}

	// Create markdown renderer only for table format (ANSI colors in table, plain text for JSON/YAML)
	var mdRenderer *markdown.Renderer
	if format == FormatTable && len(markdownFields) > 0 {
		var err error
		mdRenderer, err = markdown.New(markdown.Options{Writer: cmd.OutOrStdout()})
		if err != nil {
			// Non-fatal - fall back to no markdown rendering
			mdRenderer = nil
		}
	}

	opts := Options{
		Format:                format,
		NoHeaders:             noHeaders,
		Columns:               columns,
		AllowPlaintextSecrets: allowPlaintextSecrets,
		MaxColumnWidth:        maxColWidth,
		Writer:                cmd.OutOrStdout(),
		MarkdownFields:        markdownFields,
		MarkdownRenderer:      mdRenderer,
	}

	return NewPrinter(opts), nil
}
