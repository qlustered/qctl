package run

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the run command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "run",
		Short:       "Run resources",
		Long:        `Commands for running resources such as ingestion jobs, profiling jobs, cloud-source sensors, rule-run replays and linked-table rebuilds.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	// Add subcommands
	cmd.AddCommand(NewIngestionJobCommand())
	cmd.AddCommand(NewProfilingJobCommand())
	cmd.AddCommand(NewSensorJobCommand())
	cmd.AddCommand(NewRuleRunReplayCommand())
	cmd.AddCommand(NewCompositeRunJobCommand())

	return cmd
}
