package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// setupTestCommand creates a root command with global flags and adds the describe command
func setupTestCommand() *cobra.Command {
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
		Short: "Show details of a resource",
	}
	describeCmd.AddCommand(NewDatasetCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

// Helper to create sample dataset fixture
func sampleDatasetFull() datasets.DatasetFull {
	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("123e4567-e89b-12d3-a456-426614174000"))

	encryptBackup := true
	quarantine := false
	removeOutliers := false
	shouldReprocess := false
	detectAnomalies := false
	enableCellMove := false
	strictDatetime := false
	guessDatetime := false
	enableRowLogs := false
	cleanRows := 120
	badRows := 5
	columnsForEntityResolution := []string{"user_id"}

	return datasets.DatasetFull{
		ID:                          1,
		VersionID:                   10,
		Name:                        "user_data",
		SchemaName:                  "public",
		DatabaseName:                "analytics",
		TableName:                   "users",
		MigrationPolicy:             api.ApplyAsap,
		State:                       api.DataSetStateActive,
		OrganizationID:              orgID,
		EncryptRawDataDuringBackup:  &encryptBackup,
		QuarantineRowsUntilApproved: &quarantine,
		RemoveOutliersWhenRecommendingNumericValidators: &removeOutliers,
		ShouldReprocess:                     &shouldReprocess,
		DetectAnomalies:                     &detectAnomalies,
		EnableCellMoveSuggestions:           &enableCellMove,
		StrictlyOneDatetimeFormatInAColumn:  &strictDatetime,
		GuessDatetimeFormatInIngestion:      &guessDatetime,
		EnableRowLogs:                       &enableRowLogs,
		AnomalyThreshold:                    50,
		MaxRetryCount:                       3,
		MaxTriesToFixJSON:                   3,
		BackupKeyFormat:                     "{dataset_id}/{data_source_id}/{datetime}",
		BackupSettingsID:                    1,
		DestinationID:                       1,
		DestinationName:                     "postgres_main",
		DataLoadingProcess:                  api.Snapshot,
		CleanRowsCount:             &cleanRows,
		BadRowsCount:              &badRows,
		ColumnsForEntityResolution: &columnsForEntityResolution,
		SettingsModel:              api.SettingsSchema{},
		BackupSettingsList:                  []api.OptionTypeSchema{},
		DestinationsList:                    []api.OptionTypeSchema{},
	}
}

func TestDescribeDatasetCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockDataset        datasets.DatasetFull
		mockStatusCode     int
		wantErr            bool
		wantOutputContains []string
	}{
		{
			name:           "default YAML manifest",
			args:           []string{"1"},
			mockDataset:    sampleDatasetFull(),
			mockStatusCode: http.StatusOK,
			wantErr:        false,
			wantOutputContains: []string{
				"apiVersion: qluster.ai/v1",
				"kind: Table",
				"name: user_data",
				"destination_id: 1",
				"schema_name: public",
				"state: active",
			},
		},
		{
			name:           "json manifest output",
			args:           []string{"1", "--output", "json"},
			mockDataset:    sampleDatasetFull(),
			mockStatusCode: http.StatusOK,
			wantErr:        false,
			wantOutputContains: []string{
				`"apiVersion": "qluster.ai/v1"`,
				`"kind": "Table"`,
				`"destination_id": 1`,
				`"schema_name": "public"`,
				`"destination_name": "postgres_main"`,
			},
		},
		{
			name:    "no arguments",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "too many arguments",
			args:    []string{"1", "extra"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip server setup for argument count tests
			if strings.Contains(tt.name, "arguments") {
				cmd := setupTestCommand()
				args := append([]string{"describe", "table"}, tt.args...)
				cmd.SetArgs(args)

				var buf bytes.Buffer
				cmd.SetOut(&buf)
				cmd.SetErr(&buf)

				err := cmd.Execute()

				if (err != nil) != tt.wantErr {
					t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			// Setup test environment
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			// Create mock API server
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			// Setup config and credentials
			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			// Register mock handler for dataset detail
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatusCode != http.StatusOK {
					testutil.RespondError(w, tt.mockStatusCode, "Error")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, tt.mockDataset)
			})

			// Create command with proper hierarchy
			cmd := setupTestCommand()

			// Prepare args with "describe table" prefix
			args := append([]string{"describe", "table"}, tt.args...)

			// Capture output
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)

			// Execute command
			err := cmd.Execute()

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				output := buf.String()

				// Check output contains expected strings
				for _, want := range tt.wantOutputContains {
					if !strings.Contains(output, want) {
						t.Errorf("Output should contain %q, got: %s", want, output)
					}
				}
			}
		})
	}
}

