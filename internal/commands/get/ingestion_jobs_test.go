package get

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

// setupTestCommandForIngestion creates a root command with global flags and adds the ingestion-jobs command
func setupTestCommandForIngestion() *cobra.Command {
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
	getCmd.AddCommand(NewIngestionJobsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

// Helper to create sample ingestion job fixtures
func sampleIngestionJobTiny() ingestion.IngestionJobTiny {
	cleanRows := 10000
	badRows := 5
	storedItemID := 101
	updatedAt := time.Date(2025, 1, 15, 10, 5, 0, 0, time.UTC)

	return ingestion.IngestionJobTiny{
		ID:             1,
		DatasetID:      1,
		DatasetName:    "user_data",
		Key:            "users/users_2025_01_15.csv",
		FileName:       "users_2025_01_15.csv",
		StoredItemID:   &storedItemID,
		State:          api.IngestionJobState("finished"),
		CleanRowsCount: &cleanRows,
		BadRowsCount:   &badRows,
		TryCount:       1,
		IsDryRun:       false,
		UpdatedAt:      updatedAt,
	}
}

func sampleIngestionJobTiny2() ingestion.IngestionJobTiny {
	cleanRows := 5000
	badRows := 2
	storedItemID := 102
	updatedAt := time.Date(2025, 1, 16, 14, 5, 0, 0, time.UTC)

	return ingestion.IngestionJobTiny{
		ID:             2,
		DatasetID:      2,
		DatasetName:    "product_catalog",
		Key:            "products/products_2025_01_16.csv",
		FileName:       "products_2025_01_16.csv",
		StoredItemID:   &storedItemID,
		State:          api.IngestionJobState("running"),
		CleanRowsCount: &cleanRows,
		BadRowsCount:   &badRows,
		TryCount:       1,
		IsDryRun:       false,
		UpdatedAt:      updatedAt,
	}
}

func TestGetIngestionJobsCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockJobs           []ingestion.IngestionJobTiny
		wantErr            bool
		wantOutputContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{},
			mockJobs: []ingestion.IngestionJobTiny{
				sampleIngestionJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"users_2025_01_15.csv", "finished"},
		},
		{
			name: "with json output",
			args: []string{"--output", "json"},
			mockJobs: []ingestion.IngestionJobTiny{
				sampleIngestionJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{`"dataset_name": "user_data"`, `"state": "finished"`},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockJobs: []ingestion.IngestionJobTiny{
				sampleIngestionJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"dataset_name: user_data", "state: finished"},
		},
		{
			name: "with state filter",
			args: []string{"--state", "running"},
			mockJobs: []ingestion.IngestionJobTiny{
				sampleIngestionJobTiny2(),
			},
			wantErr:            false,
			wantOutputContains: []string{"products_2025_01_16.csv", "running"},
		},
		{
			name: "with states filter",
			args: []string{"--states", "running,finished"},
			mockJobs: []ingestion.IngestionJobTiny{
				sampleIngestionJobTiny(),
				sampleIngestionJobTiny2(),
			},
			wantErr:            false,
			wantOutputContains: []string{"users_2025_01_15.csv", "products_2025_01_16.csv"},
		},
		{
			name: "with table-id filter",
			args: []string{"--table-id", "1"},
			mockJobs: []ingestion.IngestionJobTiny{
				sampleIngestionJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"users_2025_01_15.csv"},
		},
		{
			name: "with sorting",
			args: []string{"--order-by", "table_id", "--reverse"},
			mockJobs: []ingestion.IngestionJobTiny{
				sampleIngestionJobTiny(),
			},
			wantErr: false,
		},
		{
			name: "with limit",
			args: []string{"--limit", "1"},
			mockJobs: []ingestion.IngestionJobTiny{
				sampleIngestionJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"users_2025_01_15.csv"},
		},
		{
			name:     "empty results",
			args:     []string{},
			mockJobs: []ingestion.IngestionJobTiny{},
			wantErr:  false,
		},
		{
			name:    "conflicting state flags",
			args:    []string{"--state", "running", "--states", "running,finished"},
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

			// Register mock handler for ingestion jobs
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockJobs)
				page := 1
				response := ingestion.IngestionJobsListResponse{
					Results:   tt.mockJobs,
					TotalRows: &totalRows,
					Page:      &page,
					Next:      nil,
					Previous:  nil,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			// Create command with proper hierarchy
			cmd := setupTestCommandForIngestion()

			// Prepare args with "get ingestion-jobs" prefix
			args := append([]string{"get", "ingestion-jobs"}, tt.args...)

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

func TestGetIngestionJobsCommand_NotLoggedIn(t *testing.T) {
	// Test that command fails when not logged in
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1") // avoid picking up real keyring creds in tests

	// Setup config but NO credentials
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs"})

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

func TestGetIngestionJobsCommand_ServerUnauthorized(t *testing.T) {
	// Test that command handles unauthorized errors from server
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 401
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusUnauthorized, "Unauthorized")
	})

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 401")
	}

	if !strings.Contains(err.Error(), "failed to get ingestion jobs") {
		t.Errorf("Expected 'failed to get ingestion jobs' error, got: %v", err)
	}
}

func TestGetIngestionJobsCommand_ServerError(t *testing.T) {
	// Test that command handles server errors
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 500
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestGetIngestionJobsCommand_Pagination(t *testing.T) {
	// Test that command shows pagination hint when more results are available
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	requestCount := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		// Return first page with indication that more results exist
		totalRows := 2
		pageNum := 1
		nextStart := 2
		nextPagination := &api.PaginationSchema{Start: &nextStart}
		response := ingestion.IngestionJobsListResponse{
			Results:   []ingestion.IngestionJobTiny{sampleIngestionJobTiny()},
			TotalRows: &totalRows,
			Page:      &pageNum,
			Next:      nextPagination,
		}

		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--output", "json"})

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
	// Use a generic map instead of typed struct to avoid time.Time unmarshalling issues
	var results []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 ingestion job in output (single page), got %d", len(results))
	}

	// Verify stderr contains pagination hint
	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "More available") {
		t.Errorf("Expected pagination hint in stderr, got: %s", stderrOutput)
	}
}

func TestGetIngestionJobsCommand_CustomColumns(t *testing.T) {
	// Test custom column selection for table output
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := ingestion.IngestionJobsListResponse{
			Results:   []ingestion.IngestionJobTiny{sampleIngestionJobTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--output", "table", "--columns", "id,table_id,state"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Verify table contains ID, TABLE_ID, STATE headers
	if !strings.Contains(output, "ID") || !strings.Contains(output, "TABLE") || !strings.Contains(output, "STATE") {
		t.Errorf("Expected ID, TABLE_ID, STATE columns in table output")
	}
}

func TestGetIngestionJobsCommand_DataSourceFilter(t *testing.T) {
	// Test cloud-source-id filter
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		// Verify the data_source_model_id parameter is passed
		if dsID := r.URL.Query().Get("data_source_model_id"); dsID != "5" {
			t.Errorf("Expected data_source_model_id=5, got %s", dsID)
		}

		totalRows := 1
		page := 1
		response := ingestion.IngestionJobsListResponse{
			Results:   []ingestion.IngestionJobTiny{sampleIngestionJobTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--cloud-source-id", "5"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}
