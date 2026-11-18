package describe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
	// resolve_after_migration is Tier 1 when true; the fixture leaves it false,
	// so omitempty must drop it.
	if strings.Contains(output, "resolve_after_migration:") {
		t.Errorf("Default output should omit resolve_after_migration when false, got:\n%s", output)
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

func TestDescribeAlertCommand_StatusTimesAreRFC3339InUTC(t *testing.T) {
	// The fixture's times carry a +02:00 offset, so a builder that drops the
	// UTC conversion writes "12:00:00+02:00" and fails as surely as one that
	// writes "x ago".
	zone := time.FixedZone("UTC+2", 2*60*60)
	createdAt := time.Date(2025, 1, 15, 12, 0, 0, 0, zone)
	resolvedAt := time.Date(2025, 1, 16, 12, 30, 0, 0, zone)
	expiredAt := time.Date(2025, 2, 1, 2, 0, 0, 0, zone)
	alert := sampleAlertFull()
	alert.CreatedAt = &createdAt
	alert.ResolvedAt = &resolvedAt
	alert.ExpiredAt = &expiredAt

	want := map[string]string{
		"created_at":  "2025-01-15T10:00:00Z",
		"resolved_at": "2025-01-16T10:30:00Z",
		"expired_at":  "2025-02-01T00:00:00Z",
	}

	for _, tc := range []struct {
		decode func([]byte, any) error
		name   string
		format []string
	}{
		{name: "default yaml", decode: yaml.Unmarshal},
		{name: "yaml", format: []string{"-o", "yaml"}, decode: yaml.Unmarshal},
		{name: "json", format: []string{"-o", "json"}, decode: json.Unmarshal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, alert)
			})

			cmd := setupAlertTestCommand()
			// -v: expired_at is a Tier 2 field.
			cmd.SetArgs(append([]string{"describe", "alert", "1", "-v"}, tc.format...))

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			var doc struct {
				Status map[string]any `json:"status" yaml:"status"`
			}
			if err := tc.decode(buf.Bytes(), &doc); err != nil {
				t.Fatalf("output does not parse: %v\n%s", err, buf.String())
			}
			for key, value := range want {
				if got := doc.Status[key]; got != value {
					t.Errorf("status.%s = %#v, want %q\n%s", key, got, value, buf.String())
				}
			}
			if strings.Contains(buf.String(), " ago") {
				t.Errorf("output carries a relative time:\n%s", buf.String())
			}
		})
	}
}

func TestDescribeAlertCommand_NotLoggedIn(t *testing.T) {
	// Test that command fails when not logged in
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

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

// sampleAlertFullWithActions is sampleAlertFull plus an offered action list,
// so the next-actions footer has something to render.
func sampleAlertFullWithActions() alerts.AlertFull {
	a := sampleAlertFull()
	actions := []api.AlertAction{api.AlertActionApplyMigration, api.AlertActionDiscardMigration}
	a.AlertActionsList = &actions
	return a
}

func TestDescribeAlertCommand_YAMLFooterListsResolveCommands(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleAlertFullWithActions())
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := buf.String()

	for _, want := range []string{
		"# NEXT ACTIONS",
		"qctl alert resolve 1 --action apply_migration",
		"qctl alert resolve 1 --action discard_migration",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output should contain %q, got:\n%s", want, output)
		}
	}

	// The footer is comments, so the document must still parse as YAML.
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("footer broke YAML parsing: %v\n%s", err, output)
	}
	if parsed["kind"] != "Alert" {
		t.Errorf("kind = %v, want Alert", parsed["kind"])
	}
}

// An alert offering nothing gets no footer — an empty NEXT ACTIONS block is
// worse than none.
func TestDescribeAlertCommand_NoFooterWithoutActions(t *testing.T) {
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

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(buf.String(), "NEXT ACTIONS") {
		t.Errorf("alert with no actions must get no footer, got:\n%s", buf.String())
	}
}

