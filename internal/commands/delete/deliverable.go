package delete

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewDeliverableCommand creates the delete deliverable command.
func NewDeliverableCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "deliverable <ref>",
		Short: "Move a deliverable to the trash",
		Long: `Move a deliverable to the trash. Restore it with qctl undelete deliverable.
The reference is an exact slug or name, a full UUID, or a UUID prefix.
Runs must all be delivered or cancelled before a deliverable can be trashed.
Revisions are kept, including the enabled revision.

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
			id, err := c.ResolveID(token, args[0], false)
			if err != nil {
				return err
			}
			item, err := c.Get(token, id)
			if err != nil {
				return err
			}
			if item.ArchivedAt != nil {
				return qerrors.NewBadUsagef("deliverable %s is already in the trash; restore it with 'qctl undelete deliverable %s'", id, id)
			}
			if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
				"confirmation required to trash deliverable; re-run with --yes",
				fmt.Sprintf("Move deliverable %q (%s) to the trash? It can be restored.", terminal.Sanitize(item.Name), id)); err != nil {
				return err
			}
			result, err := c.Archive(token, id)
			if err != nil {
				return err
			}
			w := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(w.Human, "deliverable/%s moved to trash; restore with 'qctl undelete deliverable %s'\n", id, id)
			return w.Write(result, "deliverable/"+id.String())
		},
	}
	addDeleteFlags(cmd, &yes)
	return cmd
}
