package apply

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/backup_settings"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewBackupSettingsCommand creates the apply backup-settings command.
func NewBackupSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup-settings",
		Short: "Apply a backup settings configuration from a file",
		Long: `Apply backup settings from a YAML file.

With metadata.id, the command updates that ID in the current organization;
a missing ID is an error. metadata.name is optional in this mode: omit it to
keep the current name, or set it to name or rename the selected configuration.
Without metadata.id, the command creates a resource when metadata.name is new
and updates the exact-name match otherwise.
Omitted credential fields preserve their stored values on updates.
Creating s3 or s3_compatible backup settings requires both
an access key and a secret key.
A string field that is exactly ${VAR} reads that environment variable;
other values, including literal dollar signs, are preserved.`,
		Annotations: cmdutil.WithFormats(catalog.For("backup-settings"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return qerrors.NewBadUsage("filename is required (-f or --filename)")
			}
			return applyStream(cmd, filePath, onlyKind(applyBackupSettings))
		},
	}
	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file (required)")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

func applyBackupSettings(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	backupManifest, err := parseBackupSettingsManifest(doc.Data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to load manifest")
	}
	processBackupSettingsEnvironmentVariables(backupManifest)
	if validationErrors := backupManifest.Validate(); len(validationErrors) > 0 {
		messages := make([]string, 0, len(validationErrors))
		for _, validationErr := range validationErrors {
			messages = append(messages, validationErr.Error())
		}
		return nil, qerrors.NewBadUsagef("manifest validation failed:\n  - %s", strings.Join(messages, "\n  - "))
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	client, err := backup_settings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	result, err := client.Apply(ctx.Credential.AccessToken, backupManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to apply backup settings: %w", err)
	}
	name := result.Name
	if name == "" {
		name = fmt.Sprint(result.ID)
	}
	fmt.Fprintf(humanOut(cmd), "backup-settings/%s %s\n",
		terminal.Sanitize(name), terminal.Sanitize(result.Action))
	return namedApplyResult(result, "backup-settings", result.ID), nil
}

func parseBackupSettingsManifest(data []byte) (*backup_settings.Manifest, error) {
	genericManifest, err := manifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	if genericManifest.Kind != "BackupSettings" {
		return nil, qerrors.NewBadUsagef("expected kind 'BackupSettings', got '%s'", genericManifest.Kind)
	}
	var typed backup_settings.Manifest
	if err := manifest.StrictUnmarshal(data, &typed); err != nil {
		return nil, err
	}
	return &typed, nil
}

func processBackupSettingsEnvironmentVariables(m *backup_settings.Manifest) {
	fields := []*string{
		m.Spec.BackupGPGPublicKey, m.Spec.BackupGPGRecipient,
		m.Spec.BackupGPGPrivateKey, m.Spec.BackupGPGPassphrase,
		m.Spec.BackupS3AccessKey, m.Spec.BackupS3SecretKey,
		m.Spec.BackupS3EndpointURL, m.Spec.BackupS3RegionName,
		m.Spec.BackupS3Bucket, m.Spec.BackupGSServiceAccountKey,
		m.Spec.BackupGSBucket,
	}
	for _, field := range fields {
		if field != nil && *field != "" {
			*field = expandEnvVars(*field)
		}
	}
}
