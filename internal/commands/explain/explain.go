// Package explain provides the explain command for displaying schema documentation.
package explain

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/schema"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// resourceDocsPath maps CLI resource names to API docs anchor paths.
var resourceDocsPath = map[string]string{
	"table":          "/datasets",
	"dataset":        "/datasets",
	"cloud-source":   "/data-sources",
	"destination":    "/destinations",
	"alert":          "/alerts",
	"warning":        "/warnings",
	"file":           "/stored-items",
	"error-incident": "/errors",
}

// NewCommand creates the explain command
func NewCommand() *cobra.Command {
	var recursive bool

	cmd := &cobra.Command{
		Use:   "explain RESOURCE[.FIELD]",
		Short: "Show documentation for a resource or field",
		Long: `Display documentation for a resource type or specific field from the API schema.

This command retrieves schema information from the API server's OpenAPI specification,
providing accurate type information, descriptions, constraints, and allowed values.
The schema is cached locally and refreshed when the API version changes.

Examples:
  # Show all fields for the table resource
  qctl explain table

  # Show specific field information
  qctl explain table.migration_policy

  # Show all fields recursively (including nested objects)
  qctl explain table --recursive

  # Output schema as JSON
  qctl explain table -o json

  # Output schema as YAML
  qctl explain table -o yaml

Supported resources:
  table          - Dataset/table configuration
  cloud-source   - Cloud data source configuration
  destination    - Database destination configuration
  alert          - Alert details
  warning        - Warning details
  file           - Stored file/item details
  error-incident - Error incident details`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap to get server URL (no auth required for schema)
			ctx, err := cmdutil.BootstrapWithoutAuth(cmd)
			if err != nil {
				return fmt.Errorf("failed to bootstrap: %w", err)
			}

			// Configure schema package with API endpoint
			schema.SetAPIEndpoint(ctx.ServerURL)

			resourcePath := args[0]
			outputFormat, _ := cmd.Flags().GetString("output")

			return runExplain(cmd.OutOrStdout(), resourcePath, outputFormat, recursive, ctx.ServerURL)
		},
	}

	cmd.Flags().BoolVar(&recursive, "recursive", false, "Show deeply nested fields (beyond 1 level of nesting)")

	return cmd
}

func runExplain(w io.Writer, resourcePath, outputFormat string, recursive bool, serverURL string) error {
	parts := strings.SplitN(resourcePath, ".", 2)
	resource := parts[0]

	// Get the schema for the resource
	schemaInfo, err := schema.GetSchemaForResource(resource)
	if err != nil {
		return fmt.Errorf("failed to get schema for %q: %w", resource, err)
	}

	// If a field path is specified, get that specific field
	if len(parts) > 1 {
		fieldPath := parts[1]
		schemaName := schema.SchemaMapping[strings.ToLower(resource)]
		fieldInfo, err := schema.GetFieldProps(schemaName, fieldPath)
		if err != nil {
			return fmt.Errorf("failed to get field %q: %w", fieldPath, err)
		}
		return outputFieldInfo(w, fieldInfo, outputFormat)
	}

	// Build docs URL
	docsURL := ""
	if path, ok := resourceDocsPath[strings.ToLower(resource)]; ok {
		docsURL = serverURL + "/api/docs#" + path
	}

	// Output the full schema
	return outputSchemaInfo(w, schemaInfo, outputFormat, recursive, docsURL)
}

func outputSchemaInfo(w io.Writer, info *schema.SchemaInfo, format string, recursive bool, docsURL string) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(info)
	case "yaml":
		encoder := yaml.NewEncoder(w)
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(info)
	default:
		return printSchemaText(w, info, recursive, docsURL)
	}
}

func outputFieldInfo(w io.Writer, info *schema.FieldInfo, format string) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(info)
	case "yaml":
		encoder := yaml.NewEncoder(w)
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(info)
	default:
		return printFieldText(w, info, 0)
	}
}

func printSchemaText(w io.Writer, info *schema.SchemaInfo, recursive bool, docsURL string) error {
	fmt.Fprintf(w, "KIND:     %s\n", info.Name)
	fmt.Fprintf(w, "VERSION:  qluster.ai/v1\n")
	fmt.Fprintln(w)

	if info.Description != "" {
		fmt.Fprintf(w, "DESCRIPTION:\n")
		printWrapped(w, info.Description, "  ", 78)
		fmt.Fprintln(w)
	}

	if docsURL != "" {
		fmt.Fprintf(w, "DOCS:     %s\n", docsURL)
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "FIELDS:\n")
	for _, field := range info.Fields {
		printFieldSummary(w, &field, "  ", recursive, 0)
	}

	return nil
}

