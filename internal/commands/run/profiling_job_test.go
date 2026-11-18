package run

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupRunProfilingTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	// Add global flags (same as in root command)
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity")

	// Add run command with profiling-job subcommand
	runCmd := NewCommand()
	rootCmd.AddCommand(runCmd)

	return rootCmd
}

func TestRunProfilingJobBulkExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		allFail    bool
		wantCode   int
		wantDetail string
	}{
		{name: "all succeed", status: http.StatusOK},
		{name: "one missing table", status: http.StatusNotFound, wantCode: qerrors.ExitNotFound},
		{name: "one forbidden table", status: http.StatusForbidden, wantCode: qerrors.ExitUnauthorized},
		{name: "one server failure", status: http.StatusInternalServerError, wantCode: qerrors.ExitGenericError},
		{name: "one without training files", status: http.StatusConflict, wantCode: qerrors.ExitBadUsage, wantDetail: "qctl explain profiling-job"},
		{name: "all missing", status: http.StatusNotFound, allFail: true, wantCode: qerrors.ExitNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			jobs := make([]api.TrainingJobTinySchema, 3)
			attempts := make(map[int]int)
			for i := range jobs {
				id := i + 1
				jobs[i] = sampleProfilingJobTinyForRun()
				jobs[i].ID, jobs[i].DatasetID = id, id
				path := fmt.Sprintf("/api/orgs/%s/datasets/%d/run-training-job", testOrgID, id)
				mock.RegisterHandler("POST", path, func(w http.ResponseWriter, _ *http.Request) {
					attempts[id]++
					if tc.allFail || id == 2 && tc.status != http.StatusOK {
						if tc.status == http.StatusConflict {
							testutil.RespondJSON(w, tc.status, map[string]interface{}{
								"detail": map[string]interface{}{"msg": "No training files", "error_code": profiling.CodeNoTrainingFiles},
							})
						} else {
							testutil.RespondError(w, tc.status, "profiling refused")
						}
						return
					}
					testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "queued"})
				})
			}
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, profiling.ProfilingJobsPage{Results: jobs, TotalRows: testutil.IntPtr(3)})
			})
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupRunProfilingTestCommand()
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetArgs([]string{"run", "profiling-job", "--filter", "search=orders", "--yes"})
			err := cmd.Execute()
			if code := qerrors.GetExitCode(err); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d; error: %v", code, tc.wantCode, err)
			}
			for id := 1; id <= 3; id++ {
				if attempts[id] != 1 {
					t.Errorf("table %d attempts = %d, want 1", id, attempts[id])
				}
			}
			wantSuccesses := 2
			if tc.allFail {
				wantSuccesses = 0
			} else if tc.status == http.StatusOK {
				wantSuccesses = 3
			}
			if n := strings.Count(out.String(), "Successfully triggered"); n != wantSuccesses {
				t.Errorf("success messages = %d, want %d; stdout: %s", n, wantSuccesses, out.String())
			}
			if tc.wantCode != 0 && !tc.allFail && !strings.Contains(err.Error(), "failed to run profiling for 1 of 3 tables") {
				t.Errorf("missing failure count: %v", err)
			}
			if tc.wantDetail != "" && !strings.Contains(err.Error(), tc.wantDetail) {
				t.Errorf("missing %q: %v", tc.wantDetail, err)
			}
		})
	}
}

func sampleProfilingJobTinyForRun() api.TrainingJobTinySchema {
	state := api.TrainingJobStateRunning
	updatedAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	return api.TrainingJobTinySchema{
		ID:              123,
		DatasetID:       1,
		DatasetName:     "test-dataset",
		SettingsModelID: 10,
		State:           state,
		Step:            api.Analysis,
		UpdatedAt:       updatedAt,
	}
}

