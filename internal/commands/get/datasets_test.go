package get

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
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// setupTestCommand creates a root command with global flags and adds the datasets command
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

	// Add get command
	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewDatasetsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

// Helper to create sample dataset fixtures
func sampleDatasetTiny() datasets.DatasetTiny {
	cleanRows := 10000
	badRows := 5
	progress := 100
	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("123e4567-e89b-12d3-a456-426614174000"))

	return datasets.DatasetTiny{
		ID:               1,
		VersionID:        10,
		Name:             "user_data",
		State:            api.DataSetState("active"),
		DestinationName:  "postgres_main",
		UnresolvedAlerts: 0,
		OrganizationID:   orgID,
		CleanRowsCount:   &cleanRows,
		BadRowsCount:     &badRows,
		ProgressPercent:  &progress,
		Users:            []api.UserInfoTinyDictSchema{},
	}
}

func sampleDatasetTiny2() datasets.DatasetTiny {
	cleanRows := 5000
	badRows := 2
	progress := 100
	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("123e4567-e89b-12d3-a456-426614174001"))

	return datasets.DatasetTiny{
		ID:               2,
		VersionID:        5,
		Name:             "product_catalog",
		State:            api.DataSetState("disabled"),
		DestinationName:  "snowflake_prod",
		UnresolvedAlerts: 2,
		OrganizationID:   orgID,
		CleanRowsCount:   &cleanRows,
		BadRowsCount:     &badRows,
		ProgressPercent:  &progress,
		Users:            []api.UserInfoTinyDictSchema{},
	}
}

func TestGetDatasetsCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockDatasets          []datasets.DatasetTiny
		wantErr               bool
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{},
			mockDatasets: []datasets.DatasetTiny{
				sampleDatasetTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data", "active", "postgres_main"},
		},
		{
			name: "with json output",
			args: []string{"--output", "json"},
			mockDatasets: []datasets.DatasetTiny{
				sampleDatasetTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{`"name": "user_data"`, `"state": "active"`},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockDatasets: []datasets.DatasetTiny{
				sampleDatasetTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"name: user_data", "state: active"},
		},
		{
			name: "with state filter",
			args: []string{"--state", "active"},
			mockDatasets: []datasets.DatasetTiny{
				sampleDatasetTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name: "with states filter",
			args: []string{"--states", "active,disabled"},
			mockDatasets: []datasets.DatasetTiny{
				sampleDatasetTiny(),
				sampleDatasetTiny2(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data", "product_catalog"},
		},
		{
			name: "with sorting",
			args: []string{"--order-by", "name", "--reverse"},
			mockDatasets: []datasets.DatasetTiny{
				sampleDatasetTiny(),
			},
			wantErr: false,
		},
		{
			name: "with destination name filter",
			args: []string{"--destination-name", "postgres_main"},
			mockDatasets: []datasets.DatasetTiny{
				sampleDatasetTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name: "with name filter",
			args: []string{"--name", "user_data"},
			mockDatasets: []datasets.DatasetTiny{
				sampleDatasetTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name: "with limit",
			args: []string{"--limit", "1"},
			mockDatasets: []datasets.DatasetTiny{
				sampleDatasetTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name:         "empty results",
			args:         []string{},
			mockDatasets: []datasets.DatasetTiny{},
			wantErr:      false,
		},
		{
			name:    "conflicting state flags",
			args:    []string{"--state", "active", "--states", "active,disabled"},
			wantErr: true,
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

			// Register mock handler for datasets
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				// Check for filters in query params (just to verify they're passed correctly)
				query := r.URL.Query()

				// Verify filter parameters if they're set
				if tt.args != nil {
					for i, arg := range tt.args {
						if arg == "--state" && i+1 < len(tt.args) {
							if state := query.Get("states"); state != "" && !strings.Contains(state, tt.args[i+1]) {
								t.Errorf("Expected state filter %s in query, got %s", tt.args[i+1], state)
							}
						}
						if arg == "--name" && i+1 < len(tt.args) {
							if name := query.Get("name"); name != tt.args[i+1] {
								t.Errorf("Expected name filter %s, got %s", tt.args[i+1], name)
							}
						}
						if arg == "--destination-name" && i+1 < len(tt.args) {
							if dest := query.Get("destination_name"); dest != tt.args[i+1] {
								t.Errorf("Expected destination_name filter %s, got %s", tt.args[i+1], dest)
							}
						}
					}
				}

				totalRows := len(tt.mockDatasets)
				page := 1
				response := datasets.DatasetsListResponse{
					Results:   tt.mockDatasets,
					TotalRows: &totalRows,
					Page:      &page,
					Next:      nil,
					Previous:  nil,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			// Create command with proper hierarchy
			cmd := setupTestCommand()

			// Prepare args with "get tables" prefix
			args := append([]string{"get", "tables"}, tt.args...)

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

func TestGetDatasetsCommand_NotLoggedIn(t *testing.T) {
	// Test that command fails when not logged in
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1") // avoid picking up real keyring creds in tests

	// Setup config but NO credentials
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables"})

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

func TestGetDatasetsCommand_InvalidConfig(t *testing.T) {
	// Test that command fails when config is invalid
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Don't setup any config file
	// This should cause config.Load() to fail

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables"})

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

func TestGetDatasetsCommand_ServerUnauthorized(t *testing.T) {
	// Test that command handles unauthorized errors from server
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 401
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusUnauthorized, "Unauthorized")
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 401")
	}

	if !strings.Contains(err.Error(), "failed to get tables") {
		t.Errorf("Expected 'failed to get tables' error, got: %v", err)
	}
}

func TestGetDatasetsCommand_ServerError(t *testing.T) {
	// Test that command handles server errors
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 500
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestGetDatasetsCommand_Pagination(t *testing.T) {
	// Test that command shows pagination hint when more results are available
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	requestCount := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		// Return first page with indication that more results exist
		totalRows := 2
		pageNum := 1
		nextStart := 2
		nextPagination := &api.PaginationSchema{Start: &nextStart}
		response := datasets.DatasetsListResponse{
			Results:   []datasets.DatasetTiny{sampleDatasetTiny()},
			TotalRows: &totalRows,
			Page:      &pageNum,
			Next:      nextPagination,
		}

		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables", "--output", "json"})

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
	var results []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 table in output (single page), got %d", len(results))
	}

	// Verify stderr contains pagination hint
	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "More available") {
		t.Errorf("Expected pagination hint in stderr, got: %s", stderrOutput)
	}
}

func TestGetDatasetsCommand_CustomColumns(t *testing.T) {
	// Test custom column selection for table output
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := datasets.DatasetsListResponse{
			Results:   []datasets.DatasetTiny{sampleDatasetTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables", "--output", "table", "--columns", "id,name,state"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Verify table contains ID, NAME, STATE headers
	if !strings.Contains(output, "ID") || !strings.Contains(output, "NAME") || !strings.Contains(output, "STATE") {
		t.Errorf("Expected ID, NAME, STATE columns in table output")
	}
}
