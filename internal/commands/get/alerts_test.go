package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupAlertsTestCommand creates a root command with global flags and adds the alerts command
func setupAlertsTestCommand() *cobra.Command {
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

	// Add get command
	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewAlertsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

// Helper to create sample alert fixtures
func sampleAlertTiny() alerts.AlertTiny {
	createdAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	dataSourceID := 5
	dataSourceName := "customer_data"
	redirectURL := "/datasets/1/alerts/1"
	resolvableByUser := true
	resolveAfterMigration := false
	affectedStoredItems := []alerts.StoredItemToAlert{}

	return alerts.AlertTiny{
		ID:                    1,
		DatasetName:           "user_data",
		DatasetID:             10,
		DataSourceModelID:     &dataSourceID,
		DataSourceModelName:   &dataSourceName,
		IssueType:             "MISSING_COLUMN",
		Count:                 3,
		Msg:                   "Column 'email' is missing from 3 files",
		CreatedAt:             &createdAt,
		ResolvedAt:            nil,
		ResolvableByUser:      &resolvableByUser,
		ResolveAfterMigration: &resolveAfterMigration,
		RedirectURL:           &redirectURL,
		AffectedStoredItems:   &affectedStoredItems,
		Whodunit:              nil,
		AssignedUser:          nil,
	}
}

func sampleAlertTiny2() alerts.AlertTiny {
	createdAt := time.Date(2025, 1, 10, 14, 0, 0, 0, time.UTC)
	dataSourceID := 8
	dataSourceName := "sales_data"
	resolvableByUser := true
	resolveAfterMigration := false
	affectedStoredItems := []alerts.StoredItemToAlert{}

	return alerts.AlertTiny{
		ID:                    2,
		DatasetName:           "product_catalog",
		DatasetID:             15,
		DataSourceModelID:     &dataSourceID,
		DataSourceModelName:   &dataSourceName,
		IssueType:             "VALIDATION_ERROR",
		Count:                 10,
		Msg:                   "Price field validation failed for 10 rows",
		CreatedAt:             &createdAt,
		ResolvableByUser:      &resolvableByUser,
		ResolveAfterMigration: &resolveAfterMigration,
		AffectedStoredItems:   &affectedStoredItems,
	}
}

func TestGetAlertsCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockAlerts            []alerts.AlertTiny
		wantErr               bool
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data", "MISSING_COLUMN"},
		},
		{
			name: "with json output",
			args: []string{"--output", "json"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{`"dataset_name": "user_data"`, `"issue_type": "MISSING_COLUMN"`},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"dataset_name: user_data", "issue_type: MISSING_COLUMN"},
		},
		{
			name: "with resolved filter",
			args: []string{"--resolved", "false"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name: "with dataset filter",
			args: []string{"--dataset-id", "10"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name: "with is-muted filter",
			args: []string{"--is-muted", "false"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name: "with resolvable-by-user filter",
			args: []string{"--resolvable-by-user", "true"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name: "with search query",
			args: []string{"--search", "email"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"email"},
		},
		{
			name: "with sorting",
			args: []string{"--order-by", "created_at", "--reverse"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr: false,
		},
		{
			name: "with limit",
			args: []string{"--limit", "1"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name:       "empty results",
			args:       []string{},
			mockAlerts: []alerts.AlertTiny{},
			wantErr:    false,
		},
		{
			name:    "invalid sort field",
			args:    []string{"--order-by", "invalid_field"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			// Register mock handler for alerts
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
				// Check for filters in query params (just to verify they're passed correctly)
				query := r.URL.Query()

				// Verify filter parameters if they're set
				if tt.args != nil {
					for i, arg := range tt.args {
						if arg == "--dataset-id" && i+1 < len(tt.args) {
							if datasetID := query.Get("dataset_id"); datasetID != tt.args[i+1] {
								t.Errorf("Expected dataset_id filter %s, got %s", tt.args[i+1], datasetID)
							}
						}
						if arg == "--search" && i+1 < len(tt.args) {
							if search := query.Get("search_query"); search != tt.args[i+1] {
								t.Errorf("Expected search_query filter %s, got %s", tt.args[i+1], search)
							}
						}
					}
				}

				totalRows := len(tt.mockAlerts)
				page := 1
				response := alerts.AlertsListResponse{
					Results:   tt.mockAlerts,
					TotalRows: &totalRows,
					Page:      &page,
					Next:      nil,
					Previous:  nil,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			// Create command with proper hierarchy
			cmd := setupAlertsTestCommand()

			// Prepare args with "get alerts" prefix
			args := append([]string{"get", "alerts"}, tt.args...)

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

				// Check output does not contain unwanted strings
				for _, notWant := range tt.wantOutputNotContains {
					if strings.Contains(output, notWant) {
						t.Errorf("Output should not contain %q, got: %s", notWant, output)
					}
				}
			}
		})
	}
}

func TestGetAlertsCommand_NotLoggedIn(t *testing.T) {
	// Test that command fails when not logged in
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1") // avoid picking up real keyring creds in tests

	// Setup config but NO credentials
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupAlertsTestCommand()
	cmd.SetArgs([]string{"get", "alerts"})

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

func TestGetAlertsCommand_InvalidConfig(t *testing.T) {
	// Test that command fails when config is invalid
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Don't setup any config file
	// This should cause config.Load() to fail

	cmd := setupAlertsTestCommand()
	cmd.SetArgs([]string{"get", "alerts"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when config is missing")
	}

	// The error message can vary - just check that there's an error about config/context
	if !strings.Contains(err.Error(), "config") && !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "server") {
		t.Errorf("Expected config-related error, got: %v", err)
	}
}

func TestGetAlertsCommand_ServerUnauthorized(t *testing.T) {
	// Test that command handles unauthorized errors from server
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 401
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusUnauthorized, "Unauthorized")
	})

	cmd := setupAlertsTestCommand()
	cmd.SetArgs([]string{"get", "alerts"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 401")
	}

	if !strings.Contains(err.Error(), "failed to get alerts") {
		t.Errorf("Expected 'failed to get alerts' error, got: %v", err)
	}
}

func TestGetAlertsCommand_ServerError(t *testing.T) {
	// Test that command handles server errors
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 500
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupAlertsTestCommand()
	cmd.SetArgs([]string{"get", "alerts"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestGetAlertsCommand_Pagination(t *testing.T) {
	// Test that command shows pagination hint when more results are available
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	requestCount := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		// Return first page with indication that more results exist
		totalRows := 2
		pageNum := 1
		nextStart := 2
		response := alerts.AlertsListResponse{
			Results:   []alerts.AlertTiny{sampleAlertTiny()},
			TotalRows: &totalRows,
			Page:      &pageNum,
			Next:      &alerts.PaginationSchema{Start: &nextStart},
		}

		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupAlertsTestCommand()
	cmd.SetArgs([]string{"get", "alerts", "--output", "json"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Verify only 1 request is made (no auto-pagination)
	if requestCount != 1 {
		t.Errorf("Expected exactly 1 request (no auto-pagination), got %d", requestCount)
	}

	// Verify output contains only first page results
	var results []alerts.AlertTiny
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 alert in output (single page), got %d", len(results))
	}

	// Verify stderr contains pagination hint
	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "More available") {
		t.Errorf("Expected pagination hint in stderr, got: %s", stderrOutput)
	}
}

func TestGetAlertsCommand_CustomColumns(t *testing.T) {
	// Test custom column selection for table output
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := alerts.AlertsListResponse{
			Results:   []alerts.AlertTiny{sampleAlertTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupAlertsTestCommand()
	cmd.SetArgs([]string{"get", "alerts", "--output", "table", "--columns", "id,dataset_name,issue_type"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Verify table contains expected data (headers may vary in case)
	upperOutput := strings.ToUpper(output)
	if !strings.Contains(upperOutput, "ID") || !strings.Contains(upperOutput, "DATASET") || !strings.Contains(upperOutput, "ISSUE") {
		t.Errorf("Expected ID, DATASET_NAME, ISSUE_TYPE columns in table output, got: %s", output)
	}
}
