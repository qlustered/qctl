package undoredo

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/history"
	"github.com/spf13/cobra"
)

// newTableCommand builds `qctl undo table` / `qctl redo table`.
//
// The selector is the standard pair plus a positional, the shape the seven
// destructive `qctl rows` verbs use: the table is the object being named, the
// positional slot is free, and cmdutil.AddTableFlags is what reaches a table
// literally named "42". Recorded selectorPositional in
// internal/catalog/table_flags_test.go.
func newTableCommand(dir history.Direction) *cobra.Command {
	word := string(dir)
	cmd := &cobra.Command{
		Use:   "table [table-id-or-name]",
		Short: shortPhrase(dir, "one table"),
		Long: `Walk your eligible change history for one table ` + directionPhrase(dir) + `.

` + callerStackNotice(dir) + `

Your eligible settings, rule, cell and row changes share this table's stack.
To reach an earlier change, first reverse your later eligible changes.

For an external user the chain holds only that user's own cell edits.

Examples:
  qctl ` + word + ` table orders
  qctl ` + word + ` table --table-id 42 --yes`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("history"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			positional := ""
			if len(args) > 0 {
				positional = args[0]
			}
			if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
			if err != nil {
				return err
			}
			label := "table " + cmdutil.TableLabel(cmd, positional, tableID)
			return runFlow(cmd, ctx, dir, history.TableTarget(tableID, label))
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	addFlags(cmd)
	return cmd
}
