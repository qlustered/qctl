package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupAlertTestCommand creates a root command with global flags and adds the alert command
func setupAlertTestCommand() *cobra.Command {
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
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	// Add describe command
	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a resource",
	}
	describeCmd.AddCommand(NewAlertCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

// Helper to create sample alert fixture
func sampleAlertFull() alerts.AlertFull {
	createdAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	dataSourceID := 5
	dataSourceName := "customer_data"
	redirectURL := "/datasets/1/alerts/1"
	fieldName := "email"
	fieldValue := "invalid@"
	settingsID := 100
	stackTrace := "Error at line 42..."
	resolvableByUser := true
	resolveAfterMigration := false
	resolved := false
	blocksProfiling := false
	blocksIngestionForDataset := true
	blocksIngestionForDataSource := false
	blocksStoredItem := false
	ingestionJobIDs := []int{100, 101}

	return alerts.AlertFull{
		ID:                           1,
		DatasetName:                  "user_data",
		DatasetID:                    10,
		DataSourceModelID:            &dataSourceID,
		DataSourceModelName:          &dataSourceName,
		IssueType:                    "MISSING_COLUMN",
		Count:                        3,
		Msg:                          "Column 'email' is missing from 3 files",
		CreatedAt:                    &createdAt,
		ResolvedAt:                   nil,
		ResolvableByUser:             &resolvableByUser,
		ResolveAfterMigration:        &resolveAfterMigration,
		RedirectURL:                  &redirectURL,
		Whodunit:                     nil,
		AssignedUser:                 nil,
		SettingsModelID:              settingsID,
		IsRowLevel:                   false,
		FieldName:                    &fieldName,
		FieldValue:                   &fieldValue,
		Resolved:                     &resolved,
		BlocksProfiling:              &blocksProfiling,
		BlocksIngestionForDataset:    &blocksIngestionForDataset,
		BlocksIngestionForDataSource: &blocksIngestionForDataSource,
		BlocksStoredItem:             &blocksStoredItem,
		StackTrace:                   &stackTrace,
		IngestionJobIds:              &ingestionJobIDs,
	}
}

// sampleAlertFullWithBody returns a sample alert with body data for testing -v output
func sampleAlertFullWithBody() alerts.AlertFull {
	alert := sampleAlertFull()
	fileName := "data.csv"
	anomalyScore := 85
	strong := true
	alert.Body = &api.AlertBodySchema{
		FileName:     &fileName,
		AnomalyScore: &anomalyScore,
		TopSuggestedValues: &[]api.ValueRecommendationTinySchema{
			{Value: "email", Strong: &strong},
		},
		AllowedValues: &[]string{"email", "name", "phone"},
	}
	return alert
}

func TestDescribeAlertCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockAlert          alerts.AlertFull
		mockStatusCode     int
		wantErr            bool
		wantOutputContains []string
	}{
		{
			name:               "successful describe with ID",
			args:               []string{"1"},
			mockAlert:          sampleAlertFull(),
			mockStatusCode:     http.StatusOK,
			wantErr:            false,
			wantOutputContains: []string{"user_data", "MISSING_COLUMN", "email"},
		},
		{
			name:               "successful describe with just id",
			args:               []string{"1"},
			mockAlert:          sampleAlertFull(),
			mockStatusCode:     http.StatusOK,
			wantErr:            false,
			wantOutputContains: []string{"user_data", "MISSING_COLUMN"},
		},
		{
			name:               "with json output",
			args:               []string{"1", "--output", "json"},
			mockAlert:          sampleAlertFull(),
			mockStatusCode:     http.StatusOK,
			wantErr:            false,
			wantOutputContains: []string{`"dataset_name": "user_data"`, `"issue_type": "MISSING_COLUMN"`, `"message":`},
		},
		{
			name:               "with yaml output",
			args:               []string{"1", "--output", "yaml"},
			mockAlert:          sampleAlertFull(),
			mockStatusCode:     http.StatusOK,
			wantErr:            false,
			wantOutputContains: []string{"dataset_name: user_data", "issue_type: MISSING_COLUMN"},
		},
		{
			name:    "invalid alert ID format",
			args:    []string{"invalid"},
			wantErr: true,
		},
		{
			name:    "invalid ID format",
			args:    []string{"abc"},
			wantErr: true,
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
			// Skip server setup for invalid argument tests
			if strings.Contains(tt.name, "invalid") || strings.Contains(tt.name, "arguments") {
				cmd := setupAlertTestCommand()
				args := append([]string{"describe", "alert"}, tt.args...)
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

			// Register mock handler for alert detail
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatusCode != http.StatusOK {
					testutil.RespondError(w, tt.mockStatusCode, "Error")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, tt.mockAlert)
			})

			// Create command with proper hierarchy
			cmd := setupAlertTestCommand()

			// Prepare args with "describe alert" prefix
			args := append([]string{"describe", "alert"}, tt.args...)

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

func TestDescribeAlertCommand_DefaultOmitsNicheFields(t *testing.T) {
	// Default verbosity (0) should omit debugging/niche fields
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleAlertFull())
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Essential fields SHOULD be present
	essentialFields := []string{
		"issue_type: MISSING_COLUMN",
		"dataset_id: 10",
		"dataset_name: user_data",
		"field_name: email",
		"blocks_ingestion_for_dataset: true",
	}
	for _, field := range essentialFields {
		if !strings.Contains(output, field) {
			t.Errorf("Default output should contain essential field %q, got:\n%s", field, output)
		}
	}

	// Niche/debugging fields should NOT be present in default output
	omittedFields := []string{
		"publisher:",
		"stack_trace:",
		"settings_model_id:",
		"redirect_url:",
		"resolvable_by_user:",
		"resolve_after_migration:",
		"ingestion_job_ids:",
		"another_field_name:",
		"another_field_value:",
	}
	for _, field := range omittedFields {
		if strings.Contains(output, field) {
			t.Errorf("Default output should NOT contain %q, got:\n%s", field, output)
		}
	}

	// False booleans with omitempty should not appear
	if strings.Contains(output, "blocks_profiling:") {
		t.Errorf("Default output should omit blocks_profiling when false, got:\n%s", output)
	}
	if strings.Contains(output, "is_row_level:") {
		t.Errorf("Default output should omit is_row_level when false, got:\n%s", output)
	}
}

