package disable

import "github.com/spf13/cobra"

// NewCommand creates the disable command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable resources",
		Long:  `Disable resources. Valid resource types include: rule`,
	}

	cmd.AddCommand(NewRuleCommand())

	return cmd
}
