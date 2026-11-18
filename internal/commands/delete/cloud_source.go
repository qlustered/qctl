package delete

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCloudSourceCommand creates the 'delete cloud-source' command.
func NewCloudSourceCommand() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "cloud-source <id>",
		Short: "Delete a cloud source (soft)",
		Long: `Mark a cloud source deleted.

This is a SOFT delete. Recovery through 'qctl undelete cloud-source' depends on
server support; current servers reject that recovery request with 404.
It:
  - sets the source's state to "deleted", which hides it from
    'qctl get cloud-sources' and makes 'qctl describe cloud-source' answer 404
  - removes its cronjob, so nothing polls it any more
  - RESOLVES every unresolved alert on the source -- and restoring the source
    does NOT un-resolve them

Ingested rows and stored files are untouched.

The server requires the source's exact name as proof of intent. qctl reads it
and sends it for you, so you only pass an id.

A source that is already deleted is refused, because the route would answer 200
without changing anything.

Requires the same permission as deleting a table, which only the ops and admin
roles hold.

Examples:
  # delete a cloud source, with a confirmation prompt
  qctl delete cloud-source 42

  # the same, unattended
  qctl delete cloud-source 42 --yes`,
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
			// Before the prompt, not after: the DELETE route writes state =
			// deleted unconditionally, so without this a second delete would
			// answer 200 and this command would report a deletion that did not
			// happen -- on a source the user has almost certainly mis-identified,
			// since a deleted one is absent from the default listing.
			if err := cloud_sources.RequireNotDeleted(src, "deleted again"); err != nil {
				return err
			}

			if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
				fmt.Sprintf("confirmation required but stdin is not an interactive terminal -- pass --yes to delete cloud source %d (%s)",
					src.ID, src.Name),
				fmt.Sprintf("Delete cloud source %d (%s) from table %s? %d unresolved alert(s) on it will be resolved.",
					src.ID, src.Name, src.DatasetName, src.UnresolvedAlerts),
			); err != nil {
				return err
			}

			if err := client.DeleteCloudSource(token, id, src.Name); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Cloud source %d (%s) deleted.\n", src.ID, terminal.Sanitize(src.Name))
			if src.UnresolvedAlerts > 0 {
				fmt.Fprintf(out, "%d unresolved alert(s) on it were resolved; restoring the source does not un-resolve them.\n",
					src.UnresolvedAlerts)
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, "  qctl get cloud-sources --state deleted   # find it again")
			fmt.Fprintf(out, "  qctl undelete cloud-source %d            # attempt recovery; current servers reject it with 404\n", src.ID)
			return nil
		},
	}

	addDeleteFlags(cmd, &yes)
	return cmd
}
