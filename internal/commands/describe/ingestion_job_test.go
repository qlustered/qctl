package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupIngestionDescribeCmd creates a root command with global flags and adds the describe ingestion-job command
func setupIngestionDescribeCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")

	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a resource",
	}
	describeCmd.AddCommand(NewIngestionJobCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

func sampleIngestionJobFullWithWhodunit() ingestion.IngestionJobFull {
	storedItemID := 99
	alertItemID := 123
	isAlertResolved := true
	cleanRows := 10
	badRows := 1
	ignoredRows := 0
	startedAt := time.Date(2025, 1, 20, 16, 0, 0, 0, time.UTC)
	createdAt := time.Date(2025, 1, 20, 15, 45, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 1, 20, 16, 5, 0, 0, time.UTC)
	msg := "processing"
	msgLogs := [][]interface{}{
		{"2025-01-20T16:00:00Z◭◘INFO◭◘job started"},
		{"2025-01-20T16:05:00Z◭◘ERROR◭◘failed to connect"},
	}

	userID := types.UUID{}
	_ = userID.UnmarshalText([]byte("123e4567-e89b-12d3-a456-426614174000"))

	return ingestion.IngestionJobFull{
		ID:                  1,
		DatasetID:           10,
		DatasetName:         "events",
		DataSourceModelID:   20,
		DataSourceModelName: "s3",
		SettingsModelID:     30,
		StoredItemID:        &storedItemID,
		FileName:            "events.csv",
		Key:                 "s3://bucket/events.csv",
		IsDryRun:            false,
		State:               api.IngestionJobState("running"),
		TryCount:            2,
		AlertItemID:         &alertItemID,
		IsAlertResolved:     &isAlertResolved,
		CleanRowsCount:      &cleanRows,
		BadRowsCount:        &badRows,
		IgnoredRowsCount:    &ignoredRows,
		Msg:                 &msg,
		CreatedAt:           createdAt,
		StartedAt:           &startedAt,
		UpdatedAt:           updatedAt,
		FinishedAt:          nil,
		MsgLogs:             &msgLogs,
		Whodunit: &api.UserInfoTinySchema{
			ID:        userID,
			Email:     "demo@cgtal.com",
			FirstName: "Demo",
			LastName:  "User",
		},
	}
}

func TestDescribeIngestionJobCommand_DefaultManifest(t *testing.T) {
	cmd := setupIngestionDescribeCmd()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	job := sampleIngestionJobFullWithWhodunit()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, job)
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "ingestion-job", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	expectedSnippets := []string{
		"apiVersion: qluster.ai/v1",
		"kind: IngestionJob",
		"ingestion_job_id: \"1\"",
		"id: 1",
		"dataset_id: 10",
		"data_source_model_id: 20",
		"file_name: events.csv",
		"id: 123e4567-e89b-12d3-a456-426614174000",
		"state: running",
		"logs:",
		"timestamp:", // Timestamp format may vary (quoted or unquoted)
		"publisher: INFO",
		"message: failed to connect",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(output, snippet) {
			t.Errorf("Output missing expected snippet %q\nOutput:\n%s", snippet, output)
		}
	}
}

func TestDescribeIngestionJobCommand_JSONManifest(t *testing.T) {
	cmd := setupIngestionDescribeCmd()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	job := sampleIngestionJobFullWithWhodunit()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, job)
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "ingestion-job", "1", "--output", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var manifest ingestion.IngestionJobManifest
	if err := json.Unmarshal(buf.Bytes(), &manifest); err != nil {
		t.Fatalf("Failed to unmarshal JSON output: %v\nOutput:\n%s", err, buf.String())
	}

	if manifest.Status == nil || manifest.Status.Whodunit == nil {
		t.Fatalf("Expected whodunit in status, got nil")
	}

	if manifest.Status.Whodunit.ID != "123e4567-e89b-12d3-a456-426614174000" {
		t.Errorf("Unexpected whodunit ID: %s", manifest.Status.Whodunit.ID)
	}

	if manifest.Spec.DatasetID != 10 || manifest.Spec.DataSourceModelID != 20 {
		t.Errorf("Spec fields not populated correctly: %+v", manifest.Spec)
	}

	if manifest.Metadata.Annotations["ingestion_job_id"] != "1" {
		t.Errorf("Expected ingestion_job_id annotation to be 1, got %s", manifest.Metadata.Annotations["ingestion_job_id"])
	}

	if len(manifest.Status.Logs) != 2 {
		t.Fatalf("Expected 2 log entries, got %d", len(manifest.Status.Logs))
	}

	first := manifest.Status.Logs[0]
	if first.Timestamp == nil || first.Timestamp.Format(time.RFC3339) != "2025-01-20T16:00:00Z" || first.Publisher != "INFO" || first.Message != "job started" {
		t.Errorf("Unexpected first log entry: %+v", first)
	}

	last := manifest.Status.Logs[1]
	if last.Publisher != "ERROR" || last.Message != "failed to connect" {
		t.Errorf("Unexpected second log entry: %+v", last)
	}
}

func TestDescribeIngestionJobCommand_TableLogs(t *testing.T) {
	cmd := setupIngestionDescribeCmd()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	job := sampleIngestionJobFullWithWhodunit()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, job)
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "ingestion-job", "1", "--output", "table"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	for _, snippet := range []string{"LOGS:", "TIMESTAMP", "PUBLISHER", "MESSAGE"} {
		if !strings.Contains(strings.ToUpper(output), snippet) {
			t.Errorf("Table output missing expected snippet %q\nOutput:\n%s", snippet, output)
		}
	}

	for _, snippet := range []string{"2025-01-20T16:00:00Z", "INFO", "job started"} {
		if !strings.Contains(output, snippet) {
			t.Errorf("Table output missing expected value %q\nOutput:\n%s", snippet, output)
		}
	}
}
