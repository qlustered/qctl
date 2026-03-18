package enable

import "github.com/spf13/cobra"

// NewCommand creates the enable command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable resources",
		Long:  `Enable resources. Valid resource types include: rule`,
	}

	cmd.AddCommand(NewRuleCommand())

	return cmd
}
