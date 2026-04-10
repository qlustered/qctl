package kill

import (
	"bytes"
	"encoding/json"
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

func setupKillProfilingTestCommand() *cobra.Command {
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

	// Add kill command with profiling-job subcommand
	killCmd := NewCommand()
	rootCmd.AddCommand(killCmd)

	return rootCmd
}

func sampleProfilingJobTinyForKill() api.TrainingJobTinySchema {
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

func sampleProfilingJobFullForKill() api.TrainingJobFullSchema {
	state := api.TrainingJobStateKilled
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	return api.TrainingJobFullSchema{
		ID:               123,
		DatasetID:        1,
		DatasetName:      "test-dataset",
		SettingsModelID:  10,
		State:            state,
		Step:             api.Analysis,
		AttemptID:        1,
		UnresolvedAlerts: 0,
		CreatedAt:        createdAt,
		AnalysisTasks:    []api.AnalysisTaskTinySchema{},
	}
}

func TestKillProfilingJobSingle(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Register handler for patching (killing) the job
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/training-jobs/123", func(w http.ResponseWriter, r *http.Request) {
		// Verify state is being set to killed
		var req api.TrainingJobPatchRequestSchema
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.State != api.TrainingJobStateKilled {
			t.Errorf("expected state to be 'killed', got: %s", req.State)
		}
		testutil.RespondJSON(w, http.StatusOK, sampleProfilingJobFullForKill())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "profiling-job", "123", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "123") {
		t.Errorf("expected output to contain job ID 123, got: %s", output)
	}
	if !strings.Contains(output, "Successfully") {
		t.Errorf("expected output to contain 'Successfully', got: %s", output)
	}
}

func TestKillProfilingJobMultiple(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	killedJobs := make(map[int]bool)

	// Register handler for patching (killing) jobs
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/training-jobs/123", func(w http.ResponseWriter, r *http.Request) {
		killedJobs[123] = true
		resp := sampleProfilingJobFullForKill()
		resp.ID = 123
		testutil.RespondJSON(w, http.StatusOK, resp)
	})
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/training-jobs/456", func(w http.ResponseWriter, r *http.Request) {
		killedJobs[456] = true
		resp := sampleProfilingJobFullForKill()
		resp.ID = 456
		testutil.RespondJSON(w, http.StatusOK, resp)
	})
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/training-jobs/789", func(w http.ResponseWriter, r *http.Request) {
		killedJobs[789] = true
		resp := sampleProfilingJobFullForKill()
		resp.ID = 789
		testutil.RespondJSON(w, http.StatusOK, resp)
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "profiling-job", "123", "456", "789", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(killedJobs) != 3 {
		t.Errorf("expected 3 jobs to be killed, got %d", len(killedJobs))
	}
}

func TestKillProfilingJobWithFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return jobs matching filter
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, profiling.ProfilingJobsPage{
			Results: []api.TrainingJobTinySchema{
				sampleProfilingJobTinyForKill(),
			},
			TotalRows: testutil.IntPtr(1),
			Page:      testutil.IntPtr(1),
		})
	})

	// Kill the job
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/training-jobs/123", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleProfilingJobFullForKill())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "profiling-job", "--filter", "table-id=1", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestKillProfilingJobDryList(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return jobs matching filter
	job1 := sampleProfilingJobTinyForKill()
	job2 := sampleProfilingJobTinyForKill()
	job2.ID = 456
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

	rootCmd := setupKillProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "profiling-job", "--filter", "table-id=1", "--dry-list"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "123") {
		t.Errorf("expected output to contain job ID 123, got: %s", output)
	}
	if !strings.Contains(output, "456") {
		t.Errorf("expected output to contain job ID 456, got: %s", output)
	}
	if !strings.Contains(output, "2 total") {
		t.Errorf("expected output to contain '2 total', got: %s", output)
	}
}

func TestKillProfilingJobNoMatch(t *testing.T) {
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

	rootCmd := setupKillProfilingTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "profiling-job", "--filter", "table-id=999", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "No jobs") {
		t.Errorf("expected output to contain 'No jobs', got: %s", output)
	}
}

func TestKillProfilingJobInvalidID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	rootCmd.SetArgs([]string{"kill", "profiling-job", "not-a-number", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ID, got nil")
	}

	if !strings.Contains(err.Error(), "invalid job ID") {
		t.Fatalf("expected 'invalid job ID' error, got: %v", err)
	}
}

func TestKillProfilingJobMissingArgs(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	rootCmd.SetArgs([]string{"kill", "profiling-job"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}

	if !strings.Contains(err.Error(), "must specify") {
		t.Fatalf("expected 'must specify' error, got: %v", err)
	}
}

func TestKillProfilingJobBothIDsAndFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	rootCmd.SetArgs([]string{"kill", "profiling-job", "123", "--filter", "table-id=1"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for both IDs and filter, got nil")
	}

	if !strings.Contains(err.Error(), "cannot specify both") {
		t.Fatalf("expected 'cannot specify both' error, got: %v", err)
	}
}

func TestKillProfilingJobDryListWithoutFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	rootCmd.SetArgs([]string{"kill", "profiling-job", "123", "--dry-list"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for --dry-list without --filter, got nil")
	}

	if !strings.Contains(err.Error(), "--dry-list requires --filter") {
		t.Fatalf("expected '--dry-list requires --filter' error, got: %v", err)
	}
}

func TestKillProfilingJobNotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Force plaintext credential store (empty for this test)
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Setup config but no credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	rootCmd := setupKillProfilingTestCommand()
	rootCmd.SetArgs([]string{"kill", "profiling-job", "123", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("expected 'not logged in' error, got: %v", err)
	}
}

func TestKillProfilingJobServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return server error when killing job
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/training-jobs/123", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Internal server error",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"kill", "profiling-job", "123", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for server error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to kill") {
		t.Fatalf("expected 'failed to kill' error, got: %v", err)
	}
}

func TestKillProfilingJobInvalidFilterKey(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	rootCmd.SetArgs([]string{"kill", "profiling-job", "--filter", "invalid-key=value", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid filter key, got nil")
	}

	if !strings.Contains(err.Error(), "unknown filter key") {
		t.Fatalf("expected 'unknown filter key' error, got: %v", err)
	}
}

func TestKillProfilingJobInvalidFilterFormat(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillProfilingTestCommand()
	rootCmd.SetArgs([]string{"kill", "profiling-job", "--filter", "badformat", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid filter format, got nil")
	}

	if !strings.Contains(err.Error(), "invalid filter format") {
		t.Fatalf("expected 'invalid filter format' error, got: %v", err)
	}
}
