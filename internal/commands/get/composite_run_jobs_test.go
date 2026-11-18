package get

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/composite_run_jobs"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const compositeRunJobsPath = "/composite-run-jobs"

// setupTestCommandForCompositeRunJobs builds `qctl get composite-run-jobs` on a
// root carrying the real persistent flags.
func setupTestCommandForCompositeRunJobs() *cobra.Command {
	rootCmd := newTestRootCmd()
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewCompositeRunJobsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleCompositeRunJobTiny() composite_run_jobs.CompositeRunJobTiny {
	createdAt := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 9, 10, 10, 5, 0, 0, time.UTC)
	finishedAt := time.Date(2026, 9, 10, 10, 6, 0, 0, time.UTC)
	changed := 5

	return composite_run_jobs.CompositeRunJobTiny{
		ID:                   77,
		State:                api.IngestionJobStateFinished,
		Reason:               "upstream_ingestion_finished",
		ChangedDatasetID:     &changed,
		AffectedTargetCount:  3,
		CompletedTargetCount: 3,
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
		FinishedAt:           &finishedAt,
	}
}

// runCompositeRunJobs executes the command against a mock server and hands the
// test back the query string the client actually sent, so a filter that never
// reaches the wire fails here rather than silently widening the listing.
func runCompositeRunJobs(t *testing.T, jobs []composite_run_jobs.CompositeRunJobTiny, args ...string) (string, url.Values, error) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var sent url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+compositeRunJobsPath, func(w http.ResponseWriter, r *http.Request) {
		sent = r.URL.Query()
		totalRows := len(jobs)
		page := 1
		testutil.RespondJSON(w, http.StatusOK, composite_run_jobs.CompositeRunJobsPage{
			Results:   jobs,
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	cmd := setupTestCommandForCompositeRunJobs()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"get", "composite-run-jobs"}, args...))

	err := cmd.Execute()
	return buf.String(), sent, err
}

