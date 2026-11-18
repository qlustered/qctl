package copycmd

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the copy command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "copy",
		Short:       "Copy a resource into a new one",
		Long:        `Commands for copying a resource into a new resource of the same kind.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	cmd.AddCommand(NewTableCommand())
	cmd.AddCommand(NewCloudSourceCommand())

	return cmd
}
