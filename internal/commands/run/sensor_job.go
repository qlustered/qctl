package run

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewSensorJobCommand creates the 'run sensor-job' command.
//
// It deliberately has none of its siblings' bulk machinery. `run ingestion-job`
// and `run profiling-job` take job ids and offer --filter/--dry-list/--yes
// because there is a listing of those jobs to select over. There is no sensor
// job listing at all, and the route takes exactly one data source, so a
// --filter here would have nothing to filter and a --dry-list nothing to list.
func NewSensorJobCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sensor-job <cloud-source-id>",
		Short: "Check one cloud source for new files now",
		Long: `Queue one immediate check for new files on ONE cloud source.

The positional is a CLOUD SOURCE id, not a job id -- unlike 'run ingestion-job'
and 'run profiling-job', whose positionals are job ids. Find one with
'qctl get cloud-sources'.

The sensor lists the source's location and enqueues every file it has not seen
before, which then ingest as usual. It does not wait for the schedule, and it
does not change the schedule.

One id only: there is no sensor-job listing to filter over, so this command has
no --filter, no --dry-list and no bulk mode.

Only these source types have a sensor:

  ` + cloud_sources.SensorEligibleTypeList() + `

qctl refuses any other type -- an upload source (qluster_ui) or a composite
source -- and every source that is not active, before sending the request. The
server also rejects deleted sources with 404, and drafts and source types
without sensor support with 400; the disabled check is qctl's stricter
precondition.

Queuing the same sensor twice fails with the server's 409 "Sensor is already
queued to be run" (exit 2), and the queued run is moved nearer the top of the
queue.

Examples:
  # look for new files right now
  qctl run sensor-job 42`,
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
			if err := cloud_sources.RequireSensorEligible(src); err != nil {
				return err
			}
			if err := cloud_sources.RequireActive(src, "check for new files"); err != nil {
				return err
			}

			if err := client.RunSensorJob(token, id); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"Sensor queued for cloud source %d (%s). Files it has not seen will be enqueued for ingestion.\n",
				src.ID, terminal.Sanitize(src.Name))
			return nil
		},
	}
}
