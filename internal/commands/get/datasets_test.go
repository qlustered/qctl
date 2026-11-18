package get

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
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
		Users:            []api.DataSetUserInfoTinySchema{},
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
		Users:            []api.DataSetUserInfoTinySchema{},
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
			wantOutputContains: []string{"user_data", "active", "postgres_main", "CLEAN-ROWS-COUNT", "10000"},
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

func TestGetTablesByKindSendsFilterAndKeepsWireOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	kindID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("problem_domain_id"); got != kindID {
			t.Errorf("problem_domain_id = %q, want %q", got, kindID)
		}
		if got := r.URL.Query().Get("states"); got != "deleted" {
			t.Errorf("states = %q, want deleted", got)
		}
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsListResponse{Results: []datasets.DatasetTiny{sampleDatasetTiny()}, TotalRows: func() *int { n := 1; return &n }()})
	})
	cmd := setupTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"get", "tables", "--table-kind", kindID, "--state", "deleted", "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	decodeListItems(t, out.Bytes(), &rows)
	if len(rows) != 1 || rows[0]["name"] != "user_data" {
		t.Fatalf("wire rows = %+v", rows)
	}
}

func TestGetTablesByKindPermissionFailureKeepsExitCode(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]string{"detail": "problem_domain:read required"})
	})
	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables", "--table-kind", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"})
	if code := qerrors.GetExitCode(cmd.Execute()); code != 4 {
		t.Fatalf("exit code = %d, want 4", code)
	}
}

func TestGetDatasetsCommand_NotLoggedIn(t *testing.T) {
	// Test that command fails when not logged in
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

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
		response := datasets.DatasetsListResponse{
			Results:   []datasets.DatasetTiny{sampleDatasetTiny()},
			TotalRows: &totalRows,
			Page:      &pageNum,
		}

		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables", "--limit", "1", "--output", "json"})

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
	decodeListItems(t, stdout.Bytes(), &results)

	if len(results) != 1 {
		t.Errorf("Expected 1 table in output (single page), got %d", len(results))
	}

	// atlas sends no next off its cursor path; total_rows alone says page 2 exists.
	assertEnvelopeNextPage(t, stdout.Bytes(), stderr.String(), 2)
}

func TestGetDatasetsCommand_EmptyStateBanner(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		wantStderrContain string
		wantStderrAbsent  string
		wantStdout        string
	}{
		{
			name:              "empty results in table format writes message to stderr",
			args:              []string{"get", "tables"},
			wantStderrContain: `No tables found in org "` + testOrgID + `" (context: "default")`,
			wantStdout:        "",
		},
		{
			name:             "empty results in json format does not write empty-state message",
			args:             []string{"get", "tables", "--output", "json"},
			wantStderrAbsent: "No tables found",
			wantStdout:       "{\n  \"items\": [],\n  \"kind\": \"List\",\n  \"limit\": 100,\n  \"next_page\": null,\n  \"page\": 1,\n  \"total\": 0\n}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				totalRows := 0
				pageNum := 1
				response := datasets.DatasetsListResponse{
					Results:   []datasets.DatasetTiny{},
					TotalRows: &totalRows,
					Page:      &pageNum,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			cmd := setupTestCommand()
			cmd.SetArgs(tt.args)

			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if tt.wantStderrContain != "" && !strings.Contains(stderr.String(), tt.wantStderrContain) {
				t.Errorf("Expected stderr to contain %q, got: %q", tt.wantStderrContain, stderr.String())
			}
			if tt.wantStderrAbsent != "" && strings.Contains(stderr.String(), tt.wantStderrAbsent) {
				t.Errorf("Expected stderr to NOT contain %q, got: %q", tt.wantStderrAbsent, stderr.String())
			}
			gotStdout := strings.TrimSpace(stdout.String())
			if gotStdout != tt.wantStdout {
				t.Errorf("Expected stdout %q, got: %q", tt.wantStdout, gotStdout)
			}
		})
	}
}