func printFieldText(w io.Writer, info *schema.FieldInfo, indent int) error {
	prefix := strings.Repeat("  ", indent)

	fmt.Fprintf(w, "%sFIELD:    %s\n", prefix, info.Name)

	typeStr := formatType(info)
	fmt.Fprintf(w, "%sTYPE:     %s\n", prefix, typeStr)

	if info.Required {
		fmt.Fprintf(w, "%sREQUIRED: true\n", prefix)
	}

	if info.ReadOnly {
		fmt.Fprintf(w, "%sREADONLY: true\n", prefix)
	}

	if info.Default != nil {
		fmt.Fprintf(w, "%sDEFAULT:  %v\n", prefix, info.Default)
	}

	fmt.Fprintln(w)

	if info.Description != "" {
		fmt.Fprintf(w, "%sDESCRIPTION:\n", prefix)
		printWrapped(w, info.Description, prefix+"  ", 78-len(prefix))
		fmt.Fprintln(w)
	}

	if len(info.Enum) > 0 {
		fmt.Fprintf(w, "%sALLOWED VALUES:\n", prefix)
		for _, v := range info.Enum {
			fmt.Fprintf(w, "%s  - %s\n", prefix, v)
		}
	}

	return nil
}

func printFieldSummary(w io.Writer, info *schema.FieldInfo, prefix string, recursive bool, depth int) {
	// Field name and type
	typeStr := formatType(info)

	// Build markers
	var markers []string
	if info.Required {
		markers = append(markers, "required")
	}
	if info.ReadOnly {
		markers = append(markers, "read-only")
	}

	markerStr := ""
	if len(markers) > 0 {
		markerStr = " <" + strings.Join(markers, ", ") + ">"
	}

	fmt.Fprintf(w, "%s%s <%s>%s\n", prefix, info.Name, typeStr, markerStr)

	// Description (truncated for summary)
	if info.Description != "" {
		desc := info.Description
		// Truncate long descriptions
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		fmt.Fprintf(w, "%s  %s\n", prefix, desc)
	}

	// Show enum values inline for short lists
	if len(info.Enum) > 0 && len(info.Enum) <= 5 {
		fmt.Fprintf(w, "%s  Allowed: %s\n", prefix, strings.Join(info.Enum, ", "))
	} else if len(info.Enum) > 5 {
		fmt.Fprintf(w, "%s  Allowed: %s, ... (%d total)\n", prefix, strings.Join(info.Enum[:3], ", "), len(info.Enum))
	}

	fmt.Fprintln(w)

	// Always show nested properties for objects (depth limit prevents infinite recursion)
	// The --recursive flag controls whether to go deeper than 1 level
	maxDepth := 1
	if recursive {
		maxDepth = 3
	}

	if depth < maxDepth {
		if len(info.Properties) > 0 {
			for _, prop := range info.Properties {
				printFieldSummary(w, &prop, prefix+"  ", recursive, depth+1)
			}
		}
		if info.Items != nil && len(info.Items.Properties) > 0 {
			fmt.Fprintf(w, "%s  [array item properties]\n", prefix)
			for _, prop := range info.Items.Properties {
				printFieldSummary(w, &prop, prefix+"    ", recursive, depth+1)
			}
		}
	}
}

func formatType(info *schema.FieldInfo) string {
	typeStr := info.Type

	// Prefer ref name over generic "object" type
	if info.Ref != "" {
		if info.Type == "object" || info.Type == "" {
			typeStr = info.Ref
		} else if info.Type != "array" {
			// For non-array types with ref, show the ref name
			typeStr = info.Ref
		}
	}

	if info.Format != "" && info.Ref == "" {
		typeStr = fmt.Sprintf("%s(%s)", info.Type, info.Format)
	}

	if info.Type == "array" && info.Items != nil {
		itemType := info.Items.Type
		if info.Items.Ref != "" {
			itemType = info.Items.Ref
		}
		if itemType == "" {
			itemType = "object"
		}
		typeStr = fmt.Sprintf("[]%s", itemType)
	}

	if info.Nullable {
		typeStr += ", nullable"
	}
	return typeStr
}

func printWrapped(w io.Writer, text, prefix string, width int) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return
	}

	line := prefix
	for _, word := range words {
		if len(line)+len(word)+1 > width && line != prefix {
			fmt.Fprintln(w, line)
			line = prefix + word
		} else {
			if line == prefix {
				line += word
			} else {
				line += " " + word
			}
		}
	}
	if line != prefix {
		fmt.Fprintln(w, line)
	}
}
