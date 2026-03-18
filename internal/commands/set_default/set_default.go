package set_default

import "github.com/spf13/cobra"

// NewCommand creates the set-default command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-default",
		Short: "Set default resources",
		Long:  `Set default resources. Valid resource types include: rule`,
	}

	cmd.AddCommand(NewRuleCommand())

	return cmd
}
