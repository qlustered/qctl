package undoredo

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/history"
	"github.com/spf13/cobra"
)

// newDestinationCommand builds `qctl undo destination` / `qctl redo destination`.
//
// A numeric id only, for the same reason as cloud-source: destinations have no
// name resolution anywhere in qctl today.
//
// Note that this one is stricter than its permission row suggests. Before
// check_user_can_undo_redo_history_items runs, the view itself refuses any
// caller outside {ops, admin} whose body carries a destination_id:
//
//	if body.destination_id is not None and current_user.role not in {Role.ops, Role.admin}:
//	    raise APIError(403, "User is not authorized to undo/redo destination changes")
//
// (atlas _check_undo_redo_entity_access, capi/capi/views/history_view.py) -- so
// a role that holds destination:create but is neither ops nor admin is a 403
// from the route, not from the per-model check.
func newDestinationCommand(dir history.Direction) *cobra.Command {
	word := string(dir)
	cmd := &cobra.Command{
		Use:   "destination <id>",
		Short: shortPhrase(dir, "one destination"),
		Long: `Walk your eligible change history for one destination ` + directionPhrase(dir) + `.

` + callerStackNotice(dir) + `

Ids come from 'qctl get destinations'. Only the ops and admin roles can undo a
destination change at all.

Examples:
  qctl ` + word + ` destination 9
  qctl ` + word + ` destination 9 --yes`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("history"), "table", "json", "yaml"), "destination"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := cmdutil.ParseIntArgs(args, "destination id")
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			return runFlow(cmd, ctx, dir, history.DestinationTarget(ids[0]))
		},
	}
	addFlags(cmd)
	return cmd
}
