package apply

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/spf13/cobra"
)

// NewCloudSourceCommand creates the apply cloud-source command
func NewCloudSourceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloud-source",
		Short: "Apply a cloud source configuration from a file",
		Long: `Apply a cloud source configuration from a YAML file.

This command creates a new cloud source if it doesn't exist, or updates an existing
one with the same name within the target table. The operation is idempotent.

SCHEMA
======
  apiVersion: qluster.ai/v1            # Required. Must be "qluster.ai/v1"
  kind: CloudSource                    # Required. Must be "CloudSource"
  metadata:
    name: <string>                     # Required. Unique cloud source name
    labels:                            # Optional
      <key>: <value>
    annotations:                       # Optional
      <key>: <value>
  spec:
    dataset_id: <int>                  # Required. Target table ID
    data_source_type: <string>         # Required. s3|minio|sftp|gs|dropbox|simple_url
    settings_model_id: <int>           # Required. Settings template ID
    schedule: <string>                 # Optional. e.g. "@hourly" or cron
    pattern: <string>                  # Optional. File glob/pattern
    s3_bucket: <string>                # Required for s3/minio
    s3_region_name: <string>           # Optional
    s3_prefix: <string>                # Optional
    s3_endpoint_url: <string>          # Optional (minio/custom endpoints)
    s3_access_key: <string>            # Sensitive, supports ${ENV}
    s3_secret_key: <string>            # Sensitive, supports ${ENV}
    gs_bucket: <string>                # Required for gs
    gs_prefix: <string>                # Optional
    gs_service_account_key: <string>   # Sensitive, supports ${ENV}
    dropbox_access_token: <string>     # Sensitive, supports ${ENV}
    dropbox_folder: <string>           # Optional
    sftp_host: <string>                # Required for sftp
    sftp_port: <int>                   # Optional
    sftp_user: <string>                # Required for sftp
    sftp_password: <string>            # Sensitive, supports ${ENV}
    sftp_folder: <string>              # Optional
    sftp_ssh_key: <string>             # Sensitive, supports ${ENV}
    sftp_ssh_key_passphrase: <string>  # Sensitive, supports ${ENV}
    simple_url: <string>               # Required for simple_url sources

EXAMPLE
=======
# File: cloud-source-s3.yaml
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: s3-orders
spec:
  dataset_id: 11
  settings_model_id: 2
  data_source_type: s3
  s3_bucket: raw-orders
  s3_prefix: incoming/
  s3_region_name: us-east-1
  s3_access_key: ${S3_ACCESS_KEY}
  s3_secret_key: ${S3_SECRET_KEY}
  schedule: "@hourly"

# Apply the configuration:
export S3_ACCESS_KEY=abc
export S3_SECRET_KEY=def
qctl apply cloud-source -f cloud-source-s3.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return fmt.Errorf("filename is required (-f or --filename)")
			}
			return applyCloudSource(cmd, filePath)
		},
	}

	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file (required)")
	_ = cmd.MarkFlagRequired("filename")

	return cmd
}

// applyCloudSource is the shared apply logic for cloud source manifests, used by
// both the "apply cloud-source" subcommand and the generic "apply -f" dispatcher.
func applyCloudSource(cmd *cobra.Command, filePath string) error {
	sourceManifest, err := loadCloudSourceManifest(filePath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	processCloudSourceEnv(sourceManifest)

	if errs := sourceManifest.Validate(); len(errs) > 0 {
		var messages []string
		for _, e := range errs {
			messages = append(messages, e.Error())
		}
		return fmt.Errorf("manifest validation failed:\n  - %s", strings.Join(messages, "\n  - "))
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	result, err := client.Apply(ctx.Credential.AccessToken, sourceManifest)
	if err != nil {
		return fmt.Errorf("failed to apply cloud source: %w", err)
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	if outputFormat == "json" {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "cloud-source/%s %s\n", sourceManifest.Metadata.Name, result.Action)
	return nil
}

func loadCloudSourceManifest(path string) (*cloud_sources.CloudSourceManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	generic, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}

	if generic.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, fmt.Errorf("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, generic.APIVersion)
	}
	if generic.Kind != "CloudSource" {
		return nil, fmt.Errorf("expected kind 'CloudSource', got '%s'", generic.Kind)
	}

	var manifest cloud_sources.CloudSourceManifest
	if err := pkgmanifest.StrictUnmarshal(data, &manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

func processCloudSourceEnv(m *cloud_sources.CloudSourceManifest) {
	expandPointer(&m.Spec.S3AccessKey)
	expandPointer(&m.Spec.S3SecretKey)
	expandPointer(&m.Spec.GsServiceAccountKey)
	expandPointer(&m.Spec.DropboxAccessToken)
	expandPointer(&m.Spec.SftpPassword)
	expandPointer(&m.Spec.SftpSSHKey)
	expandPointer(&m.Spec.SftpSSHKeyPassphrase)
	expandPointer(&m.Spec.FilePassword)
	expandPointer(&m.Spec.GpgPrivateKey)
	expandPointer(&m.Spec.GpgPassphrase)
}

func expandPointer(field **string) {
	if field == nil || *field == nil {
		return
	}
	expanded := expandEnvVars(**field)
	if expanded == "" {
		*field = nil
		return
	}
	*field = &expanded
}
