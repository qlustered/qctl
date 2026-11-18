package disable

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCloudSourceCommand creates the 'disable cloud-source' command.
func NewCloudSourceCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "cloud-source <id>",
		Short: "Turn a cloud source off",
		Long: `Turn a cloud source off by setting its state to "disabled".

This is wider than "stop polling", which is why it is spelled out here. While a
source is disabled:
  - for a type with a sensor, its cronjob is removed, so nothing polls it for
    new files
  - direct file uploads to it are rejected by the server with a 400
  - dry runs against it are rejected the same way
  - it is excluded from composite runs, drift watching and stuck-job healing

Nothing is lost. The schedule is kept, so 'qctl enable cloud-source <id>' can
turn the source back on and, for a type with a sensor, restore polling on the
same expression -- which is why there is no confirmation prompt. That holds for
every source this command accepts: a composite source is refused (exit 2)
before any write, because 'qctl enable cloud-source' does not turn composite
sources back on.

Two caveats apply to turning a source with a sensor back on. The enable runs a
connection check first, and a source that fails it stays disabled. And the
scheduler applies a disable asynchronously, so wait a minute before enabling,
then a minute later confirm with 'qctl describe cloud-source <id>' that the
source is still active, and enable it again if it is not: if the scheduler gets
to the disable after the enable, it turns the source off again.

A source that was deleted is refused. 'qctl undelete cloud-source' attempts
recovery, but current servers reject its request with 404 too.

Examples:
  # stop a source without losing its configuration
  qctl disable cloud-source 42`,
		Annotations: cmdutil.WithFormats(catalog.For("cloud-source"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := cmdutil.ParseIntArgs(args, "cloud source id")
			if err != nil {
				return err
			}
			id := ids[0]

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			token := ctx.Credential.AccessToken

			src, err := client.GetCloudSourceTiny(token, id)
			if err != nil {
				return err
			}
			if err := cloud_sources.RequireNotDeleted(src, "disabled"); err != nil {
				return err
			}
			if err := cloud_sources.RequireNotCompositeToDisable(src); err != nil {
				return err
			}

			if err := client.DisableCloudSource(token, id); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Cloud source %d (%s) is now disabled.\n", src.ID, terminal.Sanitize(src.Name))
			// src is read before the write, which sends no schedule.
			schedule := cloud_sources.ScheduleOf(src)
			switch {
			case !cloud_sources.SensorEligible(src.DataSourceType):
				fmt.Fprintln(out, "It has no sensor, so nothing was polling it.")
			case src.State != api.DataSourceStateActive:
				fmt.Fprintf(out, "It was %s, not active, so nothing was polling it.\n",
					terminal.Sanitize(string(src.State)))
				if schedule != "" {
					fmt.Fprintf(out, "Its schedule '%s' is kept for 'qctl enable cloud-source %d'.\n",
						terminal.Sanitize(schedule), src.ID)
				}
			case schedule != "":
				fmt.Fprintf(out, "Polling stopped; the schedule '%s' is kept for 'qctl enable cloud-source %d'.\n",
					terminal.Sanitize(schedule), src.ID)
			default:
				fmt.Fprintln(out, "Nothing was polling it -- there is no schedule to keep.")
			}
			fmt.Fprintln(out, "Uploads and dry runs against it will now fail until it is enabled again.")
			return nil
		},
	}
}
