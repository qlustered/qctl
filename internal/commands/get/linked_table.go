package get

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/backup_settings"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_dry_runs"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewLinkedTableCommand creates the get linked-table command.
func NewLinkedTableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "linked-table [table]",
		Short: "Show a linked table's spec, or export it as a manifest",
		Long: `Show a linked table's spec revision, or export it as a 'kind: LinkedTable'
manifest that 'qctl apply -f' and 'qctl diff -f' accept.

The table is the linked table, as a positional id-or-name, --table <name> or
--table-id <id>. The revision is the table's latest spec revision -- the
newest one, whatever its state -- unless --revision names another revision of
the same linked table.

Default output is one row: the table, the revision's release and state, the
spine source, and how many sources and output columns the spec has.

-o yaml|json outputs the manifest, plus a status block that apply and diff
do not use. Edit it and feed it back to change the table:

  qctl get linked-table orders_enriched -o yaml > orders_enriched.yaml
  # edit orders_enriched.yaml
  qctl diff -f orders_enriched.yaml
  qctl apply -f orders_enriched.yaml

The manifest has no spec.release, so an edited spec gets the next vN release
on apply; the revision's own release is status.release. spec.desired_state is
enabled when the revision is enabled or has a pending auto-enable, and draft
otherwise. When reading the
table's backup settings is refused, or they have no name,
spec.backup_settings is left out with a warning on stderr.

-o name prints linked-table/<table-id>.

Examples:
  qctl get linked-table orders_enriched
  qctl get linked-table orders_enriched -o yaml
  qctl get linked-table --table-id 12 --revision 550e8400-e29b-41d4-a716-446655440000 -o yaml`,
		Annotations: catalog.For("linked-table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runGetLinkedTable,
	}

	cmdutil.AddTableFlags(cmd, "Linked table")
	cmd.Flags().String("revision", "", "Spec revision UUID to show (default: the table's latest revision)")

	cmdutil.DeclareColumns(cmd, []linked_tables.Summary(nil))
	return cmd
}

func runGetLinkedTable(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
		return err
	}
	var revisionID *uuid.UUID
	if cmd.Flags().Changed("revision") {
		raw, _ := cmd.Flags().GetString("revision")
		id, err := cmdutil.ParseUUIDArg(raw, "--revision")
		if err != nil {
			return err
		}
		revisionID = &id
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
	if err != nil {
		return err
	}
	outputFormat, _ := cmd.Flags().GetString("output")
	manifestFormat := outputFormat == "yaml" || outputFormat == "json" //nolint:goconst // Keep output format handling local to this command.
	manifest, summary, err := exportLinkedTable(cmd, ctx, tableID, revisionID, manifestFormat)
	if err != nil {
		return err
	}

	switch outputFormat {
	case "yaml", "json": //nolint:goconst // Keep output format handling local to this command.
		return encodeStructured(cmd, outputFormat, manifest)
	case string(output.FormatName):
		fmt.Fprintf(cmd.OutOrStdout(), "linked-table/%d\n", tableID)
		return nil
	default:
		printContextBanner(cmd, ctx)
		setDefaultColumns(cmd, "name,table_id,release,state,spine,sources,output_columns,destination")
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return fmt.Errorf("failed to create output printer: %w", err)
		}
		return printer.Print(summary)
	}
}

