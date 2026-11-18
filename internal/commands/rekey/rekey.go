package rekey

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the rekey command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "rekey",
		Short:       "Change a linked table's natural keys",
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(NewLinkedTableCommand())
	return cmd
}

func consequence(table string) string {
	return fmt.Sprintf("Changing the natural keys changes how rows of '%s' are identified. After you activate the new setup the table is rebuilt from scratch: reviews and notes attached to its rows are closed, and cell overrides do not carry over. Until you activate it, the table keeps its last refreshed data and is not refreshed.", table)
}
