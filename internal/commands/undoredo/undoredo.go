// Package undoredo holds `qctl undo` and `qctl redo`.
//
// One package for two verbs, because they are one implementation: the routes,
// the request bodies, the confirmation and the output differ only by a
// direction word. No command package imports another (only tests do), so a
// directory per verb would mean a second copy of the runner.
//
// Named for the verbs rather than for the `history` resource they act on,
// because every file here also imports internal/history — and a package named
// history importing a package named history compiles but reads as a mistake on
// every line.
package undoredo

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/history"
	"github.com/spf13/cobra"
)

// NewUndoCommand creates the `qctl undo` verb.
func NewUndoCommand() *cobra.Command { return newVerb(history.Undo) }

// NewRedoCommand creates the `qctl redo` verb.
func NewRedoCommand() *cobra.Command { return newVerb(history.Redo) }

func newVerb(dir history.Direction) *cobra.Command {
	word := string(dir)
	cmd := &cobra.Command{
		Use:   word,
		Short: shortPhrase(dir, "one object"),
		Long: `Walk your eligible change history for one object ` + directionPhrase(dir) + `.

The scope is a single object -- one table, one cloud source, one destination --
never the organization. Which object is the argument; which KIND of object is
the subcommand.

` + callerStackNotice(dir) + `

qctl always asks the backend to step the history first, and prints whatever
description it gets back before it acts. The step call is not an opt-in
preview and there is no --dry-run: qctl sends the previewed ids back so the
backend refuses the ` + word + ` if the selected history ids have changed.

Examples:
  qctl ` + word + ` table orders
  qctl ` + word + ` cloud-source 4 --yes
  qctl ` + word + ` destination 9`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(
		newTableCommand(dir),
		newCloudSourceCommand(dir),
		newDestinationCommand(dir),
	)
	return cmd
}

func shortPhrase(dir history.Direction, object string) string {
	if dir == history.Redo {
		return "Re-apply your next eligible undone change to " + object
	}
	return "Reverse your most recent eligible change to " + object
}

func directionPhrase(dir history.Direction) string {
	if dir == history.Redo {
		return "forwards, re-applying your next eligible undone change"
	}
	return "backwards, reversing your most recent eligible change"
}

func callerStackNotice(dir history.Direction) string {
	if dir == history.Redo {
		return `This redoes only your own eligible undone changes, whatever your role.
Read the preview before confirming.`
	}
	return `This undoes only your own eligible changes, whatever your role.
Read the preview before confirming.`
}
