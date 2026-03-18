package create

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the create command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a resource",
		Long:  `Create a resource. Valid resource types include: dry-run-job`,
	}

	cmd.AddCommand(NewDryRunJobCommand())

	return cmd
}