// The rows trailer is the only footer line that reads a field the manifest
// path does not: is_row_level has to survive the JSON decode into
// alerts.AlertFull for the line to print at all. The unit tests in
// internal/alerts build that struct directly and cannot see the wire.
func TestDescribeAlertCommand_YAMLFooterPointsAtARowLevelAlertsRows(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Built as a wire map rather than a struct so the key name is asserted
	// literally: a regen that renames it would otherwise leave this test green
	// while the footer went silent for every row-level alert.
	body, err := json.Marshal(sampleAlertFullWithActions())
	if err != nil {
		t.Fatalf("marshalling the fixture: %v", err)
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshalling the fixture: %v", err)
	}
	if _, ok := wire["is_row_level"]; !ok {
		t.Fatal("the alert schema no longer carries is_row_level; the rows footer reads it")
	}
	wire["is_row_level"] = true
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, wire)
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1/stats", respondAffectingRows(1234))

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := buf.String()

	// Dataset id 10, alert id 1 — different numbers, so a swap cannot pass.
	if !strings.Contains(output, "qctl get data 10 --is-bad-data --alert-item-id 1") {
		t.Errorf("output should route to the alert's rows, got:\n%s", output)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("rows line broke YAML parsing: %v\n%s", err, output)
	}
}

// The same response with is_row_level false — a file-level alert holds no
// quarantine rows, so the filter would match nothing.
func TestDescribeAlertCommand_NoRowsLineForAFileLevelAlert(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleAlertFullWithActions())
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs([]string{"describe", "alert", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(buf.String(), "--alert-item-id") {
		t.Errorf("file-level alert must get no rows line, got:\n%s", buf.String())
	}
}

// runDescribeAlert runs `describe alert` against a mock returning one alert and
// returns everything it wrote. The table path appends the footer to the
// printer's own stream, so both have to be captured together.
func runDescribeAlert(t *testing.T, alert alerts.AlertFull, args ...string) string {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, alert)
	})

	cmd := setupAlertTestCommand()
	cmd.SetArgs(args)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, buf.String())
	}
	return buf.String()
}

