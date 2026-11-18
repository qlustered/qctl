// Package table_rule implements actions on rules attached to tables.
package table_rule //nolint:revive // Matches the command directory.

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `table-rule` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-rule",
		Short: "Act on a rule attached to a table",
		Long: `Act on a rule attached to a table.

See the table's rules in the order they run:
  qctl get table-rules <table> --order-by position

Then change that order:
  qctl table-rule move <rule> --table <table> --position 1

Count the rows a rule's row filter keeps:
  qctl table-rule test-filter <rule> --table <table>

Propose a change to its params for an approver to apply:
  qctl table-rule propose <rule> --table <table> --set-param <key>=<value>

A rule's configuration and state change through a manifest instead:
  qctl apply -f table-rule.yaml`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newMoveCommand(), newRecheckCommand(), newTestFilterCommand(), newProposeCommand())
	return cmd
}