// TestGetDatasetsCommand_EmptyResultRejectsUnknownColumn pins the
// internal/output print path end to end: before printEmptyResult validated
// --columns, a zero-row result short-circuited straight to "No tables
// found..." without ever constructing the printer that would have caught an
// unknown column name.
func TestGetDatasetsCommand_EmptyResultRejectsUnknownColumn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 0
		page := 1
		response := datasets.DatasetsListResponse{
			Results:   []datasets.DatasetTiny{},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables", "--output", "table", "--columns", "bogus_column"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unknown column "bogus_column"`) {
		t.Fatalf("expected an unknown-column error on an empty result, got %v", err)
	}
	if strings.Contains(buf.String(), "No tables found") {
		t.Errorf("must not print the empty-result message when --columns is invalid, got: %q", buf.String())
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

// A positive --destination-id must still reach the request as destination_id,
// the counterpart to TestGetDatasetsRejectsNonPositiveDestinationID below.
func TestGetDatasetsSendsPositiveDestinationID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var gotQuery url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		totalRows := 0
		page := 1
		response := datasets.DatasetsListResponse{
			Results:   []datasets.DatasetTiny{},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommand()
	cmd.SetArgs([]string{"get", "tables", "--destination-id", "5"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := gotQuery.Get("destination_id"); got != "5" {
		t.Errorf("expected destination_id=5 in query, got %q (query: %v)", got, gotQuery)
	}
}

// --destination-id 0 used to drop the filter and list every table in the org
// at exit code 0.
func TestGetDatasetsRejectsNonPositiveDestinationID(t *testing.T) {
	for _, value := range []string{"0", "-4"} {
		t.Run(value, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"results": []interface{}{}, "total_rows": 0, "page": 1,
				})
			})

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupTestCommand()
			cmd.SetArgs([]string{"get", "tables", "--destination-id", value})

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected an error for --destination-id %s", value)
			}
			if !strings.Contains(err.Error(), "--destination-id must be a positive integer") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if calls != 0 {
				t.Errorf("expected /datasets never to be called, got %d call(s)", calls)
			}
		})
	}
}

// The ticket's repro: an account that cannot see the org's tables got exit 3
// and prose on stdout for every format.
func TestGetTablesNoVisibleTablesIsAnEmptyResult(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		wantStdout            string
		wantStderrContains    []string
		wantStderrNotContains []string
	}{
		{
			name:               "json emits an empty List with no total",
			args:               []string{"get", "tables", "--output", "json"},
			wantStdout:         "{\n  \"items\": [],\n  \"kind\": \"List\",\n  \"limit\": 100,\n  \"next_page\": null,\n  \"page\": 1,\n  \"total\": null\n}",
			wantStderrContains: []string{"role: agent", "table owner or admin", "re-run the same command"},
		},
		{
			name:               "yaml emits an empty List with no total",
			args:               []string{"get", "tables", "--output", "yaml"},
			wantStdout:         "items: []\nkind: List\nlimit: 100\nnext_page: null\npage: 1\ntotal: null",
			wantStderrContains: []string{"role: agent", "table owner or admin", "re-run the same command"},
		},
		{
			name:               "name emits no rows",
			args:               []string{"get", "tables", "--output", "name"},
			wantStderrContains: []string{"role: agent", "table owner or admin", "re-run the same command"},
		},
		{
			name: "table format explains why the list is empty",
			args: []string{"get", "tables"},
			wantStderrContains: []string{
				"No tables found",
				"role: agent", "table owner or admin", "re-run the same command",
			},
			// The backend's UI copy must not reach the user for an
			// EmptyState code -- it names a menu item and asserts something
			// the server did not actually confirm.
			wantStderrNotContains: []string{"File > New Table", "Welcome"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, noTableBody)
				})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users/me", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": testOrgID, "role": "agent"})
			})

			cmd := setupTestCommand()
			cmd.SetArgs(tt.args)

			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("expected success, got %v (exit %d)", err, qerrors.GetExitCode(err))
			}

			if got := strings.TrimSpace(stdout.String()); got != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", got, tt.wantStdout)
			}
			// Structured output carries data only, never prose.
			for _, s := range []string{"Welcome", "New Table", "NO_TABLE_MESSAGE"} {
				if strings.Contains(stdout.String(), s) {
					t.Errorf("backend prose %q leaked onto stdout:\n%s", s, stdout.String())
				}
			}
			for _, s := range tt.wantStderrContains {
				if !strings.Contains(stderr.String(), s) {
					t.Errorf("stderr missing %q:\n%s", s, stderr.String())
				}
			}
			for _, s := range tt.wantStderrNotContains {
				if strings.Contains(stderr.String(), s) {
					t.Errorf("stderr must not contain %q:\n%s", s, stderr.String())
				}
			}
		})
	}
}
