package printer

import (
	"slices"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// formatFromCmd reads and validates the --output flag.
//
// The root command validates --output before any RunE runs
// (internal/cmdutil/output_format.go). Reaching this with an unknown value
// means the format bypassed that hook -- a synthetic command tree in a
// test, say. Fail instead of coercing: `format = FormatTable` under a
// comment that said "Validate format" is exactly how `-o jsonn` used to
// print a grid at exit 0.
//
// Validated against output.FormatNames() rather than a copy of this
// package's own four Format constants (FormatTable..FormatName in
// printer.go), so the two lists cannot drift apart.
func formatFromCmd(cmd *cobra.Command) (Format, error) {
	formatStr, _ := cmd.Flags().GetString("output")
	if !slices.Contains(output.FormatNames(), formatStr) {
		return "", qerrors.NewBadUsagef("unknown output format %q\n  valid: %s",
			formatStr, strings.Join(output.FormatNames(), ", "))
	}
	return Format(formatStr), nil
}

// NewPrinterFromCmd creates a Printer from cobra command flags
func NewPrinterFromCmd(cmd *cobra.Command) (*Printer, error) {
	format, err := formatFromCmd(cmd)
	if err != nil {
		return nil, err
	}

	// Get other flags
	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	columnsStr, _ := cmd.Flags().GetString("columns")
	allowPlaintextSecrets, _ := cmd.Flags().GetBool("allow-plaintext-secrets")

	columns := output.ParseColumns(columnsStr)

	opts := Options{
		Format:                format,
		NoHeaders:             noHeaders,
		Columns:               columns,
		AllowPlaintextSecrets: allowPlaintextSecrets,
		MaxColumnWidth:        output.MaxColumnWidth(cmd),
		Writer:                cmd.OutOrStdout(), // Use command's output writer
		NameKind:              output.NameKind(cmd),
	}

	return NewPrinter(opts), nil
}

// NewPrinterFromCmdWithMarkdown creates a Printer from cobra command flags with markdown rendering
// for specified fields. The markdown rendering only applies to table format output.
func NewPrinterFromCmdWithMarkdown(cmd *cobra.Command, markdownFields []string) (*Printer, error) {
	format, err := formatFromCmd(cmd)
	if err != nil {
		return nil, err
	}

	// Get other flags
	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	columnsStr, _ := cmd.Flags().GetString("columns")
	allowPlaintextSecrets, _ := cmd.Flags().GetBool("allow-plaintext-secrets")

	columns := output.ParseColumns(columnsStr)

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
		MaxColumnWidth:        output.MaxColumnWidth(cmd),
		Writer:                cmd.OutOrStdout(),
		NameKind:              output.NameKind(cmd),
		MarkdownFields:        markdownFields,
		MarkdownRenderer:      mdRenderer,
	}

	return NewPrinter(opts), nil
}
