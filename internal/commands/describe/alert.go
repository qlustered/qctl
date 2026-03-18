package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	defaultAlertOutputFormat = "yaml"
	jsonAlertOutputFormat    = "json"
	tableAlertOutputFormat   = "table"
)

// NewAlertCommand creates the describe alert command
func NewAlertCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alert <id>",
		Short: "Show details of a specific alert",
		Long: `Show details of a specific alert including its configuration and affected items.

By default, only essential fields are shown. Use -v for full details including
body highlights (suggested values, anomaly scores, etc.), or -vv for a raw dump
of the complete API response.

Verbosity levels:
  (default)  Essential fields only — omits debugging/niche fields
  -v         Full alert details + body highlights (suggested values, anomaly scores, etc.)
  -vv        Raw API response dump (all fields, useful for debugging)

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: Alert
  metadata:
    id: 1
  spec:
    issue_type: MISSING_COLUMN
    message: "Column 'email' is missing"
    dataset_id: 10
    dataset_name: user_data
    field_name: email
    blocks_ingestion_for_dataset: true
    actions:
      - ignore_issue_for_value
  status:
    resolved: false
    count: 3
    created_at: 3 weeks ago`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse alert ID from arg
			alertID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid alert ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create alerts client
			client := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			// Get alert
			result, err := client.GetAlert(ctx.Credential.AccessToken, alertID)
			if err != nil {
				return fmt.Errorf("failed to get alert: %w", err)
			}

			// Get output format
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = defaultAlertOutputFormat
			}

			// For -vv (verbosity >= 2), output raw API response
			if ctx.Verbosity >= 2 {
				rawManifest, err := alerts.APIResponseToRawManifest(result)
				if err != nil {
					return fmt.Errorf("failed to build raw manifest: %w", err)
				}

				if outputFormat == jsonAlertOutputFormat {
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					return encoder.Encode(rawManifest)
				}

				// YAML output for raw dump
				encoder := yaml.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent(2)
				defer encoder.Close()
				return encoder.Encode(rawManifest)
			}

			// Convert to manifest format with verbosity-aware field selection
			manifest := alerts.APIResponseToManifest(result, ctx.Verbosity)

			// Process markdown in message field for JSON/YAML output (plain text)
			markdownFields := []string{"message"}
			processedManifest := markdown.ProcessFieldsPlain(manifest, markdownFields)

			// For JSON output, encode the processed manifest
			if outputFormat == jsonAlertOutputFormat {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(processedManifest)
			}

			// For table format, use the printer with markdown rendering for message
			if outputFormat == tableAlertOutputFormat {
				p, err := printer.NewPrinterFromCmdWithMarkdown(cmd, markdownFields)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				return p.Print(manifest)
			}

			// YAML output (default)
			encoder := yaml.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent(2)
			defer encoder.Close()
			return encoder.Encode(processedManifest)
		},
	}

	return cmd
}
