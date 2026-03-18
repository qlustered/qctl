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
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupKillTestCommand() *cobra.Command {
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

	// Add kill command with ingestion-job subcommand
	killCmd := NewCommand()
	rootCmd.AddCommand(killCmd)

	return rootCmd
}

func sampleIngestionJobTinyForKill() api.IngestionJobTinySchema {
	state := api.IngestionJobState("running")
	updatedAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	return api.IngestionJobTinySchema{
		ID:          123,
		DatasetID:   1,
		DatasetName: "test-dataset",
		FileName:    "test-file.csv",
		Key:         "test-key",
		State:       state,
		UpdatedAt:   updatedAt,
	}
}

func TestKillIngestionJobSingle(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Register handler for killing single job
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/123/kill-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{
			"result": "Job 123 killed successfully",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "123", "--yes"})

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

func TestKillIngestionJobMultiple(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var receivedIDs []int
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/kill-ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		var req api.IngestionJobsKillRequestSchema
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		receivedIDs = req.IngestionJobIds
		testutil.RespondJSON(w, http.StatusOK, map[string]string{
			"result": "Jobs killed successfully",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "123", "456", "789", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(receivedIDs) != 3 {
		t.Fatalf("expected 3 job IDs, got %d", len(receivedIDs))
	}
	if receivedIDs[0] != 123 || receivedIDs[1] != 456 || receivedIDs[2] != 789 {
		t.Fatalf("expected job IDs [123, 456, 789], got %v", receivedIDs)
	}
}

func TestKillIngestionJobWithFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return jobs matching filter
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results: []api.IngestionJobTinySchema{
				sampleIngestionJobTinyForKill(),
			},
			TotalRows: testutil.IntPtr(1),
			Page:      testutil.IntPtr(1),
		})
	})

	// Kill the single matching job
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/123/kill-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{
			"result": "Job 123 killed successfully",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "--filter", "table-id=1,state=running", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestKillIngestionJobDryList(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return jobs matching filter
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		job1 := sampleIngestionJobTinyForKill()
		job2 := sampleIngestionJobTinyForKill()
		job2.ID = 456
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results:   []api.IngestionJobTinySchema{job1, job2},
			TotalRows: testutil.IntPtr(2),
			Page:      testutil.IntPtr(1),
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "--filter", "table-id=1", "--dry-list"})

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

func TestKillIngestionJobNoMatch(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return no jobs matching filter
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results:   []api.IngestionJobTinySchema{},
			TotalRows: testutil.IntPtr(0),
			Page:      testutil.IntPtr(1),
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "--filter", "table-id=999", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "No jobs") {
		t.Errorf("expected output to contain 'No jobs', got: %s", output)
	}
}

func TestKillIngestionJobInvalidID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "not-a-number", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ID, got nil")
	}

	if !strings.Contains(err.Error(), "invalid job ID") {
		t.Fatalf("expected 'invalid job ID' error, got: %v", err)
	}
}

func TestKillIngestionJobMissingArgs(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	rootCmd.SetArgs([]string{"kill", "ingestion-job"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}

	if !strings.Contains(err.Error(), "must specify") {
		t.Fatalf("expected 'must specify' error, got: %v", err)
	}
}

func TestKillIngestionJobBothIDsAndFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "123", "--filter", "table-id=1"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for both IDs and filter, got nil")
	}

	if !strings.Contains(err.Error(), "cannot specify both") {
		t.Fatalf("expected 'cannot specify both' error, got: %v", err)
	}
}

func TestKillIngestionJobDryListWithoutFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "123", "--dry-list"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for --dry-list without --filter, got nil")
	}

	if !strings.Contains(err.Error(), "--dry-list requires --filter") {
		t.Fatalf("expected '--dry-list requires --filter' error, got: %v", err)
	}
}

func TestKillIngestionJobNotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Force plaintext credential store (empty for this test)
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Setup config but no credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	rootCmd := setupKillTestCommand()
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "123", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("expected 'not logged in' error, got: %v", err)
	}
}

func TestKillIngestionJobServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return server error when killing job
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/123/kill-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Internal server error",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "123", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for server error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to kill job") {
		t.Fatalf("expected 'failed to kill job' error, got: %v", err)
	}
}

func TestKillIngestionJobInvalidFilterKey(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "--filter", "invalid-key=value", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid filter key, got nil")
	}

	if !strings.Contains(err.Error(), "unknown filter key") {
		t.Fatalf("expected 'unknown filter key' error, got: %v", err)
	}
}

func TestKillIngestionJobInvalidFilterFormat(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "--filter", "badformat", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid filter format, got nil")
	}

	if !strings.Contains(err.Error(), "invalid filter format") {
		t.Fatalf("expected 'invalid filter format' error, got: %v", err)
	}
}

func TestKillIngestionJobFilterWithMultipleStates(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	apiCalled := false
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results:   []api.IngestionJobTinySchema{sampleIngestionJobTinyForKill()},
			TotalRows: testutil.IntPtr(1),
			Page:      testutil.IntPtr(1),
		})
	})

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/123/kill-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{
			"result": "Job 123 killed successfully",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupKillTestCommand()
	// Test with states filter (pipe-separated)
	rootCmd.SetArgs([]string{"kill", "ingestion-job", "--filter", "states=running|pending", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !apiCalled {
		t.Error("expected API to be called")
	}
}
