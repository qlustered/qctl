package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/spf13/cobra"
)

// NewTableConfigCommand creates the get table-config command.
func NewTableConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		// [table], not <table>: the positional is optional — Args is
		// MaximumNArgs(1) and --table/--table-id are the alternative — and
		// every sibling that takes the table positionally spells it this way.
		Use:   "table-config [table]",
		Short: "Show a table's settings model and ingestion configuration",
		Long: `Show the configuration half of a table: the settings model (datetime formats,
null values, boolean spellings, value mappings, per-column overrides), backup
settings, migration policy, anomaly thresholds and retry limits.

The table may be given as a positional argument (ID or name), or via
--table <name> / --table-id <id>.

This is the part of a table you rarely need and never want by default, which is
why 'qctl describe table' no longer shows it. For the manifest you can edit and
re-apply, use 'qctl get table <table> -o yaml'.

-o json|yaml emit the same object as structured data.

Values marked as not anomalies are a read-only ledger and may be large. Pass
--show-not-anomalies to include them in any output format.

Examples:
  qctl get table-config orders
  qctl get table-config --table orders
  qctl get table-config orders --show-not-anomalies
  qctl get table-config 42 -o yaml`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("table-config"), "table", "json", "yaml"), "table"),
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
			tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
			if err != nil {
				return err
			}

			client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			result, err := client.GetDataset(ctx.Credential.AccessToken, tableID)
			if err != nil {
				return fmt.Errorf("failed to get table: %w", err)
			}

			showNotAnomalies, _ := cmd.Flags().GetBool("show-not-anomalies")
			config, err := datasets.TableConfigFromAPIResponse(result, showNotAnomalies)
			if err != nil {
				return err
			}

			outputFormat, _ := cmd.Flags().GetString("output")
			switch outputFormat {
			case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
				return encodeStructured(cmd, outputFormat, config)
			default:
				return datasets.RenderTableConfig(cmd, config)
			}
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().Bool("show-not-anomalies", false,
		"Include values marked as not anomalies per column (may be large)")
	return cmd
}
