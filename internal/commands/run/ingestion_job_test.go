package run

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupRunTestCommand() *cobra.Command {
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

	// Add run command with ingestion-job subcommand
	runCmd := NewCommand()
	rootCmd.AddCommand(runCmd)

	return rootCmd
}

func sampleIngestionJobTiny() api.IngestionJobTinySchema {
	state := api.IngestionJobState("running")
	updatedAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	return api.IngestionJobTinySchema{
		ID:          123,
		DatasetID:   1,
		DatasetName: "test-dataset",
		FileName:    testutil.StringPtr("test-file.csv"),
		Key:         "test-key",
		Kind:        api.IngestionJobKindFile,
		State:       state,
		UpdatedAt:   updatedAt,
	}
}

func TestRunIngestionJobSingle(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Register handler for running single job
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/123/run-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{
			"result": "Job 123 triggered successfully",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "ingestion-job", "123", "--yes"})

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

func TestRunIngestionJobSanitizesServerAcknowledgement(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/123/run-ingestion-job", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "queued\x1b[31m\x00\x7f\u0085"})
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "ingestion-job", "123", "--yes"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stdout.String(); strings.ContainsRune(got, '\x1b') || !strings.Contains(got, `queued\x1b[31m\x00\x7f\x85`) {
		t.Errorf("acknowledgement was not sanitized: %q", got)
	}
}

func TestRunIngestionJobMultiple(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var receivedIDs []int
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/run-ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		var req api.IngestionJobsRunRequestSchema
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		receivedIDs = req.IngestionJobIds
		testutil.RespondJSON(w, http.StatusOK, map[string]string{
			"result": "Jobs triggered successfully",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "ingestion-job", "123", "456", "789", "--yes"})

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

func TestRunIngestionJobWithFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return jobs matching filter
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results: []api.IngestionJobTinySchema{
				sampleIngestionJobTiny(),
			},
			TotalRows: testutil.IntPtr(1),
			Page:      testutil.IntPtr(1),
		})
	})

	// Run the single matching job
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/123/run-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{
			"result": "Job 123 triggered successfully",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "table-id=1,state=running", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunIngestionJobDryList(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return jobs matching filter
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		job1 := sampleIngestionJobTiny()
		job2 := sampleIngestionJobTiny()
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

	rootCmd := setupRunTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "table-id=1", "--dry-list"})

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

func TestRunIngestionJobNoMatch(t *testing.T) {
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

	rootCmd := setupRunTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "table-id=999", "--yes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "No jobs") {
		t.Errorf("expected output to contain 'No jobs', got: %s", output)
	}
}

func TestRunIngestionJobInvalidID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "not-a-number", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ID, got nil")
	}

	if !strings.Contains(err.Error(), "invalid job ID") {
		t.Fatalf("expected 'invalid job ID' error, got: %v", err)
	}
}

func TestRunIngestionJobMissingArgs(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}

	if !strings.Contains(err.Error(), "must specify") {
		t.Fatalf("expected 'must specify' error, got: %v", err)
	}
}

func TestRunIngestionJobBothIDsAndFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "123", "--filter", "table-id=1"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for both IDs and filter, got nil")
	}

	if !strings.Contains(err.Error(), "cannot specify both") {
		t.Fatalf("expected 'cannot specify both' error, got: %v", err)
	}
}

func TestRunIngestionJobDryListWithoutFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "123", "--dry-list"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for --dry-list without --filter, got nil")
	}

	if !strings.Contains(err.Error(), "--dry-list requires --filter") {
		t.Fatalf("expected '--dry-list requires --filter' error, got: %v", err)
	}
}

func TestRunIngestionJobNotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Setup config but no credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "123", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("expected 'not logged in' error, got: %v", err)
	}
}

func TestRunIngestionJobServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return server error when running job
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/ingestion-jobs/123/run-ingestion-job", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Internal server error",
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"run", "ingestion-job", "123", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for server error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to run job") {
		t.Fatalf("expected 'failed to run job' error, got: %v", err)
	}
}

func TestRunIngestionJobInvalidFilterKey(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "invalid-key=value", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid filter key, got nil")
	}

	if !strings.Contains(err.Error(), "unknown filter key") {
		t.Fatalf("expected 'unknown filter key' error, got: %v", err)
	}
}