func TestDescribeAlertCommand_VerboseShowsFullDetails(t *testing.T) {
	// -v should show Tier 2 fields including publisher, body, etc.
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleAlertFullWithBody())
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1", "-v"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Tier 2 fields should now appear
	verboseFields := []string{
		"stack_trace:",
		"redirect_url:",
		"resolvable_by_user:",
		"ingestion_job_ids:",
	}
	for _, field := range verboseFields {
		if !strings.Contains(output, field) {
			t.Errorf("-v output should contain %q, got:\n%s", field, output)
		}
	}

	// Body highlights should appear
	bodyFields := []string{
		"body:",
		"file_name: data.csv",
		"anomaly_score: 85",
		"top_suggested_values:",
		"value: email",
		"allowed_values:",
	}
	for _, field := range bodyFields {
		if !strings.Contains(output, field) {
			t.Errorf("-v output should contain body field %q, got:\n%s", field, output)
		}
	}
}

func TestDescribeAlertCommand_VeryVerboseShowsRawDump(t *testing.T) {
	// -vv should output raw API response
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleAlertFull())
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1", "-vv"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Raw dump should contain raw_response field
	if !strings.Contains(output, "raw_response") {
		t.Errorf("-vv output should contain raw_response, got:\n%s", output)
	}

	// Should still have manifest wrapper
	if !strings.Contains(output, "apiVersion") {
		t.Errorf("-vv output should contain apiVersion, got:\n%s", output)
	}

	// settings_model_id should appear in the raw response
	if !strings.Contains(output, "settings_model_id") {
		t.Errorf("-vv output should contain settings_model_id in raw response, got:\n%s", output)
	}
}

func TestDescribeAlertCommand_DefaultOmitsEmptyBody(t *testing.T) {
	// When body is nil, body section should not appear even with -v
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Use alert without body
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleAlertFull())
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1", "-v"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// body section should not appear when alert has no body
	if strings.Contains(output, "body:") {
		t.Errorf("Output should not contain body: when alert has no body, got:\n%s", output)
	}
}

func TestDescribeAlertCommand_RelativeTimestamps(t *testing.T) {
	// Timestamps should be human-readable relative format
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleAlertFull())
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Should contain relative time format (e.g., "X months ago" or "X years ago")
	// rather than RFC3339 format
	if strings.Contains(output, "2025-01-15T10:00:00Z") {
		t.Errorf("created_at should use relative format, not RFC3339, got:\n%s", output)
	}
	if !strings.Contains(output, "ago") {
		t.Errorf("created_at should contain 'ago' (relative format), got:\n%s", output)
	}
}

func TestDescribeAlertCommand_NotLoggedIn(t *testing.T) {
	// Test that command fails when not logged in
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1") // avoid picking up real keyring creds in tests

	// Setup config but NO credentials
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1"})

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

func TestDescribeAlertCommand_InvalidConfig(t *testing.T) {
	// Test that command fails when config is invalid
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Don't setup any config file

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1"})

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

func TestDescribeAlertCommand_AlertNotFound(t *testing.T) {
	// Test that command handles 404 not found
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 404
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/999", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Alert not found")
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "999"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when alert not found")
	}

	if !strings.Contains(err.Error(), "failed to get alert") {
		t.Errorf("Expected 'failed to get alert' error, got: %v", err)
	}
}

func TestDescribeAlertCommand_ServerUnauthorized(t *testing.T) {
	// Test that command handles unauthorized errors from server
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 401
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusUnauthorized, "Unauthorized")
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 401")
	}

	if !strings.Contains(err.Error(), "failed to get alert") {
		t.Errorf("Expected 'failed to get alert' error, got: %v", err)
	}
}

func TestDescribeAlertCommand_ServerError(t *testing.T) {
	// Test that command handles server errors
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 500
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestDescribeAlertCommand_VerboseFlag(t *testing.T) {
	// Test that verbose flag works
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleAlertFull())
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1", "-v"})

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
		t.Error("Expected dataset name in output")
	}
}
