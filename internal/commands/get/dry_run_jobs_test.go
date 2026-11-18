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

// setupTestCommandForDryRunJobs creates a root command with global flags and
// adds the dry-run-jobs command.
func setupTestCommandForDryRunJobs() *cobra.Command {
	rootCmd := newTestRootCmd()
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewDryRunJobsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

// runGetDryRunJobs wires env + mock (table 5 by ID, "orders" by name) and runs
// `qctl get dry-run-jobs args...`. The list endpoint is /datasets/5/dry-runs.
func runGetDryRunJobs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 42, "name": "orders"})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"total_rows": 1, "page": 1,
			"results": []interface{}{map[string]interface{}{"id": 42, "name": "orders", "state": "active"}},
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"total_rows": 1, "page": 1,
			"results": []interface{}{map[string]interface{}{
				"id": 7, "dataset_id": 42, "dataset_name": "orders", "state": "finished",
			}},
		})
	})

	cmd := setupTestCommandForDryRunJobs()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"get", "dry-run-jobs"}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

func TestGetDryRunJobs_AllThreeSelectorForms(t *testing.T) {
	for _, args := range [][]string{
		{"42"},
		{"--table", "orders"},
		{"--table-id", "42"},
	} {
		out, err := runGetDryRunJobs(t, args...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		if !strings.Contains(out, "finished") {
			t.Errorf("%v did not render the list; got:\n%s", args, out)
		}
	}
}

// The message must tell a user the positional form exists, now that it does.
func TestGetDryRunJobs_NoSelectorMentionsThePositional(t *testing.T) {
	_, err := runGetDryRunJobs(t)
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "positional argument") {
		t.Errorf("error = %q, want it to mention the positional form", err.Error())
	}
}

// --interval 0 reaches the same watchPoll line --watch-interval 0 did, so it
// panicked the same way. The table lookup below must never be reached: a usage
// error costs no requests.
func TestGetDryRunJobsRejectsNonPositiveInterval(t *testing.T) {
	for _, args := range [][]string{
		{"--table-id", "5", "--watch", "--interval", "0"},
		{"--table-id", "5", "--watch", "--interval", "-2"},
		{"--table-id", "5", "--interval", "0"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/5", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 5, "name": "orders"})
			})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/5/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"results": []interface{}{}, "total_rows": 0, "page": 1,
				})
			})

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupTestCommandForDryRunJobs()
			cmd.SetArgs(append([]string{"get", "dry-run-jobs"}, args...))

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected an error for %v", args)
			}
			// The flag is --interval here, not --watch-interval: an error that
			// names a flag this command does not have is worse than no error.
			if !strings.Contains(err.Error(), "--interval must be at least 1 second") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if strings.Contains(err.Error(), "--watch-interval") {
				t.Errorf("error names a flag this command does not define: %v", err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if calls != 0 {
				t.Errorf("expected no API call before the usage error, got %d", calls)
			}
		})
	}
}

// atlas defaults this endpoint to 20 rows, so a request that sends no limit
// is capped at 20 in every format. qctl sends --page and --limit, and -o json
// says where the page sits.
func TestGetDryRunJobsSendsPageAndLimit(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var query string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"total_rows": 45, "page": 2,
			"results": []interface{}{map[string]interface{}{"id": 7, "dataset_id": 42, "state": "finished"}},
		})
	})

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--table-id", "42", "-o", "json"}, "limit=100&page=1"},
		{[]string{"--table-id", "42", "--page", "2", "--limit", "20", "-o", "json"}, "limit=20&page=2"},
	} {
		cmd := setupTestCommandForDryRunJobs()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(append([]string{"get", "dry-run-jobs"}, tc.args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if query != tc.want {
			t.Errorf("%v sent %q, want %q", tc.args, query, tc.want)
		}
		if tc.want == "limit=20&page=2" {
			assertEnvelopeNextPage(t, stdout.Bytes(), stderr.String(), 3)
		}
	}
}

func TestGetDryRunJobsHTTPErrorHasOneContextPrefix(t *testing.T) {
	for _, watch := range []bool{false, true} {
		t.Run(map[bool]string{false: "one-shot", true: "watch"}[watch], func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 42, "name": "orders"})
			})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{"detail": "missing"})
			})

			args := []string{"get", "dry-run-jobs", "--table-id", "42"}
			if watch {
				args = append(args, "--watch")
			}
			cmd := setupTestCommandForDryRunJobs()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected an HTTP error")
			}
			if got := strings.Count(err.Error(), "failed to list dry-run jobs:"); got != 1 {
				t.Errorf("error has %d context prefixes, want 1: %v", got, err)
			}
		})
	}
}
