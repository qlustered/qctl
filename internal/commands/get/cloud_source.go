package get

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewCloudSourceCommand creates the singular "get cloud-source <id>" command.
func NewCloudSourceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloud-source <id>",
		Short: "Get one cloud source, or its manifest with -o yaml|json",
		Long: `Get one cloud source by ID.

By default, shows the columns 'qctl get cloud-sources' shows. Use --columns to
pick other fields of the source.

-o yaml|json print the declarative manifest for 'qctl apply -f', with secrets
left out. The export can update an existing source as is. To create an sftp
source, add spec.sftp_password or spec.sftp_ssh_key; to create a dropbox source,
add spec.dropbox_authorization_code. 'qctl describe cloud-source <id>' is the
human summary.

Examples:
  qctl get cloud-source 42
  qctl get cloud-source 42 -o yaml > s3-orders.yaml`,
		Annotations: catalog.For("cloud-source"),
		Args:        cobra.ExactArgs(1),
		RunE:        runGetCloudSource,
	}
	cmdutil.DeclareColumns(cmd, []cloud_sources.CloudSourceFull(nil))
	return cmd
}

func runGetCloudSource(cmd *cobra.Command, args []string) error {
	cloudSourceID, err := strconv.Atoi(args[0])
	if err != nil {
		return qerrors.NewBadUsagef("invalid cloud source ID: %s", args[0])
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	result, err := client.GetCloudSource(ctx.Credential.AccessToken, cloudSourceID)
	if err != nil {
		return fmt.Errorf("failed to get cloud source: %w", err)
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	switch outputFormat { //nolint:goconst // Keep output format handling local to this command.
	case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
		manifest, err := cloud_sources.APIResponseToManifest(result)
		if err != nil {
			return err
		}
		return encodeStructured(cmd, outputFormat, manifest)
	}

	setDefaultColumns(cmd, cloudSourceDefaultColumns)

	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	if err := printer.Print(result); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}
	return nil
}
