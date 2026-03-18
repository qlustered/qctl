package kill

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the kill command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kill",
		Short: "Kill resources",
		Long:  `Commands for killing/stopping resources such as ingestion jobs and profiling jobs.`,
	}

	// Add subcommands
	cmd.AddCommand(NewIngestionJobCommand())
	cmd.AddCommand(NewProfilingJobCommand())

	return cmd
}
