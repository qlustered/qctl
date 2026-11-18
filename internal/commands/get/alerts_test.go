package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
		AffectedStoredItems:   &affectedStoredItems,
		Whodunit:              nil,
		AssignedUser:          nil,
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
			args: []string{"--resolved=false"},
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
			name: "with is-row-level filter false",
			args: []string{"--is-row-level=false"},
			mockAlerts: []alerts.AlertTiny{
				sampleAlertTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"user_data"},
		},
		{
			name: "with resolvable-by-user filter",
			args: []string{"--resolvable-by-user=true"},
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

func TestGetAlertsCommand_TableSelectorErrorsBeatMissingCredentials(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "non-positive id",
			args: []string{"get", "alerts", "--table-id", "0"},
			want: "--table-id must be a positive integer",
		},
		{
			name: "conflicting selectors",
			args: []string{"get", "alerts", "--table", "orders", "--table-id", "42"},
			want: "table specified more than once (--table, --table-id)",
		},
		{
			name: "non-positive legacy alias",
			args: []string{"get", "alerts", "--dataset-id", "0"},
			want: "--dataset-id must be a positive integer",
		},
		{
			name: "legacy alias conflicts with table selector",
			args: []string{"get", "alerts", "--dataset-id", "42", "--table", "orders"},
			want: "--dataset-id is an alias for --table-id; pass one, not both",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := setupAlertsTestCommand()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
			}
		})
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

// TestGetAlertsCommand_Pagination serves a page the way atlas's OFFSET path
// does (get_paginated_results_order_by, pagination.py): total_rows and no
// next. The total alone has to say a page follows.
func TestGetAlertsCommand_Pagination(t *testing.T) {
	tests := []struct {
		name       string
		format     string
		wantHint   string
		wantNoHint bool
	}{
		{name: "json carries the paging in the envelope", format: "json", wantNoHint: true},
		{name: "yaml carries the paging in the envelope", format: "yaml", wantNoHint: true},
		{name: "table hints on stderr", format: "table",
			wantHint: "Showing 1-2 of 250 alerts (page 1 of 125). More available: use --page 2 or add filters.\n"},
		{name: "name hints on stderr", format: "name",
			wantHint: "Showing 1-2 of 250 alerts (page 1 of 125). More available: use --page 2 or add filters.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newAlertsTestEnv(t)
			requestCount := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, _ *http.Request) {
				requestCount++
				totalRows, pageNum := 250, 1
				second := sampleAlertTiny()
				second.ID = 2
				testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{
					Results:   []alerts.AlertTiny{sampleAlertTiny(), second},
					TotalRows: &totalRows,
					Page:      &pageNum,
				})
			})

			cmd := setupAlertsTestCommand()
			cmd.SetArgs([]string{"get", "alerts", "--limit", "2", "-o", tt.format})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if requestCount != 1 {
				t.Errorf("requests = %d, want 1 (no auto-pagination)", requestCount)
			}
			if tt.wantNoHint && stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing: the envelope carries the paging", stderr.String())
			}
			if tt.wantHint != "" && stderr.String() != tt.wantHint {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantHint)
			}

			var list output.List
			var items []map[string]interface{}
			switch tt.format {
			case "json":
				if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
					t.Fatalf("-o json did not decode as a List: %v\n%s", err, stdout.String())
				}
			case "yaml":
				if err := yaml.Unmarshal(stdout.Bytes(), &list); err != nil {
					t.Fatalf("-o yaml did not decode as a List: %v\n%s", err, stdout.String())
				}
			default:
				return
			}
			raw, _ := json.Marshal(list.Items)
			_ = json.Unmarshal(raw, &items)
			if list.Kind != "List" || len(items) != 2 {
				t.Errorf("kind = %q, %d items; want List, 2", list.Kind, len(items))
			}
			for name, got := range map[string]*int{"page": list.Page, "limit": list.Limit, "total": list.Total, "next_page": list.NextPage} {
				want := map[string]int{"page": 1, "limit": 2, "total": 250, "next_page": 2}[name]
				if got == nil || *got != want {
					t.Errorf("%s = %v, want %d", name, got, want)
				}
			}
		})
	}
}

