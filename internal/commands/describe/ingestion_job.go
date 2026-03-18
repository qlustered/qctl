package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewIngestionJobCommand creates the describe ingestion-job command
func NewIngestionJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingestion-job <id>",
		Short: "Show details of a specific ingestion job",
		Long: `Show details of a specific ingestion job including its configuration and status.

For YAML output (default), the ingestion job is converted into a manifest-style document
with spec and status, mirroring kubectl describe output. JSON uses the same manifest shape
for reliable parsing by automation and AI agents.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: IngestionJob
  metadata:
    name: orders-file
    labels:
      dataset_id: "11"
      data_source_model_id: "22"
    annotations:
      ingestion_job_id: "1234"
  spec:
    id: 1234
    dataset_id: 11
    dataset_name: orders
    data_source_model_id: 22
    data_source_model_name: s3-bucket
    settings_model_id: 30
    stored_item_id: 77
    file_name: orders.csv
    key: files/77/orders.csv
    is_dry_run: false
  status:
    state: finished
    try_count: 1
    attempt_id: 0
    clean_rows_count: 1200
    bad_rows_count: 3
    ignored_rows_count: 0
    created_at: 2024-01-02T15:04:05Z
    updated_at: 2024-01-02T15:05:10Z`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse ingestion job ID from arg
			jobID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid ingestion job ID: %s", args[0])
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create ingestion client
			client, err := ingestion.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return err
			}

			// Get ingestion job
			result, err := client.GetIngestionJob(ctx.Credential.AccessToken, jobID)
			if err != nil {
				return fmt.Errorf("failed to get ingestion job: %w", err)
			}

			manifest := ingestion.APIResponseToManifest(result)

			// Determine output format (default to YAML manifest for describe)
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = "yaml"
			}

			// Fields that contain markdown (in logs)
			markdownFields := []string{"message"}

			switch outputFormat {
			case "json":
				// Process markdown in message field for JSON output (plain text)
				processedManifest := markdown.ProcessFieldsPlain(manifest, markdownFields)
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(processedManifest)
			case "table":
				p, err := printer.NewPrinterFromCmd(cmd)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				if err := p.Print(manifest); err != nil {
					return fmt.Errorf("failed to print output: %w", err)
				}

				// Render logs as a dedicated table for readability
				if manifest.Status != nil && len(manifest.Status.Logs) > 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "\nLogs:")
					// Use markdown rendering for the message field in logs
					logPrinter := printer.NewPrinter(printer.Options{
						Format:         printer.FormatTable,
						Writer:         cmd.OutOrStdout(),
						Columns:        []string{"timestamp", "publisher", "message"},
						MarkdownFields: markdownFields,
					})
					return logPrinter.Print(manifest.Status.Logs)
				}
				return nil
			default:
				// Process markdown in message field for YAML output (plain text)
				processedManifest := markdown.ProcessFieldsPlain(manifest, markdownFields)
				encoder := yaml.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent(2)
				defer encoder.Close()
				return encoder.Encode(processedManifest)
			}
		},
	}

	return cmd
}