// exportLinkedTable reads the linked table tableID and the revision
// ResolveRevision picks for revisionID, and returns their manifest. The
// YAML/JSON exports also read backup settings and pending auto-enable intents.
func exportLinkedTable(cmd *cobra.Command, ctx *cmdutil.CommandContext, tableID int, revisionID *uuid.UUID, manifestFormat bool) (*linked_tables.LinkedTableManifestWithStatus, *linked_tables.Summary, error) {
	token := ctx.Credential.AccessToken
	ltClient, err := linked_tables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client: %w", err)
	}
	revision, err := ltClient.ResolveRevision(token, tableID, revisionID)
	if err != nil {
		return nil, nil, err
	}
	detail, err := ltClient.GetRevisionDetail(token, revision.ID)
	if err != nil {
		return nil, nil, err
	}
	pendingAutoEnable := false
	if manifestFormat && detail.State != "enabled" { //nolint:goconst // Revision state is a wire value.
		pendingAutoEnable, err = linkedTablePendingAutoEnable(ctx, detail.ID)
		if err != nil {
			return nil, nil, err
		}
	}

	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	dataset, err := dsClient.GetDataset(token, tableID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read table %d: %w", tableID, err)
	}
	names := map[int]string{}
	for _, id := range linked_tables.SourceDatasetIDs(detail) {
		source, err := dsClient.GetDataset(token, id)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read source table %d: %w", id, err)
		}
		names[id] = source.Name
	}
	var backupSettings *string
	if manifestFormat {
		backupSettings, err = backupSettingsName(cmd, ctx, dataset.BackupSettingsID)
		if err != nil {
			return nil, nil, err
		}
	}

	manifest, err := linked_tables.ExportManifest(linked_tables.LiveTable{
		ID:               dataset.ID,
		Name:             dataset.Name,
		Database:         dataset.DatabaseName,
		Schema:           dataset.SchemaName,
		TableName:        dataset.TableName,
		Destination:      dataset.DestinationName,
		BackupSettingsID: dataset.BackupSettingsID,
		BackupSettings:   backupSettings,
	}, detail, names, pendingAutoEnable)
	if err != nil {
		return nil, nil, err
	}
	summary := linked_tables.SummaryOf(manifest)
	summary.Name = dataset.Name
	return manifest, summary, nil
}

// linkedTablePendingAutoEnable checks unfinished dry runs of one revision for
// an auto-enable intent still pending.
func linkedTablePendingAutoEnable(ctx *cmdutil.CommandContext, revisionID uuid.UUID) (bool, error) {
	client, err := composite_dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return false, fmt.Errorf("failed to create composite dry run client: %w", err)
	}
	var total *int
	seen := 0
	for page := 1; ; page++ {
		jobs, err := client.List(ctx.Credential.AccessToken, composite_dry_runs.ListParams{
			RevisionID: &revisionID,
			States:     []string{string(api.IngestionJobStateCreated), string(api.IngestionJobStateRunning)},
			Page:       page,
			Limit:      100,
		})
		if err != nil {
			return false, fmt.Errorf("failed to list composite dry runs for revision %s: %w", revisionID, err)
		}
		if page == 1 {
			total = jobs.TotalRows
		}
		for _, job := range jobs.Results {
			detail, err := client.Get(ctx.Credential.AccessToken, job.ID)
			if err != nil {
				return false, fmt.Errorf("failed to read composite dry run %d: %w", job.ID, err)
			}
			if detail.Job.AutoEnableState != nil && *detail.Job.AutoEnableState == api.CompositeDryRunAutoEnableStatePending {
				return true, nil
			}
		}
		seen += len(jobs.Results)
		if len(jobs.Results) == 0 {
			return false, nil
		}
		if total != nil {
			if seen >= *total {
				return false, nil
			}
		} else if jobs.Next == nil {
			return false, nil
		}
	}
}

// backupSettingsName returns the name of the backup settings id, or nil with
// a warning on stderr when reading them is refused or they have no name.
func backupSettingsName(cmd *cobra.Command, ctx *cmdutil.CommandContext, id int) (*string, error) {
	client, err := backup_settings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	settings, err := client.Get(ctx.Credential.AccessToken, id)
	reason := ""
	switch {
	case err != nil && qerrors.GetExitCode(err) == qerrors.ExitUnauthorized:
		reason = "reading them was refused; it needs backup:list"
	case err != nil:
		return nil, fmt.Errorf("failed to read backup settings %d: %w", id, err)
	case settings.Name == nil || *settings.Name == "":
		reason = "they have no name"
	default:
		return settings.Name, nil
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"Warning: spec.backup_settings is left out because the name of backup settings %d is unknown (%s); "+
			"apply then uses the organization's only backup settings, and refuses the manifest when there are several\n",
		id, reason)
	return nil, nil
}
