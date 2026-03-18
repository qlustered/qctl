package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/errorincidents"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// setupErrorIncidentTestCommand creates a root command with global flags and adds the describe error-incident command
func setupErrorIncidentTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	// Add global flags (same as in root command)
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")

	// Add describe command
	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a specific resource",
	}
	describeCmd.AddCommand(NewErrorIncidentCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

// Sample error incident full response
func sampleErrorIncidentFull() errorincidents.ErrorIncidentFull {
	createdAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	jobName := "sensor-1"
	jobType := api.JobType("ingestion_job")
	stackTrace := "Traceback (most recent call last):\n  File \"sensor.py\", line 42\n    raise AccessDeniedError()"
	datasetID := 42
	return errorincidents.ErrorIncidentFull{
		ID:        123,
		Error:     "AccessDeniedError",
		Msg:       "Access denied to resource",
		Module:    "sensor",
		Count:     5,
		JobName:   &jobName,
		JobType:   &jobType,
		CreatedAt: &createdAt,
		Deleted:   false,
		DatasetID: &datasetID,
		StackTrace: &stackTrace,
	}
}

func TestDescribeErrorIncidentCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockErrorIncident     errorincidents.ErrorIncidentFull
		wantErr               bool
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name:              "successful describe with YAML output (default)",
			args:              []string{"123"},
			mockErrorIncident: sampleErrorIncidentFull(),
			wantErr:           false,
			wantOutputContains: []string{
				"apiVersion: qluster.ai/v1",
				"kind: ErrorIncident",
				"error: AccessDeniedError",
				"msg: Access denied to resource",
				"module: sensor",
				"count: 5",
				"job_name: sensor-1",
			},
		},
		{
			name:              "successful describe with JSON output",
			args:              []string{"123", "--output", "json"},
			mockErrorIncident: sampleErrorIncidentFull(),
			wantErr:           false,
			wantOutputContains: []string{
				`"error": "AccessDeniedError"`,
				`"msg": "Access denied to resource"`,
				`"module": "sensor"`,
			},
		},
		{
			name:    "invalid error incident ID",
			args:    []string{"invalid"},
			wantErr: true,
			wantOutputContains: []string{
				"invalid error incident ID",
			},
		},
		{
			name:    "missing error incident ID",
			args:    []string{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			// Setup mock API server
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			// Register handler for single error incident
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents/123", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tt.mockErrorIncident)
			})

			// Setup config and credentials
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			// Create and execute command
			rootCmd := setupErrorIncidentTestCommand()
			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)

			args := append([]string{"describe", "error-incident"}, tt.args...)
			rootCmd.SetArgs(args)

			err := rootCmd.Execute()

			// Check error expectation
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				// Check error message contains expected strings
				for _, want := range tt.wantOutputContains {
					if !strings.Contains(err.Error(), want) && !strings.Contains(stderr.String(), want) {
						t.Errorf("error should contain %q, got error: %v, stderr: %s", want, err, stderr.String())
					}
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			output := stdout.String()

			// Check expected content
			for _, want := range tt.wantOutputContains {
				if !strings.Contains(output, want) {
					t.Errorf("output should contain %q, got:\n%s", want, output)
				}
			}

			// Check content that should NOT be present
			for _, notWant := range tt.wantOutputNotContains {
				if strings.Contains(output, notWant) {
					t.Errorf("output should NOT contain %q, got:\n%s", notWant, output)
				}
			}
		})
	}
}

func TestDescribeErrorIncidentCommand_YAMLManifestFormat(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mockErrorIncident := sampleErrorIncidentFull()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents/123", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, mockErrorIncident)
	})

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupErrorIncidentTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"describe", "error-incident", "123"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse YAML output
	var manifest errorincidents.ErrorIncidentManifest
	if err := yaml.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("output is not valid YAML: %v\nOutput: %s", err, stdout.String())
	}

	// Verify manifest structure
	if manifest.APIVersion != "qluster.ai/v1" {
		t.Errorf("expected apiVersion 'qluster.ai/v1', got %q", manifest.APIVersion)
	}
	if manifest.Kind != "ErrorIncident" {
		t.Errorf("expected kind 'ErrorIncident', got %q", manifest.Kind)
	}
	if manifest.Spec.Error != "AccessDeniedError" {
		t.Errorf("expected spec.error 'AccessDeniedError', got %q", manifest.Spec.Error)
	}
	if manifest.Spec.Module != "sensor" {
		t.Errorf("expected spec.module 'sensor', got %q", manifest.Spec.Module)
	}
	if manifest.Spec.Count != 5 {
		t.Errorf("expected spec.count 5, got %d", manifest.Spec.Count)
	}
	if manifest.Status == nil {
		t.Fatal("expected status to be present")
	}
	if manifest.Status.ID != 123 {
		t.Errorf("expected status.id 123, got %d", manifest.Status.ID)
	}
}

