package undelete

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewDeliverableCommand creates the undelete deliverable command.
func NewDeliverableCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "deliverable <ref>",
		Short: "Restore a deliverable from the trash",
		Long: `Restore a deliverable from the trash, keeping its revisions.
The reference is an exact slug or name, a full UUID, or a UUID prefix.
Use qctl get deliverables --archived to find a trashed deliverable.

JSON and YAML print the server's deliverable record; name prints its reference.`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := deliverables.ValidateReference(args[0]); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			token := ctx.Credential.AccessToken
			id, err := c.ResolveID(token, args[0], true)
			if err != nil {
				return err
			}
			item, err := c.Get(token, id)
			if err != nil {
				return err
			}
			if item.ArchivedAt == nil {
				return qerrors.NewBadUsagef("deliverable %s is not in the trash", id)
			}
			if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
				"confirmation required to restore deliverable; re-run with --yes",
				fmt.Sprintf("Restore deliverable %q (%s) from the trash?", terminal.Sanitize(item.Name), id)); err != nil {
				return err
			}
			result, err := c.Recover(token, id)
			if err != nil {
				return err
			}
			w := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(w.Human, "deliverable/%s restored from trash\n", id)
			return w.Write(result, "deliverable/"+id.String())
		},
	}
	addUndeleteFlags(cmd, &yes)
	return cmd
}
