package run

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
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

func sampleProfilingJobTinyForRun() api.TrainingJobTinySchema {
	state := api.TrainingJobStateRunning
	updatedAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	return api.TrainingJobTinySchema{
		ID:              123,
		DatasetID:       1,
		DatasetName:     "test-dataset",
		SettingsModelID: 10,
		State:           state,
		Step:            api.TrainingJobStepAnalysis,
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

	if !strings.Contains(err.Error(), "must specify either --table-id or --filter") {
		t.Fatalf("expected 'must specify either --table-id or --filter' error, got: %v", err)
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

	if !strings.Contains(err.Error(), "cannot specify both --table-id and --filter") {
		t.Fatalf("expected 'cannot specify both --table-id and --filter' error, got: %v", err)
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
}

func TestRunProfilingJobNotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Force plaintext credential store (empty for this test)
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

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
}