func TestDescribeDatasetCommand_YAMLManifestStructure(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mockDataset := sampleDatasetFull()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, mockDataset)
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"describe", "table", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var manifest datasets.TableManifestWithStatus
	if err := yaml.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("output is not valid YAML: %v\nOutput: %s", err, stdout.String())
	}

	if manifest.APIVersion != pkgmanifest.APIVersionV1 {
		t.Errorf("expected apiVersion %q, got %q", pkgmanifest.APIVersionV1, manifest.APIVersion)
	}
	if manifest.Kind != "Table" {
		t.Errorf("expected kind Table, got %s", manifest.Kind)
	}
	if manifest.Metadata.Name != mockDataset.Name {
		t.Errorf("expected metadata.name %q, got %q", mockDataset.Name, manifest.Metadata.Name)
	}
	if manifest.Spec.DestinationID != mockDataset.DestinationID {
		t.Errorf("expected destination_id %d, got %d", mockDataset.DestinationID, manifest.Spec.DestinationID)
	}
	if manifest.Spec.TableName != mockDataset.TableName {
		t.Errorf("expected table_name %q, got %q", mockDataset.TableName, manifest.Spec.TableName)
	}
	if manifest.Spec.AnomalyThreshold == nil || *manifest.Spec.AnomalyThreshold != mockDataset.AnomalyThreshold {
		t.Errorf("expected anomaly_threshold %d, got %v", mockDataset.AnomalyThreshold, manifest.Spec.AnomalyThreshold)
	}
	if manifest.Status == nil {
		t.Fatal("expected status to be present")
	}
	if manifest.Status.ID != mockDataset.ID {
		t.Errorf("expected status.id %d, got %d", mockDataset.ID, manifest.Status.ID)
	}
	if manifest.Status.State != mockDataset.State {
		t.Errorf("expected status.state %s, got %s", mockDataset.State, manifest.Status.State)
	}
	if manifest.Status.OrganizationID != mockDataset.OrganizationID.String() {
		t.Errorf("expected organization_id %s, got %s", mockDataset.OrganizationID.String(), manifest.Status.OrganizationID)
	}
	if manifest.Status.CleanRowsCount == nil || *manifest.Status.CleanRowsCount != *mockDataset.CleanRowsCount {
		t.Errorf("expected clean_rows_count %d, got %v", *mockDataset.CleanRowsCount, manifest.Status.CleanRowsCount)
	}
}

func TestDescribeDatasetCommand_JSONManifestStructure(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mockDataset := sampleDatasetFull()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, mockDataset)
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"describe", "table", "1", "--output", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var manifest datasets.TableManifestWithStatus
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, stdout.String())
	}

	if manifest.Spec.SchemaName != mockDataset.SchemaName {
		t.Errorf("expected schema_name %q, got %q", mockDataset.SchemaName, manifest.Spec.SchemaName)
	}
	if manifest.Spec.DataLoadingProcess != mockDataset.DataLoadingProcess {
		t.Errorf("expected data_loading_process %s, got %s", mockDataset.DataLoadingProcess, manifest.Spec.DataLoadingProcess)
	}
	if manifest.Status == nil || manifest.Status.DestinationName != mockDataset.DestinationName {
		t.Errorf("expected destination_name %q in status, got %+v", mockDataset.DestinationName, manifest.Status)
	}
}

