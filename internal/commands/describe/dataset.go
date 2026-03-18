package describe

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewDatasetCommand creates the describe table command
func NewDatasetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table <id-or-name>",
		Short: "Show details of a specific table",
		Long: `Show details of a specific table including its configuration and settings.

The argument can be an integer ID or the table name. If the argument is a number,
it is treated as an ID. Otherwise, it is looked up by exact name match.

For YAML output (default), the table is converted into a declarative manifest that can be used
with apply workflows. JSON output also returns the manifest shape for machine parsing.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: Table
  metadata:
    name: orders
  spec:
    destination_id: 3
    database_name: analytics
    schema_name: public
    table_name: orders
    migration_policy: apply_asap
    data_loading_process: snapshot
    backup_settings_id: 9
    anomaly_threshold: 5
    max_retry_count: 3
    max_tries_to_fix_json: 2
    should_reprocess: true
    detect_anomalies: true`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create datasets client
			client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			// Resolve table ID from name or numeric ID
			datasetID, err := client.ResolveID(ctx.Credential.AccessToken, args[0])
			if err != nil {
				return err
			}

			// Get table
			result, err := client.GetDataset(ctx.Credential.AccessToken, datasetID)
			if err != nil {
				return fmt.Errorf("failed to get table: %w", err)
			}

			manifest := datasets.APIResponseToManifest(result)

			// Determine output format (default to YAML manifest for describe)
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = "yaml"
			}

			switch outputFormat {
			case "json":
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(manifest)
			case "table":
				p, err := printer.NewPrinterFromCmd(cmd)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				return p.Print(manifest)
			default:
				encoder := yaml.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent(2)
				defer encoder.Close()
				return encoder.Encode(manifest)
			}
		},
	}

	return cmd
}
