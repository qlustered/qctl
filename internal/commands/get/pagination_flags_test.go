package get

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// paginatedCommand is one row of the sweep: a `get` subcommand that registers
// --page and --limit and validates them by calling paginationFlags. The
// listing clients apply each parameter only when it is positive, so a dropped
// --page 0 used to come back as the backend's default page at exit 0.
//
// This table is maintained by hand: a new `get` command that calls
// paginationFlags has to be added here as a row, or its --page/--limit go
// unvalidated by this sweep. `get data` also registers --page/--limit but is
// deliberately not a paginationFlags caller and not a row here — it validates
// them separately (validateDataPagination in data_flags.go), for the reason
// documented on paginationFlags in pagination.go.
type paginatedCommand struct {
	name  string
	build func() *cobra.Command
	// args are the positionals the command requires.
	args []string
}

func paginatedCommands() []paginatedCommand {
	return []paginatedCommand{
		{name: "tables", build: NewDatasetsCommand},
		{name: "tables-overview", build: NewTablesOverviewCommand},
		{name: "alerts", build: NewAlertsCommand},
		{name: "warnings", build: NewWarningsCommand},
		{name: "history", build: NewHistoryCommand},
		{name: "orgs", build: NewOrgsCommand},
		{name: "table-kinds", build: NewTableKindsCommand},
		{name: "standard-fields", build: NewStandardFieldsCommand},
		{name: "deliverables", build: NewDeliverablesCommand},
		{name: "deliverable-runs", build: NewDeliverableRunsCommand, args: []string{"monthly-bordereau"}},
		{name: "profiling-jobs", build: NewProfilingJobsCommand},
		{name: "ingestion-jobs", build: NewIngestionJobsCommand},
		{name: "composite-run-jobs", build: NewCompositeRunJobsCommand},
		{name: "composite-dry-run-jobs", build: NewCompositeDryRunJobsCommand},
		{name: "backup-settings", build: NewBackupSettingsCommand},
		{name: "destinations", build: NewDestinationsCommand},
		{name: "error-incidents", build: NewErrorIncidentsCommand},
		{name: "rules", build: NewRuleFamiliesCommand},
		{name: "rule-revisions", build: NewRuleRevisionsCommand},
		{name: "rule-reviews", build: NewRuleReviewsCommand},
		{name: "rule-suggestions", build: NewRuleSuggestionsCommand},
		{name: "table-rules", build: NewTableRulesCommand},
		{name: "notifications", build: NewNotificationsCommand},
		{name: "comment-threads", build: NewCommentThreadsCommand},
		{name: "pii-access-requests", build: NewPiiAccessRequestsCommand, args: []string{"42"}},
		{name: "privilege-requests", build: NewPrivilegeRequestsCommand},
		{name: "privilege-audit", build: NewPrivilegeAuditCommand},
		{name: "change-proposals", build: NewChangeProposalsCommand},
		{name: "data-reviews", build: NewDataReviewsCommand},
		{name: "dry-run-jobs", build: NewDryRunJobsCommand},
		{name: "deleted-rows", build: NewDeletedRowsCommand, args: []string{"42"}},
		{name: "corrected-lines", build: NewCorrectedLinesCommand, args: []string{"311"}},
		{name: "dropped-lines", build: NewDroppedLinesCommand, args: []string{"311"}},
	}
}

// runPaginationFlag executes `qctl get <name> <args> --<flag> <value>` against
// a mock server with NO handlers registered. Any request that escapes
// validation gets a 404 and surfaces as a not-found error (exit 3), so
// asserting exit 2 is also an assertion that nothing was sent.
func runPaginationFlag(t *testing.T, c paginatedCommand, flag, value string) error {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := newTestRootCmd()

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(c.build())
	rootCmd.AddCommand(getCmd)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append(append([]string{"get", c.name}, c.args...), "--"+flag, value))

	return rootCmd.Execute()
}

func TestPaginationFlagsRejectNonPositive(t *testing.T) {
	for _, c := range paginatedCommands() {
		for _, flag := range []string{"page", "limit"} {
			for _, value := range []string{"0", "-3"} {
				t.Run(c.name+"/"+flag+"/"+value, func(t *testing.T) {
					err := runPaginationFlag(t, c, flag, value)
					if err == nil {
						t.Fatalf("get %s --%s %s: expected an error", c.name, flag, value)
					}
					want := "--" + flag + " must be a positive integer, got " + value
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error should contain %q, got: %v", want, err)
					}
					if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
						t.Errorf("exit code = %d, want %d (a request that reached the mock would 404 to %d)",
							code, qerrors.ExitBadUsage, qerrors.ExitNotFound)
					}
				})
			}
		}
	}
}
