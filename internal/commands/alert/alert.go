// Package alert implements the `qctl alert` command group — the mutating
// verbs on an alert, in the shape of kubectl's `certificate approve|deny`
// and qctl's own `suggestion accept|dismiss`. Reads live under
// `qctl get alerts` and `qctl describe alert`.
package alert

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `alert` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alert",
		Short: "Act on an alert",
		Long: `Act on an alert — resolve it by choosing one of the actions it offers,
or raise and withdraw one of your own.

An alert lists what can be done to it in spec.actions. That list is
authoritative: the backend filters it by your role and by per-alert runtime
checks, so it can differ between two alerts of the same issue type and
between two users looking at the same alert.

Typical flow:
  1. qctl get alerts                              # find the alert
  2. qctl describe alert <id>                     # read spec.actions
  3. qctl alert resolve <id> --action <action>    # act on it

The most common case is a draft schema migration: a table whose
migration_policy is ask_user (the backend default) raises a
needs_review_migration alert, and resolving it with apply_migration is how
the migration is confirmed.

A human can also raise an alert directly on cells of quarantined rows, which
holds those rows until it is withdrawn:
  qctl alert submit  --table orders --row 1042 --field amount --comment "wrong FX rate"
  qctl alert retract 8817 --row 1042

'retract' is not 'resolve': it means "I flagged the wrong cell", not "this is
fixed", and it only applies to alerts a human submitted.

How schema drafts work: qctl guide inspect-schema`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newResolveCommand())
	cmd.AddCommand(newProposeCommand())
	cmd.AddCommand(newSubmitCommand())
	cmd.AddCommand(newRetractCommand())
	return cmd
}
