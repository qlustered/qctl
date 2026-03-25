package get

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the get command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Display one or many resources",
		Long:  `Display one or many resources. Valid resource types include: table, tables, cloud-sources, ingestion-jobs, profiling-jobs, alerts, warnings, files, destinations, error-incidents, rule, rules, rule-revisions, table-rule, table-rules, dry-run-job, dry-run-jobs`,
		RunE:  cmdutil.SubcommandRequired,
	}

	// Add subcommands
	cmd.AddCommand(NewDatasetCommand())
	cmd.AddCommand(NewDatasetsCommand())
	cmd.AddCommand(NewCloudSourcesCommand())
	cmd.AddCommand(NewIngestionJobsCommand())
	cmd.AddCommand(NewProfilingJobsCommand())
	cmd.AddCommand(NewAlertsCommand())
	cmd.AddCommand(NewWarningsCommand())
	cmd.AddCommand(NewFilesCommand())
	cmd.AddCommand(NewDestinationsCommand())
	cmd.AddCommand(NewErrorIncidentsCommand())
	cmd.AddCommand(NewRuleCommand())
	cmd.AddCommand(NewRuleFamiliesCommand())
	cmd.AddCommand(NewRuleRevisionsCommand())
	cmd.AddCommand(NewTableRuleCommand())
	cmd.AddCommand(NewTableRulesCommand())
	cmd.AddCommand(NewDryRunJobCommand())
	cmd.AddCommand(NewDryRunJobsCommand())

	return cmd
}
