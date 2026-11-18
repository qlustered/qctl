package diff

import (
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

func diffTableSeries(cmd *cobra.Command, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read TableSeries manifest: %w", err)
	}
	m, err := table_series.ParseManifest(data)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	p, err := c.Prepare(ctx.Credential.AccessToken, m)
	if err != nil {
		return fmt.Errorf("prepare table series diff: %w", err)
	}
	if len(p.Requests) == 0 {
		return nil
	}
	var preview *table_series.DeadlineSchedule
	if p.SchedulePreviewable() {
		preview, err = c.PreviewDeadlineSchedule(ctx.Credential.AccessToken, p.Current.ID, p.Schedule.Period.PeriodKey, p.ScheduleRequest().Body)
		if err != nil {
			return err
		}
	}
	out := cmd.OutOrStdout()
	if err := p.WriteRequests(out); err != nil {
		return err
	}
	switch {
	case preview != nil:
		err = table_series.WriteSchedulePreview(out, p.Schedule, preview)
	case p.ScheduleRequest() == nil:
	case p.Create:
		_, err = fmt.Fprintln(out, "Note: the deadline schedule is not previewed for a series that does not exist yet")
	default:
		_, err = fmt.Fprintln(out, "Note: the deadline schedule is not previewed for a series whose frequency changes")
	}
	if err != nil {
		return err
	}
	return qerrors.ErrDiffDifferences
}
