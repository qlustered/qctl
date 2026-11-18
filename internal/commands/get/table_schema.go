package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const tableSchemaJSONOutputFormat = "json"

// NewTableSchemaCommand creates the get table-schema command.
func NewTableSchemaCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "table-schema [table]",
		// The API calls a table's schema a "migration"
		// (GET .../datasets/{id}/migration-details), so both spellings resolve.
		Aliases: []string{"migration", "table-migration"},
		Short:   "Display a table's schema fields",
		Long: `Display the fields of a table's schema, including pending (draft) changes.

The table may be given by ID or name. Default output is a grid of fields with
their types. When a draft schema change is pending, an ACTION column shows
what would change (added/modified/removed) and rows show the draft definition.

-o name prints just the column names, one per line — fast and server-cached. It
flattens the current and draft schemas into a single list with nothing marking
which are pending; the default grid's ACTION column is what tells them apart,
and 'qctl describe table-schema -v' shows the per-field diffs.

-o json|yaml outputs a 'kind: TableSchema' manifest: one block per column with
its type and properties, plus a status block. Edit it and feed it back with
'qctl apply -f' to change the schema, or preview with 'qctl diff -f'. Verbosity
does not change this output. For the raw API response — per-field diffs and
notable values — use 'qctl describe table-schema -v -o yaml'.

The table may be given as a positional argument (ID or name), or via
--table <name> / --table-id <id>. --table is always a name; the positional
argument resolves as an ID first, which is why a table literally named "42"
needs the flag.

For a walkthrough, see: qctl guide inspect-schema

Examples:
  qctl get table-schema orders
  qctl get table-schema 42 -v
  qctl get table-schema orders -o name
  qctl get table-schema orders -o yaml
  qctl get table-schema --table orders
  qctl get table-schema --table-id 42`,
		Annotations: cmdutil.WithArgKinds(catalog.For("table-schema"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}
			if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
			if err != nil {
				return err
			}

			client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			outputFormat, _ := cmd.Flags().GetString("output")

			// -o name is served by the cached fields endpoint, which unions the
			// current and draft field names into one unlabelled list (atlas
			// migration_controller.py get_migration_fields ->
			// _get_all_field_names). It never falls back to the details endpoint.
			if outputFormat == "name" { //nolint:goconst // Keep output format handling local to this command.
				fields, err := client.GetSchemaFields(ctx.Credential.AccessToken, datasetID)
				if err != nil {
					return fmt.Errorf("failed to get table schema: %w", err)
				}
				for _, f := range fields {
					fmt.Fprintln(cmd.OutOrStdout(), terminal.Sanitize(f))
				}
				return nil
			}

			detail, err := client.GetSchemaDetails(ctx.Credential.AccessToken, datasetID)
			if err != nil {
				return fmt.Errorf("failed to get table schema: %w", err)
			}

			switch outputFormat {
			case tableSchemaJSONOutputFormat, "yaml": //nolint:goconst // Keep output format handling local to this command.
				// The apply manifest, not the raw migration detail. This is a
				// breaking change to this command's structured output and the
				// same trade `get table` already made (see the comment in
				// get/dataset.go): emitting the raw backend response is "a third
				// shape ... that nothing consumes and nothing can round-trip."
				// The raw detail survives verbatim on
				// `describe table-schema -v -o yaml`, which the resource page
				// already advertises as the agent surface.
				//
				// Verbosity no longer changes this output: status is always
				// emitted and notable values never are.
				return encodeStructured(cmd, outputFormat, datasets.SchemaDetailToManifest(detail))
			default:
				return datasets.RenderSchemaGrid(cmd, detail, ctx.Verbosity)
			}
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmdutil.DeclareColumns(cmd, []datasets.SchemaFieldDisplay(nil))
	return cmd
}
