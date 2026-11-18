package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewTableKindCommand creates the describe table-kind command
func NewTableKindCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-kind <slug-or-id>",
		Short: "Show a human-readable diagnostic view of a table kind",
		Long: `Show a human-readable diagnostic view of a table kind and its linked standard fields.

The standard fields section shows each standard field's slug, name, expected field type, and aliases.
Use "qctl get table-kind <slug-or-id> -o yaml" for machine-readable output.

You can identify a table kind by slug, short ID, or full UUID.
Use -vv for a raw dump of the complete API response (useful for debugging).

Examples:
  qctl describe table-kind car-policy-bordereau
  qctl describe table-kind 550e8400
  qctl describe table-kind car-policy-bordereau -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("table-kind"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			kindID, err := client.ResolveDatasetKindID(ctx.Credential.AccessToken, input)
			if err != nil {
				return err
			}

			kind, err := client.GetDatasetKind(ctx.Credential.AccessToken, kindID)
			if err != nil {
				return fmt.Errorf("failed to get table kind: %w", err)
			}

			// Extract concepts from the response (may be nil)
			var concepts []dataset_kinds.DatasetConceptFull
			if kind.Concepts != nil {
				concepts = *kind.Concepts
			}

			// For -vv (verbosity >= 2), output raw API response
			if ctx.Verbosity >= 2 {
				return writeTableKindRawDump(cmd, kind)
			}

			// Check if user explicitly requested a structured format
			outputFormat, _ := cmd.Flags().GetString("output")
			if cmd.Flags().Changed("output") {
				switch outputFormat {
				case outputFormatJSON, outputFormatYAML:
					return output.WriteRaw(cmd.OutOrStdout(), outputFormat, kind)
				}
			}

			// Default: human-readable plain text
			text := dataset_kinds.FormatDescribe(kind, concepts)
			_, err = fmt.Fprint(cmd.OutOrStdout(), text)
			return err
		},
	}

	return cmd
}

// writeTableKindRawDump outputs the full API response as JSON or YAML for debugging.
func writeTableKindRawDump(cmd *cobra.Command, kind *dataset_kinds.DatasetKindWithConcepts) error {
	outputFormat, _ := cmd.Flags().GetString("output")
	if !cmd.Flags().Changed("output") || outputFormat != outputFormatJSON {
		outputFormat = outputFormatYAML
	}
	return output.WriteRaw(cmd.OutOrStdout(), outputFormat, kind)
}