func TestDescribeErrorIncidentCommand_JSONOutput(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mockErrorIncident := sampleErrorIncidentFull()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents/123", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, mockErrorIncident)
	})

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupErrorIncidentTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"describe", "error-incident", "123", "--output", "json"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse JSON output
	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, stdout.String())
	}

	// Verify raw API response format (not manifest)
	if result["error"] != "AccessDeniedError" {
		t.Errorf("expected error 'AccessDeniedError', got %v", result["error"])
	}
	if result["module"] != "sensor" {
		t.Errorf("expected module 'sensor', got %v", result["module"])
	}
}

func TestDescribeErrorIncidentCommand_NotFound(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents/999", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "error incident not found")
	})

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupErrorIncidentTestCommand()
	rootCmd.SetArgs([]string{"describe", "error-incident", "999"})

	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for not found error incident, got nil")
	}
}

func TestDescribeErrorIncidentCommand_NotLoggedIn(t *testing.T) {
	// Setup test environment without credentials
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Force plaintext credential store (empty for this test) to avoid picking up system keyring entries
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Setup config but no credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	// Create and execute command
	rootCmd := setupErrorIncidentTestCommand()
	rootCmd.SetArgs([]string{"describe", "error-incident", "123"})

	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected 'not logged in' error, got: %v", err)
	}
}

func TestApiResponseToErrorIncidentManifest(t *testing.T) {
	createdAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	jobName := "test-job"
	jobType := api.JobType("ingestion_job")
	stackTrace := "test stack trace"
	datasetID := 42

	resp := &errorincidents.ErrorIncidentFull{
		ID:         123,
		Error:      "TestError",
		Msg:        "Test message",
		Module:     "test-module",
		Count:      10,
		JobName:    &jobName,
		JobType:    &jobType,
		CreatedAt:  &createdAt,
		Deleted:    false,
		DatasetID:  &datasetID,
		StackTrace: &stackTrace,
	}

	manifest := apiResponseToErrorIncidentManifest(resp)

	// Verify basic structure
	if manifest.APIVersion != "qluster.ai/v1" {
		t.Errorf("APIVersion = %q, want %q", manifest.APIVersion, "qluster.ai/v1")
	}
	if manifest.Kind != "ErrorIncident" {
		t.Errorf("Kind = %q, want %q", manifest.Kind, "ErrorIncident")
	}

	// Verify spec
	if manifest.Spec.Error != "TestError" {
		t.Errorf("Spec.Error = %q, want %q", manifest.Spec.Error, "TestError")
	}
	if manifest.Spec.Msg != "Test message" {
		t.Errorf("Spec.Msg = %q, want %q", manifest.Spec.Msg, "Test message")
	}
	if manifest.Spec.Module != "test-module" {
		t.Errorf("Spec.Module = %q, want %q", manifest.Spec.Module, "test-module")
	}
	if manifest.Spec.Count != 10 {
		t.Errorf("Spec.Count = %d, want %d", manifest.Spec.Count, 10)
	}
	if manifest.Spec.JobName == nil || *manifest.Spec.JobName != "test-job" {
		t.Errorf("Spec.JobName = %v, want %q", manifest.Spec.JobName, "test-job")
	}
	if manifest.Spec.DatasetID == nil || *manifest.Spec.DatasetID != 42 {
		t.Errorf("Spec.DatasetID = %v, want %d", manifest.Spec.DatasetID, 42)
	}

	// Verify status
	if manifest.Status == nil {
		t.Fatal("Status should not be nil")
	}
	if manifest.Status.ID != 123 {
		t.Errorf("Status.ID = %d, want %d", manifest.Status.ID, 123)
	}
	if manifest.Status.Deleted != false {
		t.Errorf("Status.Deleted = %v, want %v", manifest.Status.Deleted, false)
	}
	if manifest.Status.CreatedAt == nil || *manifest.Status.CreatedAt != "2024-01-01T00:00:00Z" {
		t.Errorf("Status.CreatedAt = %v, want %q", manifest.Status.CreatedAt, "2024-01-01T00:00:00Z")
	}
}
