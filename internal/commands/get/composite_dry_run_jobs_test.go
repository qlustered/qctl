package get

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/composite_dry_runs"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const compositeDryRunJobsPath = "/composite-dry-runs"

// setupTestCommandForCompositeDryRunJobs builds `qctl get
// composite-dry-run-jobs` on a root carrying the real persistent flags.
func setupTestCommandForCompositeDryRunJobs() *cobra.Command {
	rootCmd := newTestRootCmd()
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewCompositeDryRunJobsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleCompositeDryRunJobTiny() composite_dry_runs.JobTiny {
	createdAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	finishedAt := time.Date(2026, 9, 20, 10, 2, 0, 0, time.UTC)
	blockers := 2
	summary := "**Join matched 40 of 50 spine rows**"

	return composite_dry_runs.JobTiny{
		ID:                      91,
		State:                   api.IngestionJobStateFinished,
		DownstreamDatasetID:     12,
		CompositeSpecGroupID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		CompositeSpecRevisionID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		MaxSpineRows:            50,
		ActivationBlockerCount:  &blockers,
		Summary:                 &summary,
		CreatedAt:               createdAt,
		FinishedAt:              &finishedAt,
	}
}

// runCompositeDryRunJobs executes the command against a mock server and hands
// the test back the query string the client actually sent, so a filter that
// never reaches the wire fails here rather than silently widening the listing.
func runCompositeDryRunJobs(t *testing.T, jobs []composite_dry_runs.JobTiny, args ...string) (string, url.Values, error) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var sent url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+compositeDryRunJobsPath, func(w http.ResponseWriter, r *http.Request) {
		sent = r.URL.Query()
		totalRows := len(jobs)
		page := 1
		testutil.RespondJSON(w, http.StatusOK, composite_dry_runs.JobsPage{
			Results:   jobs,
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	cmd := setupTestCommandForCompositeDryRunJobs()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"get", "composite-dry-run-jobs"}, args...))

	err := cmd.Execute()
	return buf.String(), sent, err
}

func TestGetCompositeDryRunJobsCommand(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		jobs         []composite_dry_runs.JobTiny
		wantContains []string
	}{
		{
			name:         "default table output",
			jobs:         []composite_dry_runs.JobTiny{sampleCompositeDryRunJobTiny()},
			wantContains: []string{"91", "finished", "22222222-2222-2222-2222-222222222222"},
		},
		{
			name:         "json output",
			args:         []string{"--output", "json"},
			jobs:         []composite_dry_runs.JobTiny{sampleCompositeDryRunJobTiny()},
			wantContains: []string{`"downstream_dataset_id": 12`, `"activation_blocker_count": 2`},
		},
		{
			name:         "yaml output",
			args:         []string{"--output", "yaml"},
			jobs:         []composite_dry_runs.JobTiny{sampleCompositeDryRunJobTiny()},
			wantContains: []string{"max_spine_rows: 50", "state: finished"},
		},
		{
			name:         "name output falls back to the id",
			args:         []string{"--output", "name"},
			jobs:         []composite_dry_runs.JobTiny{sampleCompositeDryRunJobTiny()},
			wantContains: []string{"91"},
		},
		{
			name: "empty result",
			jobs: []composite_dry_runs.JobTiny{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := runCompositeDryRunJobs(t, tt.jobs, tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("output should contain %q, got:\n%s", want, out)
				}
			}
		})
	}
}

