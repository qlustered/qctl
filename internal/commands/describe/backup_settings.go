package describe

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/backup_settings"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
)

// NewBackupSettingsCommand creates the describe backup-settings command.
func NewBackupSettingsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "backup-settings <id>",
		Short: "Show details of specific backup settings",
		Long: `Show backup settings as a manifest targeting metadata.id in the current organization.

The ID lets apply update named and unnamed configurations. Keep metadata.id
to update this configuration; add metadata.name to name or rename it. To copy
it into a new configuration, remove metadata.id and set a new metadata.name.

Credential fields are omitted from every output format. Applying the emitted
manifest therefore preserves the credentials already stored by the server.`,
		Annotations: cmdutil.WithFormats(catalog.For("backup-settings"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runBackupSettings,
	}
}

func runBackupSettings(cmd *cobra.Command, args []string) error {
	ids, err := cmdutil.ParseIntArgs(args, "backup settings ID")
	if err != nil {
		return err
	}
	if ids[0] <= 0 {
		return qerrors.NewBadUsagef("backup settings ID must be a positive integer, got %d", ids[0])
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := backup_settings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	result, err := client.Get(ctx.Credential.AccessToken, ids[0])
	if err != nil {
		return fmt.Errorf("failed to get backup settings: %w", err)
	}
	manifest := backup_settings.APIResponseToManifest(result)

	outputFormat, _ := cmd.Flags().GetString("output")
	if !cmd.Flags().Changed("output") {
		outputFormat = outputFormatYAML
	}
	switch outputFormat {
	case outputFormatJSON:
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(manifest)
	case outputFormatTable:
		p, err := printer.NewPrinterFromCmd(cmd)
		if err != nil {
			return fmt.Errorf("failed to create output printer: %w", err)
		}
		return p.Print(manifest)
	default:
		encoder := jsonnum.NewYAMLEncoder(cmd.OutOrStdout())
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(manifest)
	}
}
