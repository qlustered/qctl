package describe

import (
	"fmt"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewTableSeriesCommand creates the describe table-series command.
func NewTableSeriesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-series <name-or-id>",
		Short: "Describe a table series and its period tables",
		Long: `Read a table series by exact name or UUID in the current organization, and
its deadline schedule with the dates of one period: --period, else the period
being collected. The default view shows its details, members and Deadlines;
-o json or -o yaml emits the API record plus deadline_schedule, and -o name
prints its table-series/<uuid> reference without reading the schedule. When
the series reads but its schedule does not, the view prints without the
Deadlines (deadline_schedule: null) and the command exits with the schedule
error's code.`,
		Annotations: cmdutil.WithFormats(catalog.For("table-series"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := table_series.ValidateReference(args[0]); err != nil {
				return err
			}
			period, _ := cmd.Flags().GetString("period")
			if cmd.Flags().Changed("period") {
				if err := table_series.ValidatePeriodKey(period); err != nil {
					return err
				}
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			id, err := c.ResolveID(ctx.Credential.AccessToken, args[0])
			if err != nil {
				return err
			}
			item, err := c.Get(ctx.Credential.AccessToken, id)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("output")
			if format == string(output.FormatName) {
				return tableui.PrintFromCmd(cmd, item, "")
			}
			schedule, err := c.GetDeadlineSchedule(ctx.Credential.AccessToken, id, period)
			var writeErr error
			if format == outputFormatJSON || format == outputFormatYAML {
				writeErr = output.WriteRaw(cmd.OutOrStdout(), format, tableSeriesDescription{item, schedule})
			} else {
				writeErr = printTableSeriesDetail(cmd, item, schedule, period == "")
			}
			if err != nil {
				return err
			}
			return writeErr
		},
	}
	cmd.Flags().String("period", "", "The period whose deadline dates to show, e.g. 2026-09 or 2026-Q3 (default: the period being collected)")
	cmdutil.DeclareColumns(cmd, []table_series.Display(nil))
	return cmd
}

// tableSeriesDescription is describe table-series' JSON and YAML: the series record plus its deadline schedule.
type tableSeriesDescription struct {
	*table_series.Series
	DeadlineSchedule *table_series.DeadlineSchedule `json:"deadline_schedule" yaml:"deadline_schedule"`
}

func printTableSeriesDetail(cmd *cobra.Command, item *table_series.Series, schedule *table_series.DeadlineSchedule, collected bool) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Name: %s\nID: %s\n", terminal.Sanitize(item.Name), item.ID)
	if item.Description != nil {
		fmt.Fprintf(out, "Description: %s\n", terminal.Sanitize(*item.Description))
	}
	fmt.Fprintf(out, "Frequency: %s\n", terminal.Sanitize(string(item.Frequency)))
	if item.FirstPeriodKey != nil {
		fmt.Fprintf(out, "First period: %s\n", terminal.Sanitize(*item.FirstPeriodKey))
	}
	if item.PeriodColumn != nil {
		fmt.Fprintf(out, "Period column: %s (%s)\n", terminal.Sanitize(*item.PeriodColumn), periodColumnKind(item.PeriodColumnKind))
	}
	fmt.Fprintf(out, "Created: %s\nUpdated: %s\nMembers: %d\n",
		item.CreatedAt.Format(time.RFC3339), item.UpdatedAt.Format(time.RFC3339), len(item.Members))
	if len(item.Members) > 0 {
		// The member grid ends with a blank row of its own.
		if err := tableui.PrintFromCmd(cmd, table_series.Rows([]table_series.Series{*item}), "period_key,table,table_id,period_start,period_end,is_locked,locked_by,locked_at"); err != nil {
			return err
		}
	}
	if schedule == nil {
		return nil
	}
	if len(item.Members) == 0 {
		fmt.Fprintln(out)
	}
	return table_series.RenderDeadlines(out, schedule, collected)
}

func periodColumnKind(kind *api.PeriodColumnKind) string {
	if kind == nil {
		return "unknown"
	}
	return terminal.Sanitize(string(*kind))
}