// TestGetCompositeDryRunJobsSendsItsFilters is the guard a table-output
// assertion cannot be: a filter the client drops still prints the mocked page,
// so only reading the query string proves the filter left the process. --table
// names the downstream table, so it must arrive as downstream_dataset_id.
func TestGetCompositeDryRunJobsSendsItsFilters(t *testing.T) {
	jobs := []composite_dry_runs.JobTiny{sampleCompositeDryRunJobTiny()}
	groupID := "550e8400-e29b-41d4-a716-446655440000"
	revisionID := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

	_, sent, err := runCompositeDryRunJobs(t, jobs,
		"--state", "error", "--state", "stopped",
		"--table-id", "12",
		"--group-id", groupID,
		"--revision-id", revisionID,
		"--order-by", "snapshot_at",
		"--page", "2",
		"--limit", "7",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := sent["state"]; len(got) != 2 || got[0] != "error" || got[1] != "stopped" {
		t.Errorf("state = %v, want [error stopped] (--state must stay repeatable)", got)
	}
	for param, want := range map[string]string{
		"downstream_dataset_id": "12",
		"group_id":              groupID,
		"revision_id":           revisionID,
		"order_by":              "snapshot_at",
		"page":                  "2",
		"limit":                 "7",
	} {
		if got := sent.Get(param); got != want {
			t.Errorf("%s = %q, want %q", param, got, want)
		}
	}
}

// TestGetCompositeDryRunJobsReverseFlipsTheServerDefault pins the inversion
// documented in internal/rule_versions/client.go: the route declares
// reverse=True (list_composite_dry_runs, composite_dry_run_view.py), so an
// unflagged run must send no reverse at all and --reverse must send
// reverse=false.
func TestGetCompositeDryRunJobsReverseFlipsTheServerDefault(t *testing.T) {
	jobs := []composite_dry_runs.JobTiny{sampleCompositeDryRunJobTiny()}

	_, sent, err := runCompositeDryRunJobs(t, jobs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := sent["reverse"]; ok {
		t.Errorf("reverse=%q was sent without --reverse; the server default must be left alone", sent.Get("reverse"))
	}

	_, sent, err = runCompositeDryRunJobs(t, jobs, "--reverse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := sent.Get("reverse"); got != "false" {
		t.Errorf("reverse = %q with --reverse, want \"false\"", got)
	}
}

// TestGetCompositeDryRunJobsClientSideValidationExitsTwo asserts the code, not
// the message: a substring assertion passes even when the error has no Unwrap
// chain and the exit code has silently degraded to 1.
func TestGetCompositeDryRunJobsClientSideValidationExitsTwo(t *testing.T) {
	cases := map[string][]string{
		"unknown order-by":       {"--order-by", "finished_at"},
		"unknown state":          {"--state", "finshed"},
		"malformed group uuid":   {"--group-id", "not-a-uuid"},
		"malformed revision uid": {"--revision-id", "not-a-uuid"},
		"zero page":              {"--page", "0"},
		"negative limit":         {"--limit", "-1"},
		"table twice":            {"--table", "orders", "--table-id", "5"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			// No handler is registered for the request these must never send:
			// the mock 404s anything that escapes validation, which would show
			// up as exit 3 rather than exit 2.
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupTestCommandForCompositeDryRunJobs()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(append([]string{"get", "composite-dry-run-jobs"}, args...))

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected an error for %v", args)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage); err = %v", got, qerrors.ExitBadUsage, err)
			}
		})
	}
}

// TestGetCompositeDryRunJobsServerErrors: the list route is org-scoped, so a
// caller in the wrong organization gets 403 and a bad token 401 -- both exit 4.
func TestGetCompositeDryRunJobsServerErrors(t *testing.T) {
	cases := map[string]struct {
		status   int
		wantExit int
	}{
		"unauthorized": {http.StatusUnauthorized, qerrors.ExitUnauthorized},
		"forbidden":    {http.StatusForbidden, qerrors.ExitUnauthorized},
		"server error": {http.StatusInternalServerError, qerrors.ExitGenericError},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+compositeDryRunJobsPath,
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondError(w, tc.status, "nope")
				})

			cmd := setupTestCommandForCompositeDryRunJobs()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{"get", "composite-dry-run-jobs"})

			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := qerrors.GetExitCode(err); got != tc.wantExit {
				t.Errorf("exit code = %d, want %d; err = %v", got, tc.wantExit, err)
			}
		})
	}
}

// TestGetCompositeDryRunJobsAdvertisedColumnsAreReal checks the hand-written
// --columns help and default column set against the type the printer
// reflects, in both directions.
func TestGetCompositeDryRunJobsAdvertisedColumnsAreReal(t *testing.T) {
	available := output.AvailableColumns([]composite_dry_runs.JobTiny{}, nil)
	if len(available) == 0 {
		t.Fatal("AvailableColumns returned nothing; the reflection is broken, not the list")
	}

	cmd := NewCompositeDryRunJobsCommand()
	usage := cmd.Flags().Lookup("columns").Usage
	_, advertised, ok := strings.Cut(usage, "Available: ")
	if !ok {
		t.Fatalf("--columns usage no longer carries an \"Available: \" list: %q", usage)
	}

	advertisedNames := map[string]bool{}
	for _, c := range strings.Split(advertised, ",") {
		advertisedNames[strings.TrimSpace(c)] = true
	}
	for _, list := range map[string]string{
		"--columns help":  advertised,
		"default columns": compositeDryRunJobColumns,
	} {
		var names []string
		for _, c := range strings.Split(list, ",") {
			names = append(names, strings.TrimSpace(c))
		}
		if err := output.ValidateColumns(names, available); err != nil {
			t.Errorf("%v", err)
		}
	}

	// The other direction: a field the type gained and the help never
	// mentioned. Matched as a whole name, since "id" is a substring of every
	// other *_id column.
	for _, col := range available {
		if !advertisedNames[col] {
			t.Errorf("column %q exists on JobTiny but --columns help does not list it", col)
		}
	}
}
