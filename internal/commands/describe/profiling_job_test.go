package describe

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

// setupDescribeProfilingTestCommand creates a root command for testing describe profiling-job
func setupDescribeProfilingTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	// Add global flags
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Max column width")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Allow plaintext secrets")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow insecure http")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity")

	// Add describe command
	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Describe resources",
	}
	describeCmd.AddCommand(NewProfilingJobCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

// sampleProfilingJobFull creates a sample full profiling job for testing
func sampleProfilingJobFull() profiling.ProfilingJobFull {
	createdAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	startedAt := time.Date(2025, 1, 15, 10, 1, 0, 0, time.UTC)
	finishedAt := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	msg := "Profiling completed successfully"

	return profiling.ProfilingJobFull{
		ID:               123,
		DatasetID:        11,
		DatasetName:      "user_data",
		SettingsModelID:  30,
		MigrationModelID: nil,
		State:            api.TrainingJobStateFinished,
		Step:             api.TrainingJobStepFinished,
		AttemptID:        1,
		UnresolvedAlerts: 0,
		Msg:              &msg,
		MsgLogs:          nil,
		CreatedAt:        createdAt,
		StartedAt:        &startedAt,
		FinishedAt:       &finishedAt,
		AnalysisTasks:    []api.AnalysisTaskTinySchema{},
	}
}

func TestDescribeProfilingJobCommand_DefaultYAML(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs/123", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleProfilingJobFull())
	})

	cmd := setupDescribeProfilingTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "profiling-job", "123"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Verify YAML output contains expected manifest structure
	expectedStrings := []string{
		"apiVersion: qluster.ai/v1",
		"kind: ProfilingJob",
		"metadata:",
		"name: user_data",
		"spec:",
		"dataset_id: 11",
		"status:",
		"state: finished",
		"step: finished",
	}

	for _, expected := range expectedStrings {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected YAML output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestDescribeProfilingJobCommand_JSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs/123", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleProfilingJobFull())
	})

	cmd := setupDescribeProfilingTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "profiling-job", "123", "--output", "json"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Verify JSON output contains expected fields
	expectedStrings := []string{
		`"apiVersion": "qluster.ai/v1"`,
		`"kind": "ProfilingJob"`,
		`"dataset_id": 11`,
		`"state": "finished"`,
	}

	for _, expected := range expectedStrings {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected JSON output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestDescribeProfilingJobCommand_TableOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs/123", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleProfilingJobFull())
	})

	cmd := setupDescribeProfilingTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "profiling-job", "123", "--output", "table"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Table output should succeed without error
	output := buf.String()
	if output == "" {
		t.Error("Expected non-empty table output")
	}
}

func TestDescribeProfilingJobCommand_InvalidID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupDescribeProfilingTestCommand()
	cmd.SetArgs([]string{"describe", "profiling-job", "not-a-number"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ID")
	}

	if !strings.Contains(err.Error(), "invalid profiling job ID") {
		t.Errorf("expected 'invalid profiling job ID' error, got: %v", err)
	}
}

func TestDescribeProfilingJobCommand_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs/999", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Not found")
	})

	cmd := setupDescribeProfilingTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "profiling-job", "999"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for not found")
	}

	if !strings.Contains(err.Error(), "failed to get profiling job") {
		t.Errorf("expected 'failed to get profiling job' error, got: %v", err)
	}
}

func TestDescribeProfilingJobCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	// Setup config but NO credentials
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupDescribeProfilingTestCommand()
	cmd.SetArgs([]string{"describe", "profiling-job", "123"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when not logged in")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected 'not logged in' error, got: %v", err)
	}
}

func TestDescribeProfilingJobCommand_MissingArg(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupDescribeProfilingTestCommand()
	cmd.SetArgs([]string{"describe", "profiling-job"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing argument")
	}
}
