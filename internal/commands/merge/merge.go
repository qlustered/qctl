// Package merge implements the qctl merge command group.
package merge

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the merge command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "merge",
		Short:       "Merge one table's configuration into another table",
		Long:        `Commands for merging part of one table's configuration into another table.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	cmd.AddCommand(NewTableRulesCommand())

	return cmd
}