func TestRunProfilingJobWithTableID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Register handler for running profiling by table ID
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1/run-training-job", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{
			"result": "Profiling job triggered for table 1",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "profiling-job", "--table-id", "1", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "Successfully") {
		t.Errorf("expected output to contain 'Successfully', got: %s", output)
	}
	if !strings.Contains(output, "table 1") {
		t.Errorf("expected output to contain 'table 1', got: %s", output)
	}
}

// --table is a NAME. The fixture is named "42" and has ID 7 — the one case an
// id-first resolver can never serve, so an Atoi short-circuit cannot pass here.
func TestRunProfilingJobSanitizesServerAcknowledgement(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1/run-training-job", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "queued\x1b[31m\u009b"})
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "profiling-job", "--table-id", "1", "--yes"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stdout.String(); strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\u009b') || !strings.Contains(got, `queued\x1b[31m\x9b`) {
		t.Errorf("acknowledgement was not sanitized: %q", got)
	}
}

func TestRunProfilingJobWithTableName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var lookedUp string
	ran := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		lookedUp = r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
			Results: []api.DataSetTinySchema{{ID: 7, Name: "42"}},
		})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/7/run-training-job", func(w http.ResponseWriter, _ *http.Request) {
		ran++
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "triggered"})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "profiling-job", "--table", "42", "--yes"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
	if ran != 1 {
		t.Errorf("--table 42 must run profiling for table 7 exactly once, got %d call(s)", ran)
	}
	if !strings.Contains(stdout.String(), "table 7") {
		t.Errorf("expected output to name table 7, got: %s", stdout.String())
	}
}

func TestRunProfilingJobWithFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return jobs matching filter (the command extracts unique table IDs from jobs)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, profiling.ProfilingJobsPage{
			Results: []api.TrainingJobTinySchema{
				sampleProfilingJobTinyForRun(),
			},
			TotalRows: testutil.IntPtr(1),
			Page:      testutil.IntPtr(1),
		})
	})

	// Run profiling by table ID (extracted from job's DatasetID)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1/run-training-job", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{
			"result": "Job triggered",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "profiling-job", "--filter", "table-id=1", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// registerNoTrainingFiles answers run-training-job for table 1 with the 409
// the backend sends for a table with no file enabled for training.
func registerNoTrainingFiles(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/1/run-training-job", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
			"detail": map[string]interface{}{
				"msg":        "This table has no files enabled for training, so there is nothing to profile.",
				"error_code": "NO_TRAINING_FILES",
			},
		})
	})
}

func TestRunProfilingJobRefusedWithoutTrainingFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"table", []string{"run", "profiling-job", "--table-id", "1", "--yes"}},
		{"filter", []string{"run", "profiling-job", "--filter", "table-id=1", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, profiling.ProfilingJobsPage{
					Results:   []api.TrainingJobTinySchema{sampleProfilingJobTinyForRun()},
					TotalRows: testutil.IntPtr(1),
					Page:      testutil.IntPtr(1),
				})
			})
			registerNoTrainingFiles(mock)
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			rootCmd := setupRunProfilingTestCommand()
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs(tc.args)

			err := rootCmd.Execute()
			if err == nil {
				t.Fatal("expected the 409 to surface as an error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "qctl explain profiling-job") {
				t.Errorf("error must point at what enables a file for training, got: %v", err)
			}
		})
	}
}

