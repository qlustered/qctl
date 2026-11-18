package undelete

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// cloudSourceRecoverNote describes a recovery accepted by the server.
const cloudSourceRecoverNote = `NOTE: If the server accepts recovery, it is not a full undo:
        - it returns ACTIVE, whatever state it was in before the delete
        - alerts the deletion resolved stay RESOLVED
        - if it still has a schedule, polling resumes immediately`

// NewCloudSourceCommand creates the 'undelete cloud-source' command.
func NewCloudSourceCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "cloud-source <id>",
		Short: "Attempt to restore a deleted cloud source",
		Long: `Attempt to restore a cloud source that was deleted.

This uses run-sensor-cronjob, the endpoint 'enable cloud-source' uses for a type
with a sensor.
Current servers reject deleted sources with 404 (exit 3), so this recovery
path is unavailable there. Older servers may accept it.

` + cloudSourceRecoverNote + `

A deleted cloud source is invisible to 'qctl describe cloud-source', which
answers 404, and to a plain 'qctl get cloud-sources'. Find one with:

  qctl get cloud-sources --state deleted

The recovery request uses run-sensor-cronjob and needs ` + api.OperationPermissions["run_sensor_cronjob_now"] + `;
deleting needs source:delete. The sensor request also checks access to the
source's table: admin and ops pass for any table in the organization; other
permitted roles need a grant that is not read-only.
Permission to send the request does not guarantee that the server accepts it.

Examples:
  # attempt recovery on a server that supports it
  qctl undelete cloud-source 42`,
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
			if err := cloud_sources.RequireDeleted(src); err != nil {
				return err
			}

			// The call `enable` makes for a sensor type: there is no undelete route.
			if err := client.EnableCloudSource(token, id); err != nil {
				if qerrors.GetExitCode(err) == qerrors.ExitNotFound {
					return fmt.Errorf("%w\n\nCloud-source recovery through run-sensor-cronjob was refused. "+
						"Servers that reject deleted sources do not support this recovery path", err)
				}
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Cloud source %d (%s) restored, and is now active.\n", src.ID, terminal.Sanitize(src.Name))
			if schedule := cloud_sources.ScheduleOf(src); schedule != "" {
				fmt.Fprintf(out, "Polling resumes on '%s'.\n", terminal.Sanitize(schedule))
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, cloudSourceRecoverNote)
			return nil
		},
	}
}