func TestDescribeDatasetCommand_NotLoggedIn(t *testing.T) {
	// Test that command fails when not logged in
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1") // avoid picking up real keyring creds in tests

	// Setup config but NO credentials
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"describe", "table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when not logged in")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("Expected 'not logged in' error, got: %v", err)
	}
}

func TestDescribeDatasetCommand_InvalidConfig(t *testing.T) {
	// Test that command fails when config is invalid
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Don't setup any config file

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"describe", "table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when config is missing")
	}

	// The error message can vary - just check that there's an error about config/context/server
	if !strings.Contains(err.Error(), "config") && !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "server") {
		t.Errorf("Expected config-related error, got: %v", err)
	}
}

func TestDescribeDatasetCommand_DatasetNotFound(t *testing.T) {
	// Test that command handles 404 not found
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 404
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/999", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Dataset not found")
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"describe", "table", "999"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when table not found")
	}

	if !strings.Contains(err.Error(), "failed to get table") {
		t.Errorf("Expected 'failed to get table' error, got: %v", err)
	}
}

func TestDescribeDatasetCommand_ServerUnauthorized(t *testing.T) {
	// Test that command handles unauthorized errors from server
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 401
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusUnauthorized, "Unauthorized")
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"describe", "table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 401")
	}

	if !strings.Contains(err.Error(), "failed to get table") {
		t.Errorf("Expected 'failed to get table' error, got: %v", err)
	}
}

func TestDescribeDatasetCommand_ServerError(t *testing.T) {
	// Test that command handles server errors
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 500
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"describe", "table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestDescribeDatasetCommand_LookupByName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mockDataset := sampleDatasetFull()

	// Register list endpoint (name lookup) — returns a match
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		nameFilter := r.URL.Query().Get("name")
		if nameFilter != "user_data" {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results":    []interface{}{},
				"total_rows": 0,
				"page":       1,
			})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []datasets.DatasetTiny{
				{ID: 1, Name: "user_data", DestinationName: "postgres_main"},
			},
			"total_rows": 1,
			"page":       1,
		})
	})

	// Register detail endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, mockDataset)
	})

	cmd := setupTestCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"describe", "table", "user_data"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "name: user_data") {
		t.Errorf("Expected output to contain table name, got: %s", output)
	}
	if !strings.Contains(output, "destination_id: 1") {
		t.Errorf("Expected output to contain destination_id, got: %s", output)
	}
}

func TestDescribeDatasetCommand_LookupByNameNotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register list endpoint — returns empty results
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results":    []interface{}{},
			"total_rows": 0,
			"page":       1,
		})
	})

	cmd := setupTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"describe", "table", "nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-existent table name")
	}

	if !strings.Contains(err.Error(), "no table") {
		t.Errorf("expected 'no table' in error, got: %v", err)
	}
}

func TestDescribeDatasetCommand_LookupByNameWithSpaces(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mockDataset := sampleDatasetFull()
	mockDataset.Name = "My User Data"

	// Register list endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []datasets.DatasetTiny{
				{ID: 1, Name: "My User Data", DestinationName: "postgres_main"},
			},
			"total_rows": 1,
			"page":       1,
		})
	})

	// Register detail endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, mockDataset)
	})

	cmd := setupTestCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"describe", "table", "My User Data"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "name: My User Data") {
		t.Errorf("Expected output to contain table name with spaces, got: %s", output)
	}
}

func TestDescribeDatasetCommand_VerboseFlag(t *testing.T) {
	// Test that verbose flag works
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFull())
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"describe", "table", "1", "--verbose"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err != nil {
		t.Errorf("Execute() error = %v", err)
	}

	// Verbose flag should not cause any errors
	output := buf.String()
	if !strings.Contains(output, "user_data") {
		t.Error("Expected table name in output")
	}
}
