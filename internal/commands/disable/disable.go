package disable

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the disable command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable resources",
		Long:  `Disable resources. Valid resource types include: rule`,
		RunE:  cmdutil.SubcommandRequired,
	}

	cmd.AddCommand(NewRuleCommand())

	return cmd
}
