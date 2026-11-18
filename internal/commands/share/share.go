package share

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the share command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "share",
		Short:       "Grant another user access to a resource",
		Long:        `Commands for granting another user in your organization access to a resource.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	cmd.AddCommand(NewTableCommand())

	return cmd
}
