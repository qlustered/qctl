package describe

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewTableKindCommand creates the describe table-kind command
func NewTableKindCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-kind <slug-or-id>",
		Short: "Show a human-readable diagnostic view of a table kind",
		Long: `Show a human-readable diagnostic view of a table kind and its field kinds.

This output is for inspection only.
Use "qctl get table-kind <slug-or-id> -o yaml" for machine-readable output.

You can identify a table kind by slug, short ID, or full UUID.
Use -vv for a raw dump of the complete API response (useful for debugging).

Examples:
  qctl describe table-kind car-policy-bordereau
  qctl describe table-kind 550e8400
  qctl describe table-kind car-policy-bordereau -o json`,
		Args: cobra.ExactArgs(1),
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

			// Extract field kinds from the response (may be nil)
			var fieldKinds []dataset_kinds.DatasetFieldKindFull
			if kind.FieldKinds != nil {
				fieldKinds = *kind.FieldKinds
			}

			// For -vv (verbosity >= 2), output raw API response
			if ctx.Verbosity >= 2 {
				return writeTableKindRawDump(cmd, kind)
			}

			// Check if user explicitly requested a structured format
			outputFormat, _ := cmd.Flags().GetString("output")
			if cmd.Flags().Changed("output") {
				switch outputFormat {
				case "json":
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					return encoder.Encode(kind)
				case "yaml":
					encoder := yaml.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent(2)
					defer encoder.Close()
					return encoder.Encode(kind)
				}
			}

			// Default: human-readable plain text
			text := dataset_kinds.FormatDescribe(kind, fieldKinds)
			_, err = fmt.Fprint(cmd.OutOrStdout(), text)
			return err
		},
	}

	return cmd
}

// writeTableKindRawDump outputs the full API response as JSON or YAML for debugging.
func writeTableKindRawDump(cmd *cobra.Command, kind *dataset_kinds.DatasetKindWithFieldKinds) error {
	outputFormat, _ := cmd.Flags().GetString("output")
	if cmd.Flags().Changed("output") && outputFormat == "json" {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(kind)
	}

	encoder := yaml.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(kind)
}