func TestRunProfilingJobDryList(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return jobs matching filter with different table IDs
	job1 := sampleProfilingJobTinyForRun()
	job1.DatasetID = 1
	job2 := sampleProfilingJobTinyForRun()
	job2.ID = 456
	job2.DatasetID = 2
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, profiling.ProfilingJobsPage{
			Results:   []api.TrainingJobTinySchema{job1, job2},
			TotalRows: testutil.IntPtr(2),
			Page:      testutil.IntPtr(1),
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "profiling-job", "--filter", "search=test", "--dry-list"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	// Now dry list shows unique table IDs, not job IDs
	if !strings.Contains(output, "Tables that would have profiling run") {
		t.Errorf("expected output to contain 'Tables that would have profiling run', got: %s", output)
	}
	if !strings.Contains(output, "2 total") {
		t.Errorf("expected output to contain '2 total', got: %s", output)
	}
}

func TestRunProfilingJobNoMatch(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return no jobs matching filter
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, profiling.ProfilingJobsPage{
			Results:   []api.TrainingJobTinySchema{},
			TotalRows: testutil.IntPtr(0),
			Page:      testutil.IntPtr(1),
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "profiling-job", "--filter", "table-id=999", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "No jobs") {
		t.Errorf("expected output to contain 'No jobs', got: %s", output)
	}
}

func TestRunProfilingJobInvalidTableID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	rootCmd.SetArgs([]string{"run", "profiling-job", "--table-id", "0", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid table ID, got nil")
	}

	if !strings.Contains(err.Error(), "--table-id must be a positive integer") {
		t.Fatalf("expected '--table-id must be a positive integer' error, got: %v", err)
	}
}

// Flag validation runs before Bootstrap, so a typo is reported as a typo even
// with no credentials — OptionalTableID's own check would only be reached after
// auth and would answer "not logged in" instead.
func TestRunProfilingJobInvalidTableIDBeatsMissingCredentials(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Config but deliberately no credentials.
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	rootCmd := setupRunProfilingTestCommand()
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs([]string{"run", "profiling-job", "--table-id", "0", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected an error for --table-id 0")
	}
	if !strings.Contains(err.Error(), "--table-id must be a positive integer") {
		t.Fatalf("expected the flag error, not an auth error, got: %v", err)
	}
}

func TestRunProfilingJobMissingArgs(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	rootCmd.SetArgs([]string{"run", "profiling-job"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}

	if !strings.Contains(err.Error(), "must specify either a table (--table/--table-id) or --filter") {
		t.Fatalf("expected the 'must specify either a table or --filter' error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestRunProfilingJobBothTableIDAndFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	rootCmd.SetArgs([]string{"run", "profiling-job", "--table-id", "123", "--filter", "table-id=1"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for both --table-id and --filter, got nil")
	}

	if !strings.Contains(err.Error(), "cannot specify both a table (--table/--table-id) and --filter") {
		t.Fatalf("expected the 'cannot specify both a table and --filter' error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestRunProfilingJobDryListWithoutFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	rootCmd.SetArgs([]string{"run", "profiling-job", "--table-id", "123", "--dry-list"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for --dry-list without --filter, got nil")
	}

	if !strings.Contains(err.Error(), "--dry-list requires --filter") {
		t.Fatalf("expected '--dry-list requires --filter' error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestRunProfilingJobNotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Setup config but no credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	rootCmd := setupRunProfilingTestCommand()
	rootCmd.SetArgs([]string{"run", "profiling-job", "--table-id", "123", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("expected 'not logged in' error, got: %v", err)
	}
}

func TestRunProfilingJobInvalidFilterKey(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	rootCmd.SetArgs([]string{"run", "profiling-job", "--filter", "invalid-key=value", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid filter key, got nil")
	}

	if !strings.Contains(err.Error(), "unknown filter key") {
		t.Fatalf("expected 'unknown filter key' error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestRunProfilingJobInvalidFilterFormat(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	rootCmd.SetArgs([]string{"run", "profiling-job", "--filter", "badformat", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid filter format, got nil")
	}

	if !strings.Contains(err.Error(), "invalid filter format") {
		t.Fatalf("expected 'invalid filter format' error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

// The filter's own table-id=<value> is parsed independently of --table-id and
// used to exit 1 while its two filter-parsing siblings (unknown filter key,
// invalid filter format) already exited 2 — the same class of typo, three
// different codes.
func TestRunProfilingJobFilterInvalidTableIDValue(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	rootCmd.SetArgs([]string{"run", "profiling-job", "--filter", "table-id=abc", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for a non-numeric filter table-id, got nil")
	}
	if !strings.Contains(err.Error(), "invalid table-id") {
		t.Fatalf("expected 'invalid table-id' error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}
