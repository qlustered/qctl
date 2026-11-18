package get

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the get command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Display one or many resources",
		Long: `Display one or many resources.

Run 'qctl api-resources' for every resource, its aliases, and the verbs that act
on it — that index is computed from this command tree, so it cannot go stale.

The commands below use canonical names; for example, table-schema (aka migration).`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	// Add subcommands
	cmd.AddCommand(NewDatasetCommand())
	cmd.AddCommand(NewDataCommand())
	cmd.AddCommand(NewValueRecommendationsCommand())
	cmd.AddCommand(NewRuleIgnoresCommand())
	cmd.AddCommand(NewDeletedRowsCommand())
	cmd.AddCommand(NewDuplicateGroupsCommand())
	cmd.AddCommand(NewDroppedLinesCommand())
	cmd.AddCommand(NewCorrectedLinesCommand())
	cmd.AddCommand(NewDatasetsCommand())
	cmd.AddCommand(NewTablesOverviewCommand())
	cmd.AddCommand(NewSoftDeletedTablesCommand())
	cmd.AddCommand(NewCloudSourcesCommand())
	cmd.AddCommand(NewCloudSourceCommand())
	cmd.AddCommand(NewCommentThreadsCommand())
	cmd.AddCommand(NewIngestionJobsCommand())
	cmd.AddCommand(NewProfilingJobsCommand())
	cmd.AddCommand(NewQueueMessagesCommand())
	cmd.AddCommand(NewPrivilegeRequestsCommand())
	cmd.AddCommand(NewPiiGrantsCommand())
	cmd.AddCommand(NewPiiAccessRequestsCommand())
	cmd.AddCommand(NewPrivilegeAuditCommand())
	cmd.AddCommand(NewChangeProposalsCommand())
	cmd.AddCommand(NewCompositeRunJobsCommand())
	cmd.AddCommand(NewCompositeDryRunJobsCommand())
	cmd.AddCommand(NewAlertsCommand())
	cmd.AddCommand(NewLatestAlertCommand())
	cmd.AddCommand(NewNotificationsCommand())
	cmd.AddCommand(NewNotificationPreferencesCommand())
	cmd.AddCommand(NewSlackConfigCommand())
	cmd.AddCommand(NewWarningsCommand())
	cmd.AddCommand(NewHistoryCommand())
	cmd.AddCommand(NewFilesCommand())
	cmd.AddCommand(NewDestinationsCommand())
	cmd.AddCommand(NewBackupSettingsCommand())
	cmd.AddCommand(NewErrorIncidentsCommand())
	cmd.AddCommand(NewRuleCommand())
	cmd.AddCommand(NewRuleFamiliesCommand())
	cmd.AddCommand(NewRuleRevisionsCommand())
	cmd.AddCommand(NewRuleReviewsCommand())
	cmd.AddCommand(NewRuleSuggestionsCommand())
	cmd.AddCommand(NewRuleUpgradesCommand())
	cmd.AddCommand(NewTableRuleCommand())
	cmd.AddCommand(NewTableRulesCommand())
	cmd.AddCommand(NewTableSchemaCommand())
	cmd.AddCommand(NewTableConfigCommand())
	cmd.AddCommand(NewDryRunJobCommand())
	cmd.AddCommand(NewDryRunJobsCommand())
	cmd.AddCommand(NewTableKindsCommand())
	cmd.AddCommand(NewTableSeriesCommand())
	cmd.AddCommand(NewTableSeriesSuggestionCommand())
	cmd.AddCommand(NewSubmissionStatusesCommand())
	cmd.AddCommand(NewTableMembersCommand())
	cmd.AddCommand(NewEmailSendersCommand())
	cmd.AddCommand(NewDeliverablesCommand())
	cmd.AddCommand(NewDeliverableCommand())
	cmd.AddCommand(NewDeliverableRevisionsCommand())
	cmd.AddCommand(NewDeliverableRunsCommand())
	cmd.AddCommand(NewDeliverableRunCommand())
	cmd.AddCommand(NewTableKindGetCommand())
	cmd.AddCommand(NewStandardFieldsCommand())
	cmd.AddCommand(NewStandardFieldCommand())
	cmd.AddCommand(NewStandardFieldSuggestionsCommand())
	cmd.AddCommand(NewTableKindSuggestionsCommand())
	cmd.AddCommand(NewOrgsCommand())
	cmd.AddCommand(NewRuleReviewPolicyCommand())
	cmd.AddCommand(NewDataReviewPolicyCommand())
	cmd.AddCommand(NewChangeReviewPolicyCommand())
	cmd.AddCommand(NewLinkedTableCommand())
	cmd.AddCommand(NewDataReviewsCommand())
	cmd.AddCommand(NewDataReviewGroupsCommand())
	cmd.AddCommand(NewDataReviewChangesCommand())

	return cmd
}
