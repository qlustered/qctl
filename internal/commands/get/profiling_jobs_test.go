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
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupTestCommandForProfiling creates a root command with global flags and adds the profiling-jobs command
func setupTestCommandForProfiling() *cobra.Command {
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
	getCmd.AddCommand(NewProfilingJobsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

// Helper to create sample profiling job fixtures
func sampleProfilingJobTiny() profiling.ProfilingJobTiny {
	updatedAt := time.Date(2025, 1, 15, 10, 5, 0, 0, time.UTC)

	return profiling.ProfilingJobTiny{
		ID:              1,
		DatasetID:       1,
		DatasetName:     "user_data",
		SettingsModelID: 10,
		State:           api.TrainingJobStateFinished,
		Step:            api.Finished,
		UpdatedAt:       updatedAt,
	}
}

func sampleProfilingJobTiny2() profiling.ProfilingJobTiny {
	updatedAt := time.Date(2025, 1, 16, 14, 5, 0, 0, time.UTC)

	return profiling.ProfilingJobTiny{
		ID:              2,
		DatasetID:       2,
		DatasetName:     "product_catalog",
		SettingsModelID: 20,
		State:           api.TrainingJobStateRunning,
		Step:            api.Analysis,
		UpdatedAt:       updatedAt,
	}
}

func TestGetProfilingJobsCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockJobs           []profiling.ProfilingJobTiny
		wantErr            bool
		wantOutputContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{},
			mockJobs: []profiling.ProfilingJobTiny{
				sampleProfilingJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data", "finished"},
		},
		{
			name: "with json output",
			args: []string{"--output", "json"},
			mockJobs: []profiling.ProfilingJobTiny{
				sampleProfilingJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{`"dataset_name": "user_data"`, `"state": "finished"`},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockJobs: []profiling.ProfilingJobTiny{
				sampleProfilingJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"dataset_name: user_data", "state: finished"},
		},
		{
			name: "with table-id filter",
			args: []string{"--table-id", "1"},
			mockJobs: []profiling.ProfilingJobTiny{
				sampleProfilingJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name: "with sorting",
			args: []string{"--order-by", "dataset_id", "--reverse"},
			mockJobs: []profiling.ProfilingJobTiny{
				sampleProfilingJobTiny(),
			},
			wantErr: false,
		},
		{
			name: "with limit",
			args: []string{"--limit", "1"},
			mockJobs: []profiling.ProfilingJobTiny{
				sampleProfilingJobTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name:     "empty results",
			args:     []string{},
			mockJobs: []profiling.ProfilingJobTiny{},
			wantErr:  false,
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

			// Register mock handler for profiling jobs
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockJobs)
				page := 1
				response := profiling.ProfilingJobsListResponse{
					Results:   tt.mockJobs,
					TotalRows: &totalRows,
					Page:      &page,
					Next:      nil,
					Previous:  nil,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			// Create command with proper hierarchy
			cmd := setupTestCommandForProfiling()

			// Prepare args with "get profiling-jobs" prefix
			args := append([]string{"get", "profiling-jobs"}, tt.args...)

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

func TestGetProfilingJobsCommand_NotLoggedIn(t *testing.T) {
	// Test that command fails when not logged in
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1") // avoid picking up real keyring creds in tests

	// Setup config but NO credentials
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupTestCommandForProfiling()
	cmd.SetArgs([]string{"get", "profiling-jobs"})

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

func TestGetProfilingJobsCommand_ServerUnauthorized(t *testing.T) {
	// Test that command handles unauthorized errors from server
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register handler that returns 401
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusUnauthorized, "Unauthorized")
	})

	cmd := setupTestCommandForProfiling()
	cmd.SetArgs([]string{"get", "profiling-jobs"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 401")
	}

	if !strings.Contains(err.Error(), "failed to get profiling jobs") {
		t.Errorf("Expected 'failed to get profiling jobs' error, got: %v", err)
	}
}

func TestGetProfilingJobsCommand_Pagination(t *testing.T) {
	// Test that command shows pagination hint when more results are available
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	requestCount := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		// Return first page with indication that more results exist
		totalRows := 2
		pageNum := 1
		nextStart := 2
		nextPagination := &api.PaginationSchema{Start: &nextStart}
		response := profiling.ProfilingJobsListResponse{
			Results:   []profiling.ProfilingJobTiny{sampleProfilingJobTiny()},
			TotalRows: &totalRows,
			Page:      &pageNum,
			Next:      nextPagination,
		}

		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommandForProfiling()
	cmd.SetArgs([]string{"get", "profiling-jobs", "--output", "json"})

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
		t.Errorf("Expected 1 profiling job in output (single page), got %d", len(results))
	}

	// Verify stderr contains pagination hint
	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "More available") {
		t.Errorf("Expected pagination hint in stderr, got: %s", stderrOutput)
	}
}

func TestGetProfilingJobsCommand_CustomColumns(t *testing.T) {
	// Test custom column selection for table output
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := profiling.ProfilingJobsListResponse{
			Results:   []profiling.ProfilingJobTiny{sampleProfilingJobTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommandForProfiling()
	cmd.SetArgs([]string{"get", "profiling-jobs", "--output", "table", "--columns", "id,dataset_id,state"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Verify table contains ID, DATASET_ID, STATE headers
	if !strings.Contains(output, "ID") || !strings.Contains(output, "DATASET") || !strings.Contains(output, "STATE") {
		t.Errorf("Expected ID, DATASET_ID, STATE columns in table output, got: %s", output)
	}
}

func TestGetProfilingJobsCommand_DatasetFilter(t *testing.T) {
	// Test table-id filter
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		// Verify the dataset_id parameter is passed
		if dsID := r.URL.Query().Get("dataset_id"); dsID != "5" {
			t.Errorf("Expected dataset_id=5, got %s", dsID)
		}

		totalRows := 1
		page := 1
		response := profiling.ProfilingJobsListResponse{
			Results:   []profiling.ProfilingJobTiny{sampleProfilingJobTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommandForProfiling()
	cmd.SetArgs([]string{"get", "profiling-jobs", "--table-id", "5"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}
