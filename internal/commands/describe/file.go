package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewFileCommand creates the 'describe file' command
func NewFileCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file <id>",
		Short: "Show details of a specific file",
		Long: `Show detailed information about a specific file (stored item).

For YAML output (default), the file is converted into a declarative manifest that mirrors
the apply schema. JSON output also returns the manifest shape for machine parsing.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: File
  metadata:
    name: orders.csv
    labels:
      dataset_id: "11"
      data_source_model_id: "22"
  spec:
    dataset_id: 11
    dataset_name: orders
    cloud_source_id: 22
    cloud_source_name: s3-bucket
    file_name: orders.csv
    file_type: csv
    encoding: utf-8
    ignore_file: false
    backup_key: org/1/orders.csv
    csv_delimiter: ","
    csv_escapechar: "\\"
    csv_quotechar: "\""
    header_line_number_for_file: 1
    row_number_for_first_line_of_data: 2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse file ID from argument
			fileID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid file ID '%s': must be a number", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Get file
			result, err := client.GetStoredItem(ctx.Credential.AccessToken, fileID)
			if err != nil {
				return fmt.Errorf("failed to get file: %w", err)
			}

			manifest := stored_items.APIResponseToManifest(result)

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