func TestDescribeAlertShowsRelatedIDsInEverySelectedView(t *testing.T) {
	ids := []int{7, 9}
	a := sampleAlertFull()
	a.Body = &api.AlertBodySchema{RelatedAlertIds: &ids}

	for name, args := range map[string][]string{
		"default yaml": {"describe", "alert", "1"},
		"verbose yaml": {"describe", "alert", "1", "-v"},
		"json":         {"describe", "alert", "1", "-o", "json"},
		"table":        {"describe", "alert", "1", "-o", "table", "--max-column-width", "2000"},
	} {
		t.Run(name, func(t *testing.T) {
			out := runDescribeAlert(t, a, args...)
			for _, want := range []string{"related_alert_ids", "7", "9"} {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}

// prefixCandidateViews are the views `describe alert` selects between below
// -vv, keyed by how prefixCandidatesIn reads each one.
var prefixCandidateViews = []struct {
	name   string
	format string
	args   []string
}{
	{"default yaml", "yaml", []string{"describe", "alert", "1"}},
	{"verbose yaml", "yaml", []string{"describe", "alert", "1", "-v"}},
	{"json", "json", []string{"describe", "alert", "1", "-o", "json"}},
	{"table", "table", []string{"describe", "alert", "1", "-o", "table", "--max-column-width", "2000"}},
}

// prefixCandidatesIn decodes spec.body.prefix_candidates from one view's
// output. The table view prints the spec as a JSON cell, so the array is
// decoded from where its key starts.
func prefixCandidatesIn(t *testing.T, format, out string) []any {
	t.Helper()
	if format == "table" {
		const key = `"prefix_candidates":`
		i := strings.Index(out, key)
		if i < 0 {
			return nil
		}
		var got []any
		if err := json.NewDecoder(strings.NewReader(out[i+len(key):])).Decode(&got); err != nil {
			t.Fatalf("table cell's prefix_candidates is not a JSON array: %v\n%s", err, out)
		}
		return got
	}
	var doc map[string]any
	var err error
	if format == "json" {
		err = json.Unmarshal([]byte(out), &doc)
	} else {
		err = yaml.Unmarshal([]byte(out), &doc)
	}
	if err != nil {
		t.Fatalf("output is not valid %s: %v\n%s", format, err, out)
	}
	spec, _ := doc["spec"].(map[string]any)
	body, _ := spec["body"].(map[string]any)
	got, _ := body["prefix_candidates"].([]any)
	return got
}

// The candidates are the only values accept_gtin_prefix's new_value takes, so
// every view has to show them as the backend sent them. The fixture is out of
// lexical order and zero-led: a sorted rendering fails the comparison, and so
// does an unquoted YAML one, which decodes as an octal int.
func TestDescribeAlertShowsPrefixCandidatesInEverySelectedView(t *testing.T) {
	candidates := []string{"061414100", "06141410", "0614141"}
	a := sampleAlertFull()
	a.IssueType = api.AlertTypeUpcUnknownGtinPrefix
	a.Body = &api.AlertBodySchema{PrefixCandidates: &candidates}
	want := []any{"061414100", "06141410", "0614141"}

	for _, view := range prefixCandidateViews {
		t.Run(view.name, func(t *testing.T) {
			out := runDescribeAlert(t, a, view.args...)
			got := prefixCandidatesIn(t, view.format, out)
			if len(got) != len(want) {
				t.Fatalf("spec.body.prefix_candidates = %#v, want %#v:\n%s", got, want, out)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("spec.body.prefix_candidates[%d] = %#v (%T), want %#v:\n%s", i, got[i], got[i], want[i], out)
				}
			}
		})
	}
}

func TestDescribeAlertOmitsPrefixCandidatesWhenTheBodyHasNone(t *testing.T) {
	relatedIDs := []int{7}
	for name, body := range map[string]*api.AlertBodySchema{
		"no body":               nil,
		"body without the key":  {RelatedAlertIds: &relatedIDs},
		"empty candidates list": {PrefixCandidates: &[]string{}},
	} {
		a := sampleAlertFull()
		a.IssueType = api.AlertTypeUpcUnknownGtinPrefix
		a.Body = body
		for _, view := range prefixCandidateViews {
			t.Run(name+"/"+view.name, func(t *testing.T) {
				out := runDescribeAlert(t, a, view.args...)
				if !strings.Contains(out, "upc_unknown_gtin_prefix") {
					t.Fatalf("output does not render the alert, so its silence proves nothing:\n%s", out)
				}
				if strings.Contains(out, "prefix_candidates") {
					t.Errorf("output prints prefix_candidates for a body without any:\n%s", out)
				}
			})
		}
	}
}

// The defect this fixes: `-o table` returned the printer's output and never
// reached the footer, so a reader on the table view saw none of the runnable
// commands the command had already computed.
func TestDescribeAlertTableCarriesTheFooter(t *testing.T) {
	out := runDescribeAlert(t, sampleAlertFullWithActions(), "describe", "alert", "1", "-o", "table")

	for _, want := range []string{
		"NEXT ACTIONS",
		"qctl alert resolve 1 --action apply_migration",
		"qctl alert resolve 1 --action discard_migration",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output should contain %q, got:\n%s", want, out)
		}
	}
}

// apply_migration prompts, so the footer's line for it carries --yes exactly
// when describe runs where no prompt can be answered -- on both views that
// print the footer. discard_migration does not prompt and never gets it.
func TestDescribeAlertFooterAddsYesOnlyWithoutATerminal(t *testing.T) {
	const migration = "qctl alert resolve 1 --action apply_migration"
	for view, args := range map[string][]string{
		"yaml":  {"describe", "alert", "1"},
		"table": {"describe", "alert", "1", "-o", "table"},
	} {
		t.Run(view+"/at a terminal", func(t *testing.T) {
			cmdutil.SetReader(strings.NewReader(""))
			t.Cleanup(cmdutil.ResetReader)
			out := runDescribeAlert(t, sampleAlertFullWithActions(), args...)
			if !strings.Contains(out, migration+"\n") {
				t.Errorf("a terminal reader's line must not carry --yes, want %q in:\n%s", migration+"\n", out)
			}
		})
		t.Run(view+"/no terminal", func(t *testing.T) {
			cmdutil.ResetReader()
			t.Cleanup(cmdutil.ResetReader)
			if cmdutil.CanPrompt() {
				t.Skip("stdin is a terminal; CanPrompt() cannot be made false here")
			}
			out := runDescribeAlert(t, sampleAlertFullWithActions(), args...)
			if !strings.Contains(out, migration+" --yes\n") {
				t.Errorf("want %q in:\n%s", migration+" --yes\n", out)
			}
			if !strings.Contains(out, "qctl alert resolve 1 --action discard_migration\n") {
				t.Errorf("discard_migration does not prompt and must not carry --yes:\n%s", out)
			}
		})
	}
}

// A table is not a YAML document, so the marker that makes the footer legal
// there is noise here. Asserting its absence is what stops the plain form
// being quietly swapped back for the commented one.
func TestDescribeAlertTableFooterIsNotCommented(t *testing.T) {
	out := runDescribeAlert(t, sampleAlertFullWithActions(), "describe", "alert", "1", "-o", "table")

	if !strings.Contains(out, "NEXT ACTIONS") {
		t.Fatalf("no footer to check, got:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("table output carries a YAML comment marker: %q\n%s", line, out)
		}
	}
}

// -vv is strictly more than the default view, and it is still YAML, so it
// keeps the footer in its commented form -- and the dump must still parse.
func TestDescribeAlertRawDumpCarriesTheCommentedFooter(t *testing.T) {
	out := runDescribeAlert(t, sampleAlertFullWithActions(), "describe", "alert", "1", "-vv")

	if !strings.Contains(out, "# NEXT ACTIONS") {
		t.Errorf("-vv should carry the commented footer, got:\n%s", out)
	}
	if !strings.Contains(out, "#   qctl alert resolve 1 --action apply_migration") {
		t.Errorf("-vv footer should list the resolve commands, got:\n%s", out)
	}
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("footer broke the raw dump's YAML parsing: %v\n%s", err, out)
	}
	if len(parsed) == 0 {
		t.Errorf("raw dump parsed to nothing, so the assertion above proves little:\n%s", out)
	}
}

// -vv changes the payload tier, not the selected renderer. A reader asking for
// a table must still get the raw manifest as a table, with the footer in the
// same plain form as the lower-verbosity table view.
func TestDescribeAlertRawDumpHonorsTableFormat(t *testing.T) {
	out := runDescribeAlert(t, sampleAlertFullWithActions(), "describe", "alert", "1", "-o", "table", "-vv")

	for _, want := range []string{
		"RAW-RESPONSE",
		"NEXT ACTIONS",
		"qctl alert resolve 1 --action apply_migration",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("-o table -vv output should contain %q, got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"raw_response:", "# NEXT ACTIONS"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("-o table -vv rendered YAML marker %q, got:\n%s", unwanted, out)
		}
	}
}

// -o json is the ONE view with no footer, at every verbosity: it has no
// comment syntax, and the raw response is the machine view. Both arms matter
// -- the two json paths are different branches of the command.
func TestDescribeAlertJSONNeverCarriesTheFooter(t *testing.T) {
	for name, args := range map[string][]string{
		"default":  {"describe", "alert", "1", "-o", "json"},
		"raw dump": {"describe", "alert", "1", "-o", "json", "-vv"},
	} {
		t.Run(name, func(t *testing.T) {
			out := runDescribeAlert(t, sampleAlertFullWithActions(), args...)
			if strings.Contains(out, "NEXT ACTIONS") {
				t.Errorf("-o json must not carry the footer, got:\n%s", out)
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(out), &parsed); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, out)
			}
		})
	}
}

