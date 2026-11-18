package describe

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

const (
	outputFormatYAML  = "yaml"
	outputFormatJSON  = "json"
	outputFormatTable = "table"
)

// NewCommand creates the describe command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a specific resource",
		Long: `Show details of a specific resource (like kubectl describe).

Run 'qctl api-resources' for every resource, its aliases, and the verbs that act
on it — that index is computed from this command tree, so it cannot go stale.

The commands below use canonical names; for example, table-schema (aka migration).

Most of these print a YAML manifest by default; several print a purpose-built
text view instead. Override with -o table, -o json or -o yaml; -o name is
rejected with exit 2 except on table-series, which can print its bare reference.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	// Add subcommands
	cmd.AddCommand(NewDatasetCommand())
	cmd.AddCommand(NewCloudSourceCommand())
	cmd.AddCommand(NewCommentThreadCommand())
	cmd.AddCommand(NewDuplicateGroupCommand())
	cmd.AddCommand(NewDataReviewCommand())
	cmd.AddCommand(NewIngestionJobCommand())
	cmd.AddCommand(NewProfilingJobCommand())
	cmd.AddCommand(NewQueueMessageCommand())
	cmd.AddCommand(NewPrivilegeRequestCommand())
	cmd.AddCommand(NewChangeProposalCommand())
	cmd.AddCommand(NewCompositeRunJobCommand())
	cmd.AddCommand(NewCompositeDryRunJobCommand())
	cmd.AddCommand(NewAlertCommand())
	cmd.AddCommand(NewWarningCommand())
	cmd.AddCommand(NewFileCommand())
	cmd.AddCommand(NewDestinationCommand())
	cmd.AddCommand(NewBackupSettingsCommand())
	cmd.AddCommand(NewErrorIncidentCommand())
	cmd.AddCommand(NewRuleCommand())
	cmd.AddCommand(NewRuleReviewCommand())
	cmd.AddCommand(NewRowCommand())
	cmd.AddCommand(NewRuleSuggestionCommand())
	cmd.AddCommand(NewRuleRunReplayCommand())
	cmd.AddCommand(NewTableRuleCommand())
	cmd.AddCommand(NewRuleMergeCommand())
	cmd.AddCommand(NewTableSchemaCommand())
	cmd.AddCommand(NewDryRunJobCommand())
	cmd.AddCommand(NewTableKindCommand())
	cmd.AddCommand(NewTableSeriesCommand())
	cmd.AddCommand(NewDeliverableCommand())
	cmd.AddCommand(NewDeliverableRevisionCommand())
	cmd.AddCommand(NewDeliverableRunCommand())
	cmd.AddCommand(NewStandardFieldCommand())

	return cmd
}
