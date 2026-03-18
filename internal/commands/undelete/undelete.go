package undelete

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the undelete command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "undelete",
		Short: "Undelete resources",
		Long:  `Commands for undeleting (restoring) resources such as files.`,
	}

	// Add subcommands
	cmd.AddCommand(NewFileCommand())

	return cmd
}