// An alert that offers nothing gets no footer on the table view either -- the
// table path appends the block unconditionally, so an empty case leaking a
// bare heading would print one under every alert.
func TestDescribeAlertTableHasNoFooterWithoutActions(t *testing.T) {
	out := runDescribeAlert(t, sampleAlertFull(), "describe", "alert", "1", "-o", "table")
	if strings.Contains(out, "NEXT ACTIONS") {
		t.Errorf("an alert with no actions must get no footer, got:\n%s", out)
	}
}

// The reason the table view's silence mattered most: a context with
// `output: table` never types a flag. cmdutil.ApplyContextOutputFormat calls
// Flags().Set, which marks the flag Changed, so a configured default reaches
// describe as an explicit `-o table` would. Asserted rather than reasoned
// about, because the whole severity of the bug rests on it.
func TestDescribeAlertRawDumpHonorsAContextConfiguredTableView(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupMultipleContexts(map[string]*config.Context{
		"default": {Server: mock.Server.URL, Organization: testOrgID, Output: "table"},
	}, "default")
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleAlertFullWithActions())
	})

	cmd := setupAlertTestCommand()
	// The one hook the real root installs that matters here.
	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		cmdutil.ApplyContextOutputFormat(c)
		return nil
	}
	cmd.SetArgs([]string{"describe", "alert", "1", "-vv"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, buf.String())
	}
	out := buf.String()

	// It really is the table view, not the YAML default falling through.
	if strings.Contains(out, "apiVersion:") {
		t.Fatalf("context output=table did not reach the command; this rendered YAML:\n%s", out)
	}
	if !strings.Contains(out, "RAW-RESPONSE") {
		t.Errorf("a context-configured table raw dump should contain the raw-response column, got:\n%s", out)
	}
	if !strings.Contains(out, "qctl alert resolve 1 --action apply_migration") {
		t.Errorf("a context-configured table view should carry the footer, got:\n%s", out)
	}
}

// alertWithDeclaredSeverity is an alert the rule declared `warning` and the
// rule instance's severity policy promoted. That is the only value an ALERT
// can carry: the backend writes the key when `severity != declared_severity`
// (atlas IssueContext.is_promoted, common/common/exceptions.py) and an alert's
// own severity is blocker by construction (`if issue_context.is_blocker:`,
// rule_checker.py get_issue_exception_from_issue_context).
func alertWithDeclaredSeverity() alerts.AlertFull {
	sev := api.IssueSeverityWarning
	a := sampleAlertFull()
	a.Body = &api.AlertBodySchema{DeclaredSeverity: &sev}
	return a
}