// The last page has no next_page, and json and yaml write it as null rather
// than leaving the key out.
func TestGetAlertsLastPageHasNullNextPage(t *testing.T) {
	mock := newAlertsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, _ *http.Request) {
		totalRows, pageNum := 3, 2
		testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{
			Results: []alerts.AlertTiny{sampleAlertTiny()}, TotalRows: &totalRows, Page: &pageNum,
		})
	})
	cmd := setupAlertsTestCommand()
	cmd.SetArgs([]string{"get", "alerts", "--limit", "2", "--page", "2", "-o", "json"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("-o json did not decode: %v", err)
	}
	if v, ok := raw["next_page"]; !ok || v != nil {
		t.Errorf("next_page = %v (present %v), want null", v, ok)
	}
	if raw["page"] != float64(2) || raw["total"] != float64(3) {
		t.Errorf("page/total = %v/%v, want 2/3", raw["page"], raw["total"])
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

// runGetAlerts drives `get alerts` through a fresh root, the way the shell
// does. These cases assert on the request qctl sent, not on what it printed,
// so the buffers exist only to keep the output off the test log.
func runGetAlerts(t *testing.T, args ...string) error {
	t.Helper()
	cmd := setupAlertsTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "alerts"}, args...))
	return cmd.Execute()
}

// newAlertsTestEnv wires config, credentials and a mock server for one test and
// hands back the mock so the test can register the routes it cares about.
func newAlertsTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(func() {
		mock.Close()
		env.Cleanup()
	})
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func TestGetAlertsAcceptsTableID(t *testing.T) {
	mock := newAlertsTestEnv(t)
	var gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("--table-id must not trigger a name lookup")
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{})
		})

	if err := runGetAlerts(t, "--table-id", "42"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "dataset_id=42") {
		t.Errorf("--table-id must send dataset_id=42, query: %s", gotQuery)
	}
}

func TestGetAlertsAcceptsTableName(t *testing.T) {
	mock := newAlertsTestEnv(t)
	var lookedUp, gotQuery string
	// The fixture's NAME is the numeric string "42" and its ID is 7 — the one
	// case an overloaded name-or-ID flag can never serve.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			lookedUp = r.URL.Query().Get("name")
			testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
				Results: []api.DataSetTinySchema{{ID: 7, Name: "42"}},
			})
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{})
		})

	if err := runGetAlerts(t, "--table", "42"); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
	if !strings.Contains(gotQuery, "dataset_id=7") {
		t.Errorf("--table 42 must resolve to dataset_id=7, query: %s", gotQuery)
	}
}

func TestGetAlertsDatasetIDStillWorksAndIsHidden(t *testing.T) {
	mock := newAlertsTestEnv(t)
	var gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{})
		})

	if err := runGetAlerts(t, "--dataset-id", "42"); err != nil {
		t.Fatalf("the old spelling must keep working: %v", err)
	}
	if !strings.Contains(gotQuery, "dataset_id=42") {
		t.Errorf("--dataset-id must still send dataset_id=42, query: %s", gotQuery)
	}

	flag := NewAlertsCommand().Flags().Lookup("dataset-id")
	if flag == nil {
		t.Fatal("--dataset-id must survive as an alias")
	}
	if !flag.Hidden {
		t.Error("--dataset-id must be hidden: --table-id is the documented spelling")
	}
}

// Both spellings at once is a contradiction, not a precedence question — and a
// script needs exit 2 to tell it from a retryable server failure.
func TestGetAlertsRejectsDatasetIDWithTableSelector(t *testing.T) {
	for _, selector := range [][]string{{"--table-id", "7"}, {"--table", "orders"}} {
		t.Run(selector[0], func(t *testing.T) {
			mock := newAlertsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
				func(_ http.ResponseWriter, _ *http.Request) {
					t.Error("a contradictory request must fail before any lookup")
				})
			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts",
				func(w http.ResponseWriter, _ *http.Request) {
					calls++
					testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{})
				})

			err := runGetAlerts(t, append([]string{"--dataset-id", "42"}, selector...)...)
			if err == nil || !strings.Contains(err.Error(), "--dataset-id is an alias for --table-id; pass one, not both") {
				t.Fatalf("expected the alias-conflict error, got: %v", err)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
			}
			if calls != 0 {
				t.Errorf("expected /alerts never to be called, got %d call(s)", calls)
			}
		})
	}
}

