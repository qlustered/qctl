package describe

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	defaultTableRuleOutputFormat = "yaml"
	jsonTableRuleOutputFormat    = "json"
)

// NewTableRuleCommand creates the describe table-rule command
func NewTableRuleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-rule <name-or-id>",
		Short: "Show details of a specific table rule",
		Long: `Show details of a specific table rule (instantiated rule on a table).

You can identify a table rule by instance name, short ID, or full UUID.

By default, essential fields including params and column_mapping are shown
(enabling round-trip get → edit → apply workflows). Use -v to also include
dataset_field_names, or -vv for a raw dump of the complete API response.

Verbosity levels:
  (default)  Essential fields including params and column_mapping
  -v         Adds dataset_field_names
  -vv        Raw API response dump (all fields, useful for debugging)

Examples:
  qctl describe table-rule my_email_check --table 1
  qctl describe table-rule 550e8400 --table 1
  qctl describe table-rule 550e8400-e29b-41d4-a716-446655440000 --table 1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

			tableID, _ := cmd.Flags().GetInt("table")
			if tableID == 0 {
				return fmt.Errorf("--table flag is required")
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create client
			client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Resolve input to a dataset rule ID
			datasetRuleID, err := client.ResolveDatasetRuleID(ctx.Credential.AccessToken, tableID, input)
			if err != nil {
				return err
			}

			// Get detail
			detail, err := client.GetDatasetRuleDetail(ctx.Credential.AccessToken, datasetRuleID)
			if err != nil {
				return fmt.Errorf("failed to get table rule: %w", err)
			}

			// Get output format
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = defaultTableRuleOutputFormat
			}

			// For -vv (verbosity >= 2), output raw API response
			if ctx.Verbosity >= 2 {
				rawManifest, err := dataset_rules.APIResponseToRawManifest(detail)
				if err != nil {
					return fmt.Errorf("failed to build raw manifest: %w", err)
				}

				if outputFormat == jsonTableRuleOutputFormat {
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					return encoder.Encode(rawManifest)
				}

				encoder := yaml.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent(2)
				defer encoder.Close()
				return encoder.Encode(rawManifest)
			}

			// Convert to manifest format (kind: TableRule)
			manifest := dataset_rules.APIResponseToManifest(detail, ctx.Verbosity)

			if outputFormat == jsonTableRuleOutputFormat {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(manifest)
			}

			// YAML output (default)
			encoder := yaml.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent(2)
			defer encoder.Close()
			return encoder.Encode(manifest)
		},
	}

	cmd.Flags().Int("table", 0, "Table (dataset) ID (required)")
	return cmd
}

