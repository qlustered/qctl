package enable

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCloudSourceCommand creates the 'enable cloud-source' command.
func NewCloudSourceCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "cloud-source <id>",
		Short: "Turn a cloud source on",
		Long: `Turn a cloud source on by setting its state to "active".

An active source accepts direct file uploads, takes part in dry runs and
composite runs, and -- if it has a schedule and a sensor -- polls for new files
on that schedule.

The request depends on the source's type. These types have a sensor, and are
turned on through run-sensor-cronjob, which needs ` + api.OperationPermissions["run_sensor_cronjob_now"] + `:

  ` + cloud_sources.SensorEligibleTypeList() + `

The sensor request also checks access to the source's table: admin and ops
pass for any table in the organization; other permitted roles need a grant
that is not read-only.

Enabling one of them that has no schedule turns it on without starting any
polling; give it one with 'qctl apply'. Enabling a source that is already
active sends the request, but the server changes nothing and does not redeploy
its cronjob. When polling has stopped for an active source that looks correctly
configured, disable the source, wait a minute, enable it, then a minute later
confirm with 'qctl describe cloud-source <id>' that it is still active, and
enable it again if it is not: the scheduler applies a disable asynchronously,
and if it gets to it after the enable it turns the source off again.

Any other type except composite -- in practice an upload source, type
qluster_ui -- is turned on by a versioned update that sets only the state,
which needs source:edit. qctl reads the source's version_id, then writes state
"active" at that version; if something writes the source between the read and
the write, the server answers 409 (exit 2) -- run the command again. One that is
already active is reported as such, and nothing is sent. An external user
cannot change a source's state, so the server refuses this for them (403).
Nothing polls these sources; an upload source takes its files from 'qctl upload
file'.

A composite source is refused (exit 2) before any write: it belongs to the
linked table that owns it, and enable does not turn it on.

A source that was deleted is refused. 'qctl undelete cloud-source' attempts
recovery, but current servers reject its request with 404 too.

Examples:
  # turn a source back on
  qctl enable cloud-source 42`,
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
			if err := cloud_sources.RequireNotDeleted(src, "enabled"); err != nil {
				return err
			}
			if err := cloud_sources.RequireNotComposite(src); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if cloud_sources.SensorEligible(src.DataSourceType) {
				if err := client.EnableCloudSource(token, id); err != nil {
					return err
				}
				printSensorEnabled(out, src)
				return nil
			}
			if src.State != api.DataSourceStateActive {
				if err := client.ActivateCloudSource(token, id); err != nil {
					return err
				}
			}
			printActivated(out, src)
			return nil
		},
	}
}

// printSensorEnabled reports a source turned on through run-sensor-cronjob.
// src is read before the write; enabling does not change the schedule.
func printSensorEnabled(out io.Writer, src *cloud_sources.CloudSourceTiny) {
	if src.State == api.DataSourceStateActive {
		fmt.Fprintf(out, "Cloud source %d (%s) was already active; the server changed nothing and did not redeploy its cronjob.\n",
			src.ID, terminal.Sanitize(src.Name))
		fmt.Fprintln(out, "If polling has stopped, see 'qctl enable cloud-source --help'.")
		return
	}
	fmt.Fprintf(out, "Cloud source %d (%s) is now active.\n", src.ID, terminal.Sanitize(src.Name))
	if schedule := cloud_sources.ScheduleOf(src); schedule != "" {
		fmt.Fprintf(out, "Polling resumes on '%s'.\n", terminal.Sanitize(schedule))
	} else {
		fmt.Fprintln(out, "No schedule set -- nothing will poll; set one with 'qctl apply'.")
	}
}

// printActivated reports enabling a source of a type without a sensor. src is
// read before any write; an active src is reported as already active.
func printActivated(out io.Writer, src *cloud_sources.CloudSourceTiny) {
	if src.State == api.DataSourceStateActive {
		fmt.Fprintf(out, "Cloud source %d (%s) was already active; nothing was sent.\n", src.ID, terminal.Sanitize(src.Name))
	} else {
		fmt.Fprintf(out, "Cloud source %d (%s) is now active.\n", src.ID, terminal.Sanitize(src.Name))
	}
	switch src.DataSourceType {
	case api.DataSourceTypeQlusterUI:
		fmt.Fprintln(out, "It is an upload source: it accepts uploads, and it has no sensor, so nothing polls it.")
		fmt.Fprintf(out, "Upload a file with: qctl upload file <path> --table-id %d --cloud-source-id %d\n",
			src.DatasetID, src.ID)
	case "":
		fmt.Fprintln(out, "It has no source type set, so it has no sensor and nothing polls it.")
	default:
		fmt.Fprintf(out, "Its type, %s, has no sensor, so nothing polls it.\n",
			terminal.Sanitize(string(src.DataSourceType)))
	}
}