// A locally-invalid --order-by must fail before --table pays for a name
// lookup that can only be discarded. Resolving --table before
// parseAlertsParams runs makes this test fail: the trap fires because the
// lookup runs first.
func TestGetAlertsInvalidSortFieldSkipsTableLookup(t *testing.T) {
	mock := newAlertsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a bad --order-by must fail before --table is looked up")
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a bad --order-by must fail before the listing request")
		})

	err := runGetAlerts(t, "--table", "nope", "--order-by", "bogus")
	if err == nil || !strings.Contains(err.Error(), "invalid sort field") {
		t.Fatalf("expected the sort-field error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

// --dataset-id 0 used to drop the filter and return every alert in the org at
// exit code 0.
func TestGetAlertsRejectsNonPositiveDatasetID(t *testing.T) {
	for _, value := range []string{"0", "-7"} {
		t.Run(value, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"results": []interface{}{}, "total_rows": 0, "page": 1,
				})
			})

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupAlertsTestCommand()
			cmd.SetArgs([]string{"get", "alerts", "--dataset-id", value})

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected an error for --dataset-id %s", value)
			}
			if !strings.Contains(err.Error(), "--dataset-id must be a positive integer") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if calls != 0 {
				t.Errorf("expected /alerts never to be called, got %d call(s)", calls)
			}
		})
	}
}

func TestAlertsIsRowLevelSendsTheParam(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string // expected is_row_level query value; "" means not sent
	}{
		{name: "bare flag sends true", args: []string{"--is-row-level"}, want: "true"},
		{name: "explicit false sends false", args: []string{"--is-row-level=false"}, want: "false"},
		{name: "unset sends nothing", args: []string{}, want: ""},
		{name: "hidden alias still works", args: []string{"--is-muted"}, want: "true"},
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

			var got string
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Query().Get("is_row_level")
				totalRows := 0
				page := 1
				testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{
					Results: []alerts.AlertTiny{}, TotalRows: &totalRows, Page: &page,
				})
			})

			cmd := setupAlertsTestCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(append([]string{"get", "alerts"}, tt.args...))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got != tt.want {
				t.Errorf("is_row_level = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAlertsRejectsBothMutedSpellings(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupAlertsTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "alerts", "--is-row-level", "--is-muted"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want an error when both spellings are passed")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestAlertsIsMutedIsHidden(t *testing.T) {
	cmd := NewAlertsCommand()
	flag := cmd.Flags().Lookup("is-muted")
	if flag == nil {
		t.Fatal("--is-muted must survive as an alias")
	}
	if !flag.Hidden {
		t.Error("--is-muted must be hidden so --help teaches --is-row-level")
	}
}

// TestGetAlertsCommand_EmptyWithFilterNamesTheFilter pins that the shared
// empty-result helper reaches past `get rules`: any of the 17 list commands
// that hands printEmptyResult an empty set reports the filter that emptied it.
func TestGetAlertsCommand_EmptyWithFilterNamesTheFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 0
		page := 1
		testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{
			Results:   []alerts.AlertTiny{},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	cmd := setupAlertsTestCommand()
	cmd.SetArgs([]string{"get", "alerts", "--search", "nonexistent", "--limit", "5"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := `No alerts matched --search "nonexistent" in org "` + testOrgID + `" (context: "default").` + "\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if got := strings.TrimSpace(stdout.String()); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
}

// TestGetAlertsShortSearchRejected pins that the 3-character minimum belongs to
// the shared parser, not to get rules: these two commands share no code on this
// path except parseSearchFlag. The --table trap pins the placement too -- the
// rejection is free, and must not come after a name lookup that costs a
// request and can only be thrown away.
func TestGetAlertsShortSearchRejected(t *testing.T) {
	mock := newAlertsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a search the server would ignore must fail before --table is looked up")
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a search the server would ignore must fail before the listing request")
		})

	err := runGetAlerts(t, "--table", "orders", "--search", "ab")
	if err == nil {
		t.Fatal("runGetAlerts(--table orders --search ab) error = nil, want the short-search error")
	}
	want := `--search "ab" is too short (minimum 3 characters); the server ignores shorter queries ` +
		`and would return unfiltered results. Widen the search or drop --search.`
	if err.Error() != want {
		t.Errorf("error = %q,\nwant %q", err.Error(), want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

// D1: --issue-type is forwarded to the server, never checked locally.
func TestGetAlertsIssueTypeIsForwardedToTheServer(t *testing.T) {
	mock := newAlertsTestEnv(t)
	var gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{})
		})

	if err := runGetAlerts(t, "--issue-type", "severe_rule_bug"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "issue_type=severe_rule_bug") {
		t.Errorf("--issue-type must send issue_type=severe_rule_bug, query: %s", gotQuery)
	}
}

// An absent flag must not send an empty filter: issue_type= is a value the
// backend would try to parse, not the absence of a filter.
func TestGetAlertsWithoutIssueTypeSendsNoFilter(t *testing.T) {
	mock := newAlertsTestEnv(t)
	var gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{})
		})

	if err := runGetAlerts(t); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(gotQuery, "issue_type") {
		t.Errorf("no --issue-type must send no issue_type filter, query: %s", gotQuery)
	}
}