// The note is NOT verbosity-gated even though the value it explains is: it
// carries the value inline, so at the default tier it is the only place the
// override is stated at all.
func TestDescribeAlertNotesDeclaredSeverityAtEveryTier(t *testing.T) {
	for _, args := range [][]string{
		{"describe", "alert", "1"},
		{"describe", "alert", "1", "-v"},
		{"describe", "alert", "1", "-vv"},
		{"describe", "alert", "1", "-o", "table"},
		{"describe", "alert", "1", "-o", "table", "-vv"},
	} {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			out := runDescribeAlert(t, alertWithDeclaredSeverity(), args...)
			for _, want := range []string{
				"DECLARED SEVERITY",
				"the rule marked this issue warning",
				"(alert)",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("output should contain %q, got:\n%s", want, out)
				}
			}
		})
	}
}

// The -v body tier carries the machine-readable copy; the default tier must
// not, because that is where every other body field is withheld.
func TestDescribeAlertBodyTierCarriesDeclaredSeverity(t *testing.T) {
	verbose := runDescribeAlert(t, alertWithDeclaredSeverity(), "describe", "alert", "1", "-v")
	var doc struct {
		Spec struct {
			Body struct {
				DeclaredSeverity string `yaml:"declared_severity"`
			} `yaml:"body"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(verbose), &doc); err != nil {
		t.Fatalf("-v output did not parse as YAML: %v\n%s", err, verbose)
	}
	if doc.Spec.Body.DeclaredSeverity != "warning" {
		t.Errorf("spec.body.declared_severity = %q, want warning:\n%s", doc.Spec.Body.DeclaredSeverity, verbose)
	}

	base := runDescribeAlert(t, alertWithDeclaredSeverity(), "describe", "alert", "1")
	if strings.Contains(base, "declared_severity:") {
		t.Errorf("default tier printed the body key:\n%s", base)
	}
}

// -o json is the machine view and the one view with no footer at all. The
// value has to survive there; the prose must not appear.
func TestDescribeAlertJSONCarriesTheValueAndNoNote(t *testing.T) {
	out := runDescribeAlert(t, alertWithDeclaredSeverity(), "describe", "alert", "1", "-v", "-o", "json")
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("-o json output did not parse: %v\n%s", err, out)
	}
	spec, _ := doc["spec"].(map[string]interface{})
	body, _ := spec["body"].(map[string]interface{})
	if body["declared_severity"] != "warning" {
		t.Errorf("spec.body.declared_severity = %v, want warning:\n%s", body["declared_severity"], out)
	}
	if strings.Contains(out, "DECLARED SEVERITY") {
		t.Errorf("-o json must carry no footer prose, got:\n%s", out)
	}
}

// The two halves of the footer withhold themselves independently. A RESOLVED
// alert offers nothing to run, so NEXT ACTIONS is silent -- but its severity
// was still overridden, and the note is what says so.
func TestDescribeAlertNotesDeclaredSeverityOnAResolvedAlert(t *testing.T) {
	a := alertWithDeclaredSeverity()
	resolved := true
	a.Resolved = &resolved

	out := runDescribeAlert(t, a, "describe", "alert", "1")
	if !strings.Contains(out, "DECLARED SEVERITY") {
		t.Errorf("a resolved alert lost the note:\n%s", out)
	}
	if strings.Contains(out, "NEXT ACTIONS") {
		t.Errorf("a resolved alert must still offer no actions:\n%s", out)
	}
}

// The negative control for every assertion above: an alert whose rule severity
// was NOT overridden carries no key and no note. Without this the tests would
// pass against a note printed unconditionally.
func TestDescribeAlertSaysNothingWhenDeclaredSeverityIsAbsent(t *testing.T) {
	for _, args := range [][]string{
		{"describe", "alert", "1"},
		{"describe", "alert", "1", "-v"},
		{"describe", "alert", "1", "-o", "table"},
	} {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			out := runDescribeAlert(t, sampleAlertFullWithBody(), args...)
			for _, unwanted := range []string{"DECLARED SEVERITY", "declared_severity"} {
				if strings.Contains(out, unwanted) {
					t.Errorf("output should not contain %q, got:\n%s", unwanted, out)
				}
			}
		})
	}
}

// The footer is comments, so the note must not break the document or inject a
// phantom key -- the same guard the NEXT ACTIONS block carries.
func TestDescribeAlertDeclaredSeverityNoteStaysComments(t *testing.T) {
	out := runDescribeAlert(t, alertWithDeclaredSeverity(), "describe", "alert", "1")
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("note broke YAML parsing: %v\n%s", err, out)
	}
	for key := range parsed {
		if strings.ContainsAny(key, " #") {
			t.Errorf("note leaked a phantom YAML key %q:\n%s", key, out)
		}
	}
}

// describe alert decodes a marked message at its default and -v tiers, in YAML
// and in -o json, and -vv dumps the response as sent, marker and all.
func TestDescribeAlertMarkedMessageByTier(t *testing.T) {
	alert := sampleAlertFull()
	alert.Msg = `~|"~{~*Hyundai Sonata Hybrid Limited*~}~"|~ is not a valid **model**`
	const rendered = `"Hyundai Sonata Hybrid Limited" is not a valid model`
	for _, args := range [][]string{{}, {"-o", "json"}, {"-v"}, {"-v", "-o", "json"}, {"-o", "table", "--max-column-width", "2000"}} {
		rawOut := runDescribeAlert(t, alert, append([]string{"describe", "alert", "1"}, args...)...)
		if len(args) >= 2 && args[1] == "table" && !strings.Contains(rawOut, "\nMessage:\n"+rendered+"\n") {
			t.Errorf("describe alert table did not print the complete plain-text message outside the capped spec cell:\n%s", rawOut)
		}
		out := rawOut
		out = strings.ReplaceAll(out, `\"`, `"`) // -o json escapes the quotes
		if !strings.Contains(out, rendered) || strings.Contains(out, "~*") || strings.Contains(out, "~|") {
			t.Errorf("describe alert %v: want the message rendered, got:\n%s", args, out)
		}
	}
	if out := runDescribeAlert(t, alert, "describe", "alert", "1", "-vv"); !strings.Contains(out, "~{~*Hyundai Sonata Hybrid Limited*~}~") {
		t.Errorf("describe alert -vv: want the message as sent, got:\n%s", out)
	}
}

// -vv prints a 7-digit count as an integer in YAML, and an id past 2^53
// unrounded in JSON.
func TestDescribeAlertRawDumpKeepsIntegers(t *testing.T) {
	alert := sampleAlertFull()
	alert.Count = 1234567
	alert.SettingsModelID = 9007199254740993

	out := runDescribeAlert(t, alert, "describe", "alert", "1", "-vv")
	if !strings.Contains(out, "count: 1234567\n") {
		t.Errorf("-vv YAML should print count: 1234567, got:\n%s", out)
	}

	out = runDescribeAlert(t, alert, "describe", "alert", "1", "-vv", "-o", "json")
	if !strings.Contains(out, `"settings_model_id": 9007199254740993`) {
		t.Errorf("-vv JSON should print settings_model_id: 9007199254740993, got:\n%s", out)
	}
}

// respondAffectingRows answers a stats request with the given row count.
func respondAffectingRows(n int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"affecting_rows_count": n})
	}
}

// describeAlertRun is one `describe alert` run with its streams kept apart.
type describeAlertRun struct {
	err      error
	stdout   string
	stderr   string
	requests []string
}

// runDescribeAlertWithStats runs `describe alert 1` against a mock returning
// alert, with stats answering its stats route.
func runDescribeAlertWithStats(t *testing.T, alert alerts.AlertFull, stats http.HandlerFunc, args ...string) describeAlertRun {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, alert)
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/1/stats", stats)

	cmd := setupAlertTestCommand()
	cmd.SetArgs(append([]string{"describe", "alert", "1"}, args...))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	return describeAlertRun{err: err, stdout: stdout.String(), stderr: stderr.String(), requests: mock.Requests()}
}

// rowLevelDescribeAlert is a row-level alert whose count sits at the cap.
func rowLevelDescribeAlert() alerts.AlertFull {
	a := sampleAlertFull()
	a.IsRowLevel = true
	a.Count = 1000
	capped := true
	a.CountCapped = &capped
	return a
}

const alertStatsRequest = "GET /api/orgs/" + testOrgID + "/alerts/1/stats"

// A row-level alert's exact count comes from a second request, and every view
// that carried status.affecting_rows_count before it moved off the alert
// carries it again: the YAML tiers, -o json, the -o table status cell, and the
// -vv dump as a sibling of the untouched raw response.
func TestDescribeAlertShowsTheStatsRowCountOnEveryView(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want []string
	}{
		"default yaml": {nil, []string{"affecting_rows_count: 1234", "count: 1000", "count_capped: true"}},
		"verbose yaml": {[]string{"-v"}, []string{"affecting_rows_count: 1234", "count_capped: true"}},
		"json":         {[]string{"-o", "json"}, []string{`"affecting_rows_count": 1234`, `"count": 1000`, `"count_capped": true`}},
		"table":        {[]string{"-o", "table", "--max-column-width", "2000"}, []string{`"affecting_rows_count":1234`, `"count_capped":true`}},
		"raw yaml":     {[]string{"-vv"}, []string{"raw_stats:\n  affecting_rows_count: 1234\n"}},
		"raw json":     {[]string{"-vv", "-o", "json"}, []string{`"raw_stats": {`, `"affecting_rows_count": 1234`}},
	} {
		t.Run(name, func(t *testing.T) {
			run := runDescribeAlertWithStats(t, rowLevelDescribeAlert(), respondAffectingRows(1234), tc.args...)
			if run.err != nil {
				t.Fatalf("Execute() error = %v\n%s", run.err, run.stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(run.stdout, want) {
					t.Errorf("output should contain %q, got:\n%s", want, run.stdout)
				}
			}
			if run.stderr != "" {
				t.Errorf("a successful stats read should warn nothing, got stderr:\n%s", run.stderr)
			}
		})
	}
}

// The raw dump leaves the alert's own response as sent: the stats response is
// a sibling key, not a field spliced into raw_response.
func TestDescribeAlertRawDumpKeepsTheStatsOutOfTheRawResponse(t *testing.T) {
	run := runDescribeAlertWithStats(t, rowLevelDescribeAlert(), respondAffectingRows(1234), "-vv", "-o", "json")
	if run.err != nil {
		t.Fatalf("Execute() error = %v\n%s", run.err, run.stderr)
	}
	var doc struct {
		RawResponse map[string]interface{} `json:"raw_response"`
		RawStats    map[string]interface{} `json:"raw_stats"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, run.stdout)
	}
	if _, ok := doc.RawResponse["affecting_rows_count"]; ok {
		t.Errorf("raw_response gained affecting_rows_count:\n%s", run.stdout)
	}
	if doc.RawStats["affecting_rows_count"] != float64(1234) {
		t.Errorf("raw_stats = %v, want affecting_rows_count 1234", doc.RawStats)
	}
}

// A table-level alert's stats are null by definition, so the command does not
// ask: no request, no key, no warning.
func TestDescribeAlertSkipsTheStatsReadForATableLevelAlert(t *testing.T) {
	for _, args := range [][]string{nil, {"-o", "json"}, {"-vv"}} {
		run := runDescribeAlertWithStats(t, sampleAlertFull(), respondAffectingRows(1234), args...)
		if run.err != nil {
			t.Fatalf("describe alert %v: Execute() error = %v\n%s", args, run.err, run.stderr)
		}
		for _, req := range run.requests {
			if req == alertStatsRequest {
				t.Errorf("describe alert %v asked for a table-level alert's stats: %v", args, run.requests)
			}
		}
		if strings.Contains(run.stdout, "affecting_rows_count") || strings.Contains(run.stdout, "raw_stats") {
			t.Errorf("describe alert %v printed a row count it never read:\n%s", args, run.stdout)
		}
		if run.stderr != "" {
			t.Errorf("describe alert %v warned on stderr:\n%s", args, run.stderr)
		}
	}
}

// The control for the test above: the same harness does see the request when
// the alert is row level.
func TestDescribeAlertAsksForARowLevelAlertsStats(t *testing.T) {
	run := runDescribeAlertWithStats(t, rowLevelDescribeAlert(), respondAffectingRows(1234))
	if run.err != nil {
		t.Fatalf("Execute() error = %v\n%s", run.err, run.stderr)
	}
	asked := 0
	for _, req := range run.requests {
		if req == alertStatsRequest {
			asked++
		}
	}
	if asked != 1 {
		t.Errorf("want one stats request, got %d in %v", asked, run.requests)
	}
}

// The stats read is best-effort: a failure costs the field and one stderr
// line, never the describe.
func TestDescribeAlertSurvivesAFailedStatsRead(t *testing.T) {
	for name, stats := range map[string]http.HandlerFunc{
		"500": func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusInternalServerError, "boom")
		},
		"404": func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusNotFound, "Alert 1 can not be found.")
		},
	} {
		// The table's status cell is truncated at 80 columns by default, which
		// would cut a row count off before the check below could see it.
		for _, args := range [][]string{nil, {"-o", "json"}, {"-o", "table", "--max-column-width", "2000"}, {"-vv"}} {
			t.Run(fmt.Sprintf("%s %v", name, args), func(t *testing.T) {
				run := runDescribeAlertWithStats(t, rowLevelDescribeAlert(), stats, args...)
				if code := qerrors.GetExitCode(run.err); code != qerrors.ExitSuccess {
					t.Fatalf("exit code = %d, want 0 (err %v)", code, run.err)
				}
				if !strings.Contains(run.stdout, "count") {
					t.Errorf("the describe itself is missing:\n%s", run.stdout)
				}
				if strings.Contains(run.stdout, "affecting_rows_count") || strings.Contains(run.stdout, "raw_stats") {
					t.Errorf("a failed stats read still printed a row count:\n%s", run.stdout)
				}
				assertOneLineStatsWarning(t, run.stderr)
			})
		}
	}
}

