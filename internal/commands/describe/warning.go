package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewWarningCommand creates the describe warning command
func NewWarningCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "warning <id>",
		Short: "Show details of a specific warning",
		Long: `Show details of a specific warning including its configuration and details.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: Warning
  metadata:
    name: schema-drift
  spec:
    dataset_id: 10
    dataset_name: user_data
    message: "Schema drift detected"
    issue_type: SCHEMA_DRIFT
  status:
    id: 55
    is_row_level: false
    created_at: 2024-01-02T15:04:05Z`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse warning ID from arg
			warningID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid warning ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create warnings client
			client := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			// Get warning
			result, err := client.GetWarning(ctx.Credential.AccessToken, warningID)
			if err != nil {
				return fmt.Errorf("failed to get warning: %w", err)
			}

			// Get output format - default to YAML for describe commands
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = "yaml"
			}

			// Fields that contain markdown
			markdownFields := []string{"message", "msg"}

			// For table format, use the printer with markdown rendering
			if outputFormat == "table" {
				p, err := printer.NewPrinterFromCmdWithMarkdown(cmd, markdownFields)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				return p.Print(result)
			}

			// Process markdown in message fields for JSON/YAML output (plain text)
			processedResult := markdown.ProcessFieldsPlain(result, markdownFields)

			// For JSON output
			if outputFormat == "json" {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(processedResult)
			}

			// YAML output (default)
			encoder := yaml.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent(2)
			defer encoder.Close()
			return encoder.Encode(processedResult)
		},
	}

	return cmd
}