// D1, the other half: an unknown value is the SERVER's rejection, so it must
// reach the server and come back as a 422 (exit 2), not be refused locally.
// atlas's RequestValidationError handler (request_validation_exception_handler,
// capi/capi/main.py) sends the list-shaped detail this fixture copies.
func TestGetAlertsUnknownIssueTypeIsRejectedByTheServer(t *testing.T) {
	mock := newAlertsTestEnv(t)
	var reached bool
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts",
		func(w http.ResponseWriter, r *http.Request) {
			reached = true
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": []map[string]interface{}{{
					"type": "enum",
					"loc":  []interface{}{"query", "issue_type"},
					"msg":  "Input should be 'access_denied', 'anomaly' or 'severe_rule_bug'",
				}},
			})
		})

	err := runGetAlerts(t, "--issue-type", "not_a_type")
	if err == nil {
		t.Fatal("expected the server's 422 to surface as an error")
	}
	if !reached {
		t.Error("--issue-type must not be validated client-side; the request never reached the server")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestCompleteAlertIssueTypeOffersTheGeneratedList(t *testing.T) {
	got, directive := completeAlertIssueType(nil, nil, "severe")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if len(got) != 1 || got[0] != "severe_rule_bug" {
		t.Errorf("completion for %q = %v, want [severe_rule_bug]", "severe", got)
	}

	all, _ := completeAlertIssueType(nil, nil, "")
	if len(all) != len(api.IssueTypeAlertActions) {
		t.Errorf("empty prefix completed %d values, want all %d", len(all), len(api.IssueTypeAlertActions))
	}
	if !sort.StringsAreSorted(all) {
		t.Error("completion output must be sorted; map iteration order is random")
	}
	// An actionless issue type is still a filterable one.
	var sawActionless bool
	for _, name := range all {
		if name == "ran_out_of_memory" {
			sawActionless = true
		}
	}
	if !sawActionless {
		t.Error("completion dropped ran_out_of_memory, which is a real issue type with no actions")
	}
}

// Registration is the half the function above cannot prove: an unregistered
// completion function is dead code that still passes its own unit test.
func TestIssueTypeFlagHasCompletionRegistered(t *testing.T) {
	cmd := NewAlertsCommand()
	if _, ok := cmd.GetFlagCompletionFunc("issue-type"); !ok {
		t.Error("--issue-type has no completion function registered")
	}
}

// runGetAlertsPage runs `get alerts` against a mock answering with results and
// returns stdout.
func runGetAlertsPage(t *testing.T, results []alerts.AlertTiny, args ...string) string {
	t.Helper()
	mock := newAlertsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, _ *http.Request) {
		total, page := len(results), 1
		testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{Results: results, TotalRows: &total, Page: &page})
	})
	cmd := setupAlertsTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "alerts"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, errOut.String())
	}
	return out.String()
}

