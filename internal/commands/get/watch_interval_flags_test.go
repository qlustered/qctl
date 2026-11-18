package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// watchIntervalCommand is one `get` subcommand that accepts
// --watch/--watch-interval, for the table-driven sweep below. Only what
// actually differs between commands lives here: the rest of the test setup
// (env, mock server, config, credential) is identical across all of them.
type watchIntervalCommand struct {
	name     string // as typed on the CLI, e.g. "profiling-jobs"
	build    func() *cobra.Command
	endpoint string // the listing request path this command's client hits
}

// watchIntervalCommands lists the two `get` subcommands whose --watch-interval
// guard shares this test's setup (env, mock server, config, credential). It is
// not the full set of commands that guard a poll interval: `get latest-alert`
// also registers --watch-interval (latest_alert.go) and is covered by
// TestLatestAlertRejectsSubSecondInterval in latest_alert_test.go, and
// `get dry-run-jobs` spells the same guard --interval (dry_run_jobs.go) and is
// covered by TestGetDryRunJobsRejectsNonPositiveInterval in
// dry_run_jobs_test.go — both were left out of this table because their setup
// differs enough that a shared row would not fit. Add a row here for a new
// command whose setup does fit; give one that doesn't its own test instead.
func watchIntervalCommands() []watchIntervalCommand {
	return []watchIntervalCommand{
		{
			name:     "profiling-jobs",
			build:    setupTestCommandForProfiling,
			endpoint: "/api/orgs/" + testOrgID + "/training-jobs",
		},
		{
			name:     "ingestion-jobs",
			build:    setupTestCommandForIngestion,
			endpoint: "/api/orgs/" + testOrgID + "/ingestion-jobs",
		},
	}
}

// runWatchIntervalFlag runs `get <tc.name> <args...>` against a fresh
// env/mock/config/credential setup, and reports the command's error plus how
// many times its listing endpoint was hit.
func runWatchIntervalFlag(t *testing.T, tc watchIntervalCommand, args []string) (calls int, err error) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", tc.endpoint, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := tc.build()
	cmd.SetArgs(append([]string{"get", tc.name}, args...))

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err = cmd.Execute()
	return calls, err
}

// --watch-interval 0 used to reach time.NewTicker and take the whole CLI down
// with a Go stack trace, before any request was made. Every command that
// takes --watch-interval must reject a non-positive value before making any
// request, whether or not --watch was also passed.
func TestGetJobsRejectsNonPositiveWatchInterval(t *testing.T) {
	argSets := [][]string{
		{"--watch", "--watch-interval", "0"},
		{"--watch", "--watch-interval", "-5"},
		{"--watch-interval", "0"}, // refused without --watch too: never silently ignored
	}

	for _, tc := range watchIntervalCommands() {
		for _, args := range argSets {
			t.Run(tc.name+" "+strings.Join(args, " "), func(t *testing.T) {
				calls, err := runWatchIntervalFlag(t, tc, args)

				if err == nil {
					t.Fatalf("expected an error for %s %v", tc.name, args)
				}
				if !strings.Contains(err.Error(), "--watch-interval must be at least 1 second") {
					t.Errorf("error should name the flag and the rule, got: %v", err)
				}
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
				}
				if calls != 0 {
					t.Errorf("expected %s never to be called, got %d call(s)", tc.endpoint, calls)
				}
			})
		}
	}
}
