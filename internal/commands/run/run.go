package run

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the run command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run resources",
		Long:  `Commands for running resources such as ingestion jobs and profiling jobs.`,
	}

	// Add subcommands
	cmd.AddCommand(NewIngestionJobCommand())
	cmd.AddCommand(NewProfilingJobCommand())

	return cmd
}