func TestRunIngestionJobInvalidFilterFormat(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "badformat", "--yes"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid filter format, got nil")
	}

	if !strings.Contains(err.Error(), "invalid filter format") {
		t.Fatalf("expected 'invalid filter format' error, got: %v", err)
	}
}

func TestRunIngestionJobCreatedByFilterReachesWire(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	const creatorID = "11111111-2222-3333-4444-555555555555"
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("whodunit_id"); got != creatorID {
			t.Errorf("whodunit_id = %q, want %q", got, creatorID)
		}
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results:   []api.IngestionJobTinySchema{},
			TotalRows: testutil.IntPtr(0),
			Page:      testutil.IntPtr(1),
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "created-by=" + creatorID, "--dry-list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Each states= member is trimmed and sent as its own state parameter, and
// is-dry-run takes any strconv.ParseBool spelling.
func TestRunIngestionJobStateAndDryRunFiltersReachWire(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Query()["state"], []string{"staged", "pending"}; !slices.Equal(got, want) {
			t.Errorf("state = %q, want %q", got, want)
		}
		if got := r.URL.Query().Get("is_dry_run"); got != "true" {
			t.Errorf("is_dry_run = %q, want \"true\"", got)
		}
		// A filter without a kind matches only the kinds that can be run
		// again and stopped.
		if got, want := r.URL.Query()["kind"], []string{"composite_dry_run", "dry_run", "file"}; !slices.Equal(got, want) {
			t.Errorf("kind = %q, want %q", got, want)
		}
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results:   []api.IngestionJobTinySchema{},
			TotalRows: testutil.IntPtr(0),
			Page:      testutil.IntPtr(1),
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "states=staged| pending,is-dry-run=TRUE", "--dry-list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(mock.Requests()); got != 1 {
		t.Errorf("made %d request(s), want 1", got)
	}
}

func TestRunIngestionJobKindAndFileFiltersReachWire(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Query()["kind"], []string{"file", "dry_run"}; !slices.Equal(got, want) {
			t.Errorf("kind = %q, want %q", got, want)
		}
		if got := r.URL.Query().Get("stored_item_id"); got != "7" {
			t.Errorf("stored_item_id = %q, want 7", got)
		}
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results:   []api.IngestionJobTinySchema{},
			TotalRows: testutil.IntPtr(0),
			Page:      testutil.IntPtr(1),
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "file-id=7,kinds=file| dry_run", "--dry-list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(mock.Requests()); got != 1 {
		t.Errorf("made %d request(s), want 1", got)
	}
}

func TestRunIngestionJobUnactionableKindIsBadUsageBeforeBootstrap(t *testing.T) {
	for _, kind := range []string{"quarantine_reprocess", "composite_carrier"} {
		t.Run(kind, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			rootCmd := setupRunTestCommand()
			rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "kinds=file|" + kind, "--yes"})
			err := rootCmd.Execute()
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (%v)", got, qerrors.ExitBadUsage, err)
			}
		})
	}
}

func TestRunIngestionJobCreatedByRequiresUUIDBeforeBootstrap(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "created-by=dana@example.com", "--yes"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected a non-UUID creator to fail")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "created-by user ID") {
		t.Errorf("error does not explain the UUID requirement: %v", err)
	}
}

func TestRunIngestionJobFilterIgnoredByTheServerActsOnNothing(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// A server that ignores kind lists a job of a kind the filter excluded.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		job := sampleIngestionJobTiny()
		job.Kind = api.IngestionJobKindQuarantineReprocess
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results:   []api.IngestionJobTinySchema{job},
			TotalRows: testutil.IntPtr(1),
			Page:      testutil.IntPtr(1),
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunTestCommand()
	rootCmd.SetArgs([]string{"run", "ingestion-job", "--filter", "table-id=1", "--yes"})
	err := rootCmd.Execute()
	if got := qerrors.GetExitCode(err); got != qerrors.ExitAPIIncompatible {
		t.Errorf("exit code = %d, want %d (%v)", got, qerrors.ExitAPIIncompatible, err)
	}
	if got := len(mock.Requests()); got != 1 {
		t.Errorf("made %d request(s), want only the listing", got)
	}
}