// The -vv table has a RAW-STATS column only when the stats were read: a
// skipped or failed read leaves the column out rather than printing null.
func TestDescribeAlertRawTableHasNoStatsColumnWithoutStats(t *testing.T) {
	failed := func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "boom")
	}
	for name, tc := range map[string]struct {
		alert     alerts.AlertFull
		stats     http.HandlerFunc
		wantStats bool
	}{
		"read":    {rowLevelDescribeAlert(), respondAffectingRows(1234), true},
		"failed":  {rowLevelDescribeAlert(), failed, false},
		"skipped": {sampleAlertFull(), respondAffectingRows(1234), false},
	} {
		t.Run(name, func(t *testing.T) {
			run := runDescribeAlertWithStats(t, tc.alert, tc.stats, "-vv", "-o", "table")
			if run.err != nil {
				t.Fatalf("Execute() error = %v\n%s", run.err, run.stderr)
			}
			header, _, _ := strings.Cut(run.stdout, "\n")
			if !strings.Contains(header, "RAW-RESPONSE") {
				t.Fatalf("first line is not the table header:\n%s", run.stdout)
			}
			if got := strings.Contains(header, "RAW-STATS"); got != tc.wantStats {
				t.Errorf("RAW-STATS in header = %v, want %v:\n%s", got, tc.wantStats, run.stdout)
			}
		})
	}
}

