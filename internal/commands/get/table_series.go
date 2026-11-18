package get

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

// NewTableSeriesCommand creates the get table-series command.
func NewTableSeriesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-series [name-or-id]",
		Short: "List table series and their period tables",
		Long: `List all table series in the current organization, or read one by exact name or UUID.

The table view shows each member's series frequency, period key, table name
and ID, dates, lock state, and who locked it and when. The series' first
expected period is the first_period_key column, which only a --columns list
naming it shows; --columns replaces the default columns.
JSON and YAML export an apply-compatible manifest for a selected series,
including its deadline schedule's overrides and switches;
a listing preserves the API records in a List envelope.
Use -o name for one table-series/<uuid> reference per series.

With --uploadable, list the caller's upload targets without series-admin or
table-detail reads. Each row shows a cloud_source_id and routing_mode, plus a
period_key, label and table_id for each selectable period. A selectable period
with table_id "-" gets its table when uploaded. Upload with
qctl upload file <path> --cloud-source-id <cloud_source_id>, adding
--period <period_key> only for uploader_chooses routing. JSON/YAML preserve
the API records in a List envelope; -o name prints one reference per series.
This flag takes no series reference and conflicts with --periods and --rule-approval.

With --periods, supply a full series UUID to list the caller's accessible
submission periods without series or table-detail reads. The table shows
period keys, table IDs, dates and lock state in server order, including locked
periods. JSON/YAML print a List of API records; -o name prints table/<id>.
Find the series UUID in describe cloud-source's table_series_id.

With --rule-approval, read one series' rule-set approval: whether the latest
approval matches the current rules, the current fingerprint, the table and
rule counts, and the latest approver and time. Requires review:list and a
grant on every table of the series (admin and ops roles need none). JSON/YAML
print the server's record, with the full rule set and every approval, newest
first; -o name prints table-series/<uuid>. Approve with qctl table-series approve-rules.

With --reloadable-files and --period <key>, list the files of that period's
table that qctl table-series recreate-period can reload (Qluster ops only;
any other account gets not found): every file but phantom, partition,
trashed, held and turned-down ones, in upload order. JSON/YAML print a List
of API records; -o name prints file/<id>.`,
		Annotations: catalog.For("table-series"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if handled, err := runTableSeriesMode(cmd, args); handled || err != nil {
				return err
			}
			if err := validateColumnsFlag(cmd, []table_series.Display(nil)); err != nil {
				return err
			}
			if len(args) > 0 {
				if err := table_series.ValidateReference(args[0]); err != nil {
					return err
				}
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if len(args) > 0 {
				return getTableSeries(cmd, c, ctx.Credential.AccessToken, args[0])
			}
			items, err := c.List(ctx.Credential.AccessToken)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "table series", []table_series.Display{}, nil)
				if err != nil || skip {
					return err
				}
			}
			printContextBanner(cmd, ctx)
			return printListPage(cmd, items, listPage{noun: "table series"}, func(data any) error {
				if list, ok := data.(output.List); ok {
					format, _ := cmd.Flags().GetString("output")
					return output.WriteRaw(cmd.OutOrStdout(), format, list)
				}
				return printTableSeries(cmd, items)
			})
		},
	}
	cmd.Flags().Bool("uploadable", false, "List accessible upload targets and their selectable periods")
	cmd.Flags().Bool("periods", false, "List accessible submission periods (requires a full series UUID)")
	cmd.Flags().Bool("rule-approval", false, "Show one series' rule-set approval state")
	cmd.Flags().Bool("reloadable-files", false, "List the files a recreate of --period can reload (Qluster ops)")
	cmd.Flags().String("period", "", "Exact reporting period key (with --reloadable-files)")
	cmdutil.DeclareColumns(cmd, []table_series.Display(nil), []table_series.SubmissionPeriodDisplay(nil),
		[]table_series.RuleApprovalDisplay(nil), []table_series.UploadableDisplay(nil),
		[]table_series.ReloadableFileDisplay(nil))
	return cmd
}

// runTableSeriesMode runs the mode a get table-series flag selects. It reports
// false when no mode flag is set.
func runTableSeriesMode(cmd *cobra.Command, args []string) (bool, error) {
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"uploadable", "periods", "rule-approval", "reloadable-files"}); err != nil {
		return true, err
	}
	if reloadable, _ := cmd.Flags().GetBool("reloadable-files"); reloadable {
		return true, getReloadableFiles(cmd, args)
	}
	if cmd.Flags().Changed("period") {
		return true, qerrors.NewBadUsage("--period is accepted only with --reloadable-files")
	}
	if uploadable, _ := cmd.Flags().GetBool("uploadable"); uploadable {
		return true, getUploadableTableSeries(cmd, args)
	}
	if ruleApproval, _ := cmd.Flags().GetBool("rule-approval"); ruleApproval {
		return true, getRuleApproval(cmd, args)
	}
	if periods, _ := cmd.Flags().GetBool("periods"); periods {
		return true, getSubmissionPeriods(cmd, args)
	}
	return false, nil
}

func getTableSeries(cmd *cobra.Command, c *table_series.Client, token, ref string) error {
	id, err := c.ResolveID(token, ref)
	if err != nil {
		return err
	}
	item, err := c.Get(token, id)
	if err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		schedule, err := c.GetDeadlineSchedule(token, id, "")
		if err != nil {
			return err
		}
		return output.WriteRaw(cmd.OutOrStdout(), format, table_series.FromAPI(item, schedule))
	}
	return printTableSeries(cmd, []table_series.Series{*item})
}

func printTableSeries(cmd *cobra.Command, items []table_series.Series) error {
	var data any = items
	if isTableFormat(cmd) {
		data = table_series.Rows(items)
	}
	return tableui.PrintFromCmd(cmd, data, table_series.Columns)
}
