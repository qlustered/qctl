package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewCloudSourceCommand creates the describe cloud-source command
func NewCloudSourceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloud-source <id>",
		Short: "Show details of a specific cloud source",
		Long: `Show details of a specific cloud source including its configuration and settings.

For YAML output (default), the cloud source is converted into a declarative manifest that can
be used with 'qctl apply cloud-source -f'. JSON output also returns the manifest shape for
machine parsing.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: CloudSource
  metadata:
    name: s3-orders
    labels:
      dataset_id: "11"
  spec:
    dataset_id: 11
    data_source_type: s3
    settings_model_id: 2
    s3_bucket: raw-orders
    s3_prefix: incoming/
    s3_region_name: us-east-1
    s3_access_key: ${S3_ACCESS_KEY}
    s3_secret_key: ${S3_SECRET_KEY}
    schedule: "@hourly"
  status:
    id: 42
    state: active
    version_id: 3`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse cloud source ID from arg
			cloudSourceID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid cloud source ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create cloud sources client
			client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Get cloud source
			result, err := client.GetCloudSource(ctx.Credential.AccessToken, cloudSourceID)
			if err != nil {
				return fmt.Errorf("failed to get cloud source: %w", err)
			}

			manifest := cloud_sources.APIResponseToManifest(result)

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
