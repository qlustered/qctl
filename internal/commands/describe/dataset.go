package describe

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/spf13/cobra"
)

// NewDatasetCommand creates the describe table command.
func NewDatasetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table [id-or-name]",
		Short: "Summarize a table: columns, rules, state, and what is blocking it",
		Long: `Summarize a table before acting on it: identity and state, series and period lock, destination and
data loading process, row counts, every column with the rules bound to it and
the rule it is the output of, the pending schema draft and undecided rule suggestions, the newest ingestion and
profiling job, who else the table is shared with, which columns hold personal
data and why (listed by hand, or the rules that map them), whether you see that
data masked (or unmasked by your role, or by a grant until when), and what is
currently holding the table.

The argument can be an integer ID or the table name. If the argument is a
number, it is treated as an ID. Otherwise, it is looked up by exact name match.
The --table <name> / --table-id <id> pair is the unambiguous alternative; use
--table for a name that looks numeric.

This view makes at least eight requests. Pending suggestions are paginated,
and update/delete suggestions each need a detail read when a draft exists.
For one column list, 'qctl get table-schema <table>' is one request.
Sections you lack permission for degrade to "(unavailable)"
rather than failing the command.

What this command deliberately does not show:

  qctl get table-schema <table>     the columns on their own
  qctl get table-config <table>     the settings model, backup and thresholds
  qctl get table <table> -o yaml    the apply-compatible manifest

-o json|yaml emit the same summary as a structured object.

Examples:
  qctl describe table orders
  qctl describe table --table 42
  qctl describe table 42 -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("table"), "table", "json", "yaml"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tableArg := ""
			if len(args) > 0 {
				tableArg = args[0]
			}
			if err := cmdutil.ValidateTableSelector(cmd, tableArg); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, err := cmdutil.ResolveTableID(ctx, cmd, tableArg)
			if err != nil {
				return err
			}

			summary, err := fetchTableSummary(cmd, ctx, datasetID)
			if err != nil {
				return err
			}

			// No Changed("output") special case here, unlike the manifest
			// describes: this command's default view is the text summary, so
			// an unset flag and -o table mean the same thing.
			outputFormat, _ := cmd.Flags().GetString("output")
			switch outputFormat {
			case outputFormatJSON, outputFormatYAML:
				return encodeSchemaStructured(cmd, outputFormat, summary)
			default:
				return datasets.RenderTableSummary(cmd, summary)
			}
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")

	return cmd
}