func TestGetCompositeRunJobsCommand(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		jobs         []composite_run_jobs.CompositeRunJobTiny
		wantContains []string
	}{
		{
			name:         "default table output",
			jobs:         []composite_run_jobs.CompositeRunJobTiny{sampleCompositeRunJobTiny()},
			wantContains: []string{"77", "finished", "upstream_ingestion_finished"},
		},
		{
			name:         "json output",
			args:         []string{"--output", "json"},
			jobs:         []composite_run_jobs.CompositeRunJobTiny{sampleCompositeRunJobTiny()},
			wantContains: []string{`"reason": "upstream_ingestion_finished"`, `"state": "finished"`},
		},
		{
			name:         "yaml output",
			args:         []string{"--output", "yaml"},
			jobs:         []composite_run_jobs.CompositeRunJobTiny{sampleCompositeRunJobTiny()},
			wantContains: []string{"reason: upstream_ingestion_finished", "state: finished"},
		},
		{
			name:         "name output falls back to the id",
			args:         []string{"--output", "name"},
			jobs:         []composite_run_jobs.CompositeRunJobTiny{sampleCompositeRunJobTiny()},
			wantContains: []string{"77"},
		},
		{
			name: "empty result",
			jobs: []composite_run_jobs.CompositeRunJobTiny{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := runCompositeRunJobs(t, tt.jobs, tt.args...)
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

// TestGetCompositeRunJobsSendsItsFilters is the guard a table-output assertion
// cannot be: a filter the client drops still prints the mocked page, so only
// reading the query string proves the filter left the process.
func TestGetCompositeRunJobsSendsItsFilters(t *testing.T) {
	jobs := []composite_run_jobs.CompositeRunJobTiny{sampleCompositeRunJobTiny()}
	groupID := "550e8400-e29b-41d4-a716-446655440000"

	_, sent, err := runCompositeRunJobs(t, jobs,
		"--state", "error", "--state", "killed",
		"--table-id", "5",
		"--group-id", groupID,
		"--reason", "revision_enabled",
		"--order-by", "created_at",
		"--page", "2",
		"--limit", "7",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := sent["state"]; len(got) != 2 || got[0] != "error" || got[1] != "killed" {
		t.Errorf("state = %v, want [error killed] (--state must stay repeatable)", got)
	}
	for param, want := range map[string]string{
		"changed_dataset_id": "5",
		"group_id":           groupID,
		"reason":             "revision_enabled",
		"order_by":           "created_at",
		"page":               "2",
		"limit":              "7",
	} {
		if got := sent.Get(param); got != want {
			t.Errorf("%s = %q, want %q", param, got, want)
		}
	}
}

// TestGetCompositeRunJobsReverseFlipsTheServerDefault pins the inversion
// documented in internal/rule_versions/client.go: the route declares
// reverse=True (get_composite_run_jobs, composite_run_job_view.py), so an
// unflagged run must send no reverse at all and --reverse must send
// reverse=false.
func TestGetCompositeRunJobsReverseFlipsTheServerDefault(t *testing.T) {
	jobs := []composite_run_jobs.CompositeRunJobTiny{sampleCompositeRunJobTiny()}

	_, sent, err := runCompositeRunJobs(t, jobs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := sent["reverse"]; ok {
		t.Errorf("reverse=%q was sent without --reverse; the server default must be left alone", sent.Get("reverse"))
	}

	_, sent, err = runCompositeRunJobs(t, jobs, "--reverse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := sent.Get("reverse"); got != "false" {
		t.Errorf("reverse = %q with --reverse, want \"false\"", got)
	}
}

// TestGetCompositeRunJobsClientSideValidationExitsTwo asserts the code, not the
// message: a substring assertion passes even when the error has no Unwrap chain
// and the exit code has silently degraded to 1.
func TestGetCompositeRunJobsClientSideValidationExitsTwo(t *testing.T) {
	cases := map[string][]string{
		"unknown order-by": {"--order-by", "bogus_field"},
		"unknown state":    {"--state", "finshed"},
		"malformed uuid":   {"--group-id", "not-a-uuid"},
		"zero page":        {"--page", "0"},
		"negative limit":   {"--limit", "-1"},
		"table twice":      {"--table", "orders", "--table-id", "5"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			// No handler is registered for the request these must never send:
			// the mock 404s anything that escapes validation, which would show
			// up as exit 3 rather than exit 2.
			_, _, err := runCompositeRunJobsNoHandler(t, args...)
			if err == nil {
				t.Fatalf("expected an error for %v", args)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage); err = %v", got, qerrors.ExitBadUsage, err)
			}
		})
	}
}

// runCompositeRunJobsNoHandler runs the command against a mock server with no
// handlers, so any request that escapes client-side validation 404s.
func runCompositeRunJobsNoHandler(t *testing.T, args ...string) (string, url.Values, error) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommandForCompositeRunJobs()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"get", "composite-run-jobs"}, args...))

	return buf.String(), nil, cmd.Execute()
}

// TestGetCompositeRunJobsServerErrors: the list route is org-scoped, so a
// caller in the wrong organization gets 403 and a bad token 401 -- both exit 4.
func TestGetCompositeRunJobsServerErrors(t *testing.T) {
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

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+compositeRunJobsPath,
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondError(w, tc.status, "nope")
				})

			cmd := setupTestCommandForCompositeRunJobs()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{"get", "composite-run-jobs"})

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

// TestGetCompositeRunJobsAdvertisedColumnsAreReal: the --columns help text and
// the default column set are both hand-written lists of wire field names, and a
// hand-written list of what a type has is wrong within months. Check both
// against the type the printer actually reflects.
func TestGetCompositeRunJobsAdvertisedColumnsAreReal(t *testing.T) {
	available := output.AvailableColumns([]composite_run_jobs.CompositeRunJobTiny{}, nil)
	if len(available) == 0 {
		t.Fatal("AvailableColumns returned nothing; the reflection is broken, not the list")
	}

	cmd := NewCompositeRunJobsCommand()
	usage := cmd.Flags().Lookup("columns").Usage
	_, advertised, ok := strings.Cut(usage, "Available: ")
	if !ok {
		t.Fatalf("--columns usage no longer carries an \"Available: \" list: %q", usage)
	}

	for _, list := range map[string]string{
		"--columns help":  advertised,
		"default columns": compositeRunJobColumns,
	} {
		var names []string
		for _, c := range strings.Split(list, ",") {
			names = append(names, strings.TrimSpace(c))
		}
		if err := output.ValidateColumns(names, available); err != nil {
			t.Errorf("%v", err)
		}
	}

	// The other direction: a field the type gained and the help never mentioned.
	for _, col := range available {
		if !strings.Contains(advertised, col) {
			t.Errorf("column %q exists on CompositeRunJobTiny but --columns help does not list it", col)
		}
	}
}
