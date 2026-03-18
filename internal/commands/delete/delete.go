package delete

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the delete command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete resources",
		Long:  `Commands for deleting resources such as files, error incidents, and rules.`,
	}

	// Add subcommands
	cmd.AddCommand(NewFileCommand())
	cmd.AddCommand(NewErrorIncidentCommand())
	cmd.AddCommand(NewRuleCommand())
	cmd.AddCommand(NewRulesCommand())

	return cmd
}