// cappedAlertTiny is a row-level alert whose count sits at the cap.
func cappedAlertTiny() alerts.AlertTiny {
	a := sampleAlertTiny()
	a.Count = 1000
	capped := true
	a.CountCapped = &capped
	return a
}

// A capped count means "at least that many", and the table says so; the
// machine views keep count a number and carry count_capped beside it.
func TestGetAlertsMarksACappedCount(t *testing.T) {
	uncapped := sampleAlertTiny()
	uncapped.ID = 2

	table := runGetAlertsPage(t, []alerts.AlertTiny{cappedAlertTiny(), uncapped}, "--columns", "id,count,count_capped")
	lines := strings.Split(strings.TrimSpace(table), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", table)
	}
	if fields := strings.Fields(lines[1]); len(fields) < 2 || fields[1] != "1000+" {
		t.Errorf("capped row count cell = %v, want 1000+:\n%s", fields, table)
	}
	if fields := strings.Fields(lines[2]); len(fields) < 2 || fields[1] != "3" {
		t.Errorf("uncapped row count cell = %v, want 3:\n%s", fields, table)
	}

	var list struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal([]byte(runGetAlertsPage(t, []alerts.AlertTiny{cappedAlertTiny()}, "-o", "json")), &list); err != nil {
		t.Fatalf("-o json did not decode: %v", err)
	}
	if page := list.Items; len(page) != 1 || page[0]["count"] != float64(1000) || page[0]["count_capped"] != true {
		t.Errorf("-o json items = %v, want count 1000 (a number) and count_capped true", page)
	}

	if y := runGetAlertsPage(t, []alerts.AlertTiny{cappedAlertTiny()}, "-o", "yaml"); !strings.Contains(y, "count: 1000\n") || strings.Contains(y, "1000+") {
		t.Errorf("-o yaml should keep count a number, got:\n%s", y)
	}
}

// issueCellRows changes one cell and nothing else: the same columns, in the
// same order, with every other cell rendered as before.
func TestCountCellRowsChangesOnlyTheCountCell(t *testing.T) {
	full := cappedAlertTiny()
	score := 4.5
	full.ImpactScore = &score
	full.Whodunit = &api.UserInfoTinySchema{Email: "someone@example.com"}
	resolved := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	full.ResolvedAt = &resolved
	rows := []alerts.AlertTiny{full, sampleAlertTiny()}

	p := output.NewPrinter(output.Options{})
	projected := issueCellRows(rows)
	if got, want := output.AvailableColumns(projected, nil), output.AvailableColumns(rows, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	before, after := p.DataToRows(rows), p.DataToRows(projected)
	if len(before) != len(after) {
		t.Fatalf("row count changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		for col, cell := range before[i] {
			if col == "count" {
				continue
			}
			if after[i][col] != cell {
				t.Errorf("row %d column %s = %q, want %q", i, col, after[i][col], cell)
			}
		}
	}
	if after[0]["count"] != "1000+" || after[1]["count"] != "3" {
		t.Errorf("count cells = %q, %q, want 1000+, 3", after[0]["count"], after[1]["count"])
	}
}

// Rows issueCellRows cannot project come back as they went in, without a panic.
func TestCountCellRowsLeavesUnprojectableRowsUnchanged(t *testing.T) {
	type withUnexported struct {
		Count int `json:"count"`
		note  string
	}
	type noCount struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name string
		rows any
		run  func() any
	}{
		{"non-struct", []string{"a", "b"}, func() any { return issueCellRows([]string{"a", "b"}) }},
		{"unexported field", []withUnexported{{Count: 3, note: "x"}}, func() any { return issueCellRows([]withUnexported{{Count: 3, note: "x"}}) }},
		{"no Count", []noCount{{Name: "n"}}, func() any { return issueCellRows([]noCount{{Name: "n"}}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("issueCellRows panicked: %v", r)
				}
			}()
			if got := tt.run(); !reflect.DeepEqual(got, tt.rows) {
				t.Errorf("issueCellRows = %#v, want %#v unchanged", got, tt.rows)
			}
		})
	}
}
