package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewProfilingJobCommand creates the describe profiling-job command
func NewProfilingJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiling-job <id>",
		Short: "Show details of a specific profiling job",
		Long: `Show details of a specific profiling job including its configuration and status.

For YAML output (default), the profiling job is converted into a manifest-style document
with spec and status, mirroring kubectl describe output. JSON uses the same manifest shape
for reliable parsing by automation and AI agents.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: ProfilingJob
  metadata:
    name: user_data
    labels:
      dataset_id: "11"
      dataset_name: "user_data"
    annotations:
      profiling_job_id: "1234"
  spec:
    id: 1234
    dataset_id: 11
    dataset_name: user_data
    settings_model_id: 30
  status:
    state: finished
    step: finished
    attempt_id: 1
    unresolved_alerts: 0
    created_at: 2024-01-02T15:04:05Z`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse profiling job ID from arg
			jobID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid profiling job ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create profiling client
			client, err := profiling.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Get profiling job
			result, err := client.GetProfilingJob(ctx.Credential.AccessToken, jobID)
			if err != nil {
				return fmt.Errorf("failed to get profiling job: %w", err)
			}

			manifest := profiling.APIResponseToManifest(result)

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