// assertOneLineStatsWarning fails unless stderr is exactly the stats warning
// line, without the organization-scope note a 403 or 404 error carries.
func assertOneLineStatsWarning(t *testing.T, stderr string) {
	t.Helper()
	note := apierror.ScopeNote()
	if note == "" {
		t.Fatal("no organization scope was recorded, so no stats error carried the note checked for here")
	}
	noteHead, _, _ := strings.Cut(note, "\n")
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "warning: affected row count unavailable: ") {
		t.Errorf("want one stderr warning line, got:\n%s", stderr)
	}
	if strings.Contains(stderr, noteHead) {
		t.Errorf("the stderr warning carries the organization-scope note:\n%s", stderr)
	}
}

// ingestion_jobs sits in the -v tier beside ingestion_job_ids, which stays.
func TestDescribeAlertShowsIngestionJobsAtVerbose(t *testing.T) {
	a := sampleAlertFull()
	storedItemID, fileName := 41, "orders.csv"
	a.IngestionJobs = &[]api.AlertIngestionJobSchema{
		{ID: 100, Kind: api.IngestionJobKindFile, State: api.IngestionJobState("done"), StoredItemID: &storedItemID, FileName: &fileName},
	}

	base := runDescribeAlertWithStats(t, a, respondAffectingRows(0))
	if base.err != nil {
		t.Fatalf("Execute() error = %v\n%s", base.err, base.stderr)
	}
	for _, unwanted := range []string{"ingestion_jobs:", "ingestion_job_ids:"} {
		if strings.Contains(base.stdout, unwanted) {
			t.Errorf("default tier should omit %q:\n%s", unwanted, base.stdout)
		}
	}

	verbose := runDescribeAlertWithStats(t, a, respondAffectingRows(0), "-v")
	if verbose.err != nil {
		t.Fatalf("Execute() error = %v\n%s", verbose.err, verbose.stderr)
	}
	for _, want := range []string{"ingestion_job_ids:", "ingestion_jobs:", "kind: file", "file_name: orders.csv", "stored_item_id: 41", "state: done"} {
		if !strings.Contains(verbose.stdout, want) {
			t.Errorf("-v output should contain %q, got:\n%s", want, verbose.stdout)
		}
	}
}
