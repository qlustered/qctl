package output

import (
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
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
func formatFromCmd(cmd *cobra.Command) (Format, error) {
	formatStr, _ := cmd.Flags().GetString("output")
	if !slices.Contains(FormatNames(), formatStr) {
		return "", qerrors.NewBadUsagef("unknown output format %q\n  valid: %s",
			formatStr, strings.Join(FormatNames(), ", "))
	}
	return Format(formatStr), nil
}

// AnnotationNameKind is the cobra annotation key naming the kind -o name
// prints for a command whose objects are not its catalog resource.
const AnnotationNameKind = "name_kind"

// WithNameKind declares the kind -o name prints, merged into the annotation
// map catalog.For built.
func WithNameKind(base map[string]string, kind string) map[string]string {
	merged := make(map[string]string, len(base)+1)
	for k, v := range base {
		merged[k] = v
	}
	merged[AnnotationNameKind] = kind
	return merged
}

// NameKind is the kind -o name prints for cmd's objects unless its printer
// says otherwise: its AnnotationNameKind, else its catalog resource.
func NameKind(cmd *cobra.Command) string {
	if kind := cmd.Annotations[AnnotationNameKind]; kind != "" {
		return kind
	}
	return cmd.Annotations[catalog.AnnotationResource]
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

	columns := ParseColumns(columnsStr)

	opts := Options{
		Format:                format,
		NoHeaders:             noHeaders,
		Columns:               columns,
		AllowPlaintextSecrets: allowPlaintextSecrets,
		MaxColumnWidth:        MaxColumnWidth(cmd),
		Writer:                cmd.OutOrStdout(), // Use command's output writer
		NameKind:              NameKind(cmd),
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

	columns := ParseColumns(columnsStr)

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
		MaxColumnWidth:        MaxColumnWidth(cmd),
		Writer:                cmd.OutOrStdout(),
		NameKind:              NameKind(cmd),
		MarkdownFields:        markdownFields,
		MarkdownRenderer:      mdRenderer,
	}

	return NewPrinter(opts), nil
}
