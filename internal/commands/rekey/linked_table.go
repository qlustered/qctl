package rekey

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/backup_settings"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewLinkedTableCommand creates the rekey linked-table command.
func NewLinkedTableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "linked-table [table]",
		Short: "Replace a linked table's natural keys from a manifest",
		Long: `Replace a linked table's natural keys using a kind: LinkedTable manifest.
Start with 'qctl get linked-table <table> -o yaml', edit
spec.spine.natural_key_columns and optionally natural_key_normalization,
sources and output, then set spec.release for the new draft revision.

The command checks the manifest's table identity against the selected table.
It creates a draft revision in a replacement spec group; it does not launch a
dry run or activate the new revision. The table keeps its last refreshed data
and is not refreshed until the new revision is activated. Activation rebuilds
the table from scratch and closes row reviews and notes whose rows disappear;
cell overrides do not carry over. The command asks for acknowledgement unless
--yes is set. A non-interactive caller must use --yes.

Examples:
  qctl get linked-table orders_enriched -o yaml > orders_enriched.yaml
  qctl rekey linked-table orders_enriched -f orders_enriched.yaml
  qctl rekey linked-table --table-id 12 -f orders_enriched.yaml --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("linked-table"), "table", "json", "yaml", "name"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runLinkedTable,
	}
	cmdutil.AddTableFlags(cmd, "Linked table")
	cmd.Flags().StringP("filename", "f", "", "LinkedTable manifest (required)")
	cmd.Flags().BoolP("yes", "y", false, "Acknowledge the re-key consequences without prompting")
	return cmd
}

func runLinkedTable(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
		return err
	}
	path, _ := cmd.Flags().GetString("filename")
	manifest, err := loadRekeyManifest(path)
	if err != nil {
		return err
	}
	return performRekey(cmd, positional, manifest)
}

func performRekey(cmd *cobra.Command, positional string, manifest *linked_tables.LinkedTableManifest) error {
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
	if err != nil {
		return err
	}
	ltClient, err := linked_tables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	revision, err := ltClient.LatestRevision(token, tableID)
	if err != nil {
		return err
	}
	detail, err := ltClient.GetRevisionDetail(token, revision.ID)
	if err != nil {
		return err
	}
	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	table, err := dsClient.GetDataset(token, tableID)
	if err != nil {
		return fmt.Errorf("failed to read table %d: %w", tableID, err)
	}
	if err := checkTableIdentity(manifest, table, detail); err != nil {
		return err
	}
	if err := checkBackupSettings(ctx, table, manifest); err != nil {
		return err
	}
	sourceIDs, err := resolveSourceIDs(dsClient, token, manifest)
	if err != nil {
		return err
	}
	if sourceIDs[manifest.Spec.Sources[manifest.Spec.Spine.Source].Dataset] != detail.Group.SpineDatasetID {
		return qerrors.NewBadUsage("spec.spine.source must name the selected table's existing primary source")
	}
	request, err := manifest.RekeyRequest(sourceIDs)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		"re-keying requires acknowledgement; pass --yes in a non-interactive session",
		consequence(terminal.Sanitize(table.Name))+" Continue? [y/N]: "); err != nil {
		return err
	}
	result, raw, err := ltClient.Rekey(token, revision.GroupID, request)
	if err != nil {
		return err
	}
	if result.Warnings != nil {
		linked_tables.WriteWarnings(cmd.ErrOrStderr(), *result.Warnings)
	}
	return writeResult(cmd, table.ID, table.Name, result, raw)
}

func loadRekeyManifest(path string) (*linked_tables.LinkedTableManifest, error) {
	if path == "" {
		return nil, qerrors.NewBadUsage("filename is required (-f or --filename)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	manifest, err := linked_tables.LoadManifest(data)
	if err != nil {
		return nil, err
	}
	if manifest.Spec.Release == nil || *manifest.Spec.Release == "" {
		return nil, qerrors.NewBadUsage("spec.release is required for re-keying")
	}
	if manifest.Spec.DryRun != nil {
		return nil, qerrors.NewBadUsage("spec.dry_run is not used by re-key; launch a dry run after re-keying")
	}
	return manifest, nil
}

func checkBackupSettings(ctx *cmdutil.CommandContext, table *datasets.DatasetFull, manifest *linked_tables.LinkedTableManifest) error {
	if manifest.Spec.BackupSettings == nil {
		return nil
	}
	backupClient, err := backup_settings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return err
	}
	backup, err := backupClient.GetByName(ctx.Credential.AccessToken, *manifest.Spec.BackupSettings)
	if err != nil {
		return fmt.Errorf("failed to resolve backup settings: %w", err)
	}
	if backup == nil || backup.ID != table.BackupSettingsID {
		return qerrors.NewBadUsage("spec.backup_settings does not match the selected table")
	}
	return nil
}

func checkTableIdentity(m *linked_tables.LinkedTableManifest, table *datasets.DatasetFull, detail *linked_tables.RevisionDetail) error {
	if m.Metadata.Name != table.Name {
		return qerrors.NewBadUsagef("metadata.name %q does not match selected table %q", m.Metadata.Name, table.Name)
	}
	schema := "public"
	if m.Spec.Table.Schema != nil {
		schema = *m.Spec.Table.Schema
	}
	if m.Spec.Table.Database != table.DatabaseName || schema != table.SchemaName || m.Spec.Table.Name != table.TableName {
		return qerrors.NewBadUsage("spec.table does not match the selected table; re-key cannot change its coordinates")
	}
	if m.Spec.Destination != table.DestinationName {
		return qerrors.NewBadUsage("spec.destination does not match the selected table")
	}
	if m.Spec.Spine.IdentityHashVersion != nil && *m.Spec.Spine.IdentityHashVersion != detail.Group.IdentityHashVersion {
		return qerrors.NewBadUsage("spec.spine.identity_hash_version does not match the selected table")
	}
	return nil
}

func resolveSourceIDs(client *datasets.Client, token string, m *linked_tables.LinkedTableManifest) (map[string]int, error) {
	wanted := map[string]bool{}
	for _, source := range m.Spec.Sources {
		wanted[source.Dataset] = true
	}
	ids := make(map[string]int, len(wanted))
	for _, name := range slices.Sorted(maps.Keys(wanted)) {
		page, err := client.GetDatasets(token, datasets.GetDatasetsParams{Name: &name, Limit: 1})
		if err != nil {
			return nil, fmt.Errorf("failed to look up source table %q: %w", name, err)
		}
		found := false
		for _, item := range page.Results {
			if item.Name == name {
				ids[name] = item.ID
				found = true
				break
			}
		}
		if !found {
			return nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("source table %q not found", name)))
		}
	}
	return ids, nil
}

func writeResult(cmd *cobra.Command, tableID int, name string, result *api.CompositeSpecGroupRekeyResponse, raw []byte) error {
	out := cmd.OutOrStdout()
	name = terminal.Sanitize(name)
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json":
		return output.WriteIndentedJSON(out, raw)
	case "yaml":
		return output.WriteJSONAsYAML(out, raw)
	case "name":
		_, err := fmt.Fprintf(out, "linked-table/%d\n", tableID)
		return err
	}
	fmt.Fprintf(out, "linked-table/%s re-keyed\n", name)
	fmt.Fprintf(out, "  revision : %s (%s)\n", terminal.Sanitize(result.Release), terminal.Sanitize(result.State))
	fmt.Fprintf(out, "  group    : %s (replaces %s)\n", result.CompositeSpecGroupID, result.ReplacedCompositeSpecGroupID)
	_, err := fmt.Fprintf(out, "Next: qctl create composite-dry-run-job --table-id %d --revision %s\n", tableID, result.CompositeSpecRevisionID)
	return err
}
