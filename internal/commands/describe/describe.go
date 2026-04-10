package describe

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the describe command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a specific resource",
		Long: `Show details of a specific resource in YAML format (like kubectl describe).

Valid resource types include: table, cloud-source, ingestion-job, profiling-job, alert, warning, file, destination, error-incident, rule, table-rule, dry-run-job, table-kind

The default output format is YAML for readability. You can override with -o json, -o table, etc.`,
		RunE: cmdutil.SubcommandRequired,
	}

	// Add subcommands
	cmd.AddCommand(NewDatasetCommand())
	cmd.AddCommand(NewCloudSourceCommand())
	cmd.AddCommand(NewIngestionJobCommand())
	cmd.AddCommand(NewProfilingJobCommand())
	cmd.AddCommand(NewAlertCommand())
	cmd.AddCommand(NewWarningCommand())
	cmd.AddCommand(NewFileCommand())
	cmd.AddCommand(NewDestinationCommand())
	cmd.AddCommand(NewErrorIncidentCommand())
	cmd.AddCommand(NewRuleCommand())
	cmd.AddCommand(NewTableRuleCommand())
	cmd.AddCommand(NewDryRunJobCommand())
	cmd.AddCommand(NewTableKindCommand())

	return cmd
}
