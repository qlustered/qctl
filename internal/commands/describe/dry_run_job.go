package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	defaultDryRunJobOutputFormat = "yaml"
	jsonDryRunJobOutputFormat    = "json"
	tableDryRunJobOutputFormat   = "table"
)

// NewDryRunJobCommand creates the describe dry-run-job command
func NewDryRunJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dry-run-job <id>",
		Short: "Show details of a specific dry-run job",
		Long: `Show details of a specific dry-run job including its configuration and status.

By default, only essential fields are shown. Use -v for full details including
sampling metadata and metrics, or -vvv for a raw dump of the complete API response.

Verbosity levels:
  (default)  Essential fields: state, rules, timestamps
  -v         Full details: metrics, sampling, structured summary
  -vvv       Raw API response dump (all fields, useful for debugging)

Examples:
  qctl describe dry-run-job 123
  qctl describe dry-run-job 123 -v
  qctl describe dry-run-job 123 -vvv
  qctl describe dry-run-job 123 -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid dry-run job ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			result, err := client.GetDryRunJob(ctx.Credential.AccessToken, jobID)
			if err != nil {
				return fmt.Errorf("failed to get dry-run job: %w", err)
			}

			// Get output format
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = defaultDryRunJobOutputFormat
			}

			// For -vvv (verbosity >= 3), output raw API response
			if ctx.Verbosity >= 3 {
				rawManifest, err := dry_runs.APIResponseToRawManifest(result)
				if err != nil {
					return fmt.Errorf("failed to build raw manifest: %w", err)
				}

				if outputFormat == jsonDryRunJobOutputFormat {
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					return encoder.Encode(rawManifest)
				}

				encoder := yaml.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent(2)
				defer encoder.Close()
				return encoder.Encode(rawManifest)
			}

			// Convert to manifest format with verbosity-aware field selection
			manifest := dry_runs.APIResponseToManifest(result, ctx.Verbosity)

			// Process markdown in summary field for JSON/YAML output
			markdownFields := []string{"summary"}
			processedManifest := markdown.ProcessFieldsPlain(manifest, markdownFields)

			if outputFormat == jsonDryRunJobOutputFormat {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(processedManifest)
			}

			if outputFormat == tableDryRunJobOutputFormat {
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
