package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/errorincidents"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupErrorIncidentsTestCommand creates a root command with global flags and adds the error-incidents command
func setupErrorIncidentsTestCommand() *cobra.Command {
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
	getCmd.AddCommand(NewErrorIncidentsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

// Helper to create sample error incident fixtures
func sampleErrorIncidentTiny() errorincidents.ErrorIncidentTiny {
	createdAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	jobName := "sensor-1"
	return errorincidents.ErrorIncidentTiny{
		ID:          1,
		Module:      "sensor",
		Msg:         "Access denied to resource",
		JobName:     &jobName,
		Count:       5,
		CreatedAt:   &createdAt,
		DatasetID:   nil,
		DatasetName: "",
	}
}

func sampleErrorIncidentTiny2() errorincidents.ErrorIncidentTiny {
	createdAt := time.Date(2024, 1, 14, 9, 0, 0, 0, time.UTC)
	jobName := "sensor-2"
	datasetID := 42
	return errorincidents.ErrorIncidentTiny{
		ID:          2,
		Module:      "ingestion",
		Msg:         "Connection timeout",
		JobName:     &jobName,
		Count:       3,
		CreatedAt:   &createdAt,
		DatasetID:   &datasetID,
		DatasetName: "my-dataset",
	}
}

func TestGetErrorIncidentsCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockErrorIncidents    []errorincidents.ErrorIncidentTiny
		wantErr               bool
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"sensor", "Access denied"},
		},
		{
			name: "with json output",
			args: []string{"--output", "json"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{`"module": "sensor"`, `"msg": "Access denied to resource"`},
		},
		{
			// msg is backend Markdown and can carry a value marker: the table
			// renders it, and -o json (the next case) keeps it as sent.
			name: "marked message is rendered in the table",
			args: []string{},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				func() errorincidents.ErrorIncidentTiny {
					e := sampleErrorIncidentTiny()
					e.Msg = markedMessage
					return e
				}(),
			},
			wantOutputContains:    []string{renderedMessage},
			wantOutputNotContains: []string{"~*", "*~", "~|", "**"},
		},
		{
			name: "marked message is sent as is in json",
			args: []string{"--output", "json"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				func() errorincidents.ErrorIncidentTiny {
					e := sampleErrorIncidentTiny()
					e.Msg = markedMessage
					return e
				}(),
			},
			wantOutputContains: []string{wireMessageJSON},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"module: sensor", "msg: Access denied to resource"},
		},
		{
			name: "with sorting",
			args: []string{"--order-by", "module", "--reverse"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
				sampleErrorIncidentTiny2(),
			},
			wantErr:            false,
			wantOutputContains: []string{"sensor", "ingestion"},
		},
		{
			name: "multiple error incidents",
			args: []string{},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
				sampleErrorIncidentTiny2(),
			},
			wantErr:            false,
			wantOutputContains: []string{"sensor", "ingestion"},
		},
		{
			name:               "empty results",
			args:               []string{},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{},
			wantErr:            false,
			wantOutputContains: []string{}, // No output expected for empty results
		},
		{
			name:               "invalid sort field",
			args:               []string{"--order-by", "invalid_field"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{},
			wantErr:            true,
			wantOutputContains: []string{"invalid sort field"},
		},
		{
			name: "with limit",
			args: []string{"--limit", "1"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"sensor"},
		},
		{
			name: "with custom columns",
			args: []string{"--columns", "id,module,msg"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"sensor", "Access denied"},
		},
		{
			name: "with no-headers flag",
			args: []string{"--no-headers"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
			},
			wantErr:               false,
			wantOutputContains:    []string{"sensor"},
			wantOutputNotContains: []string{"MODULE", "MSG"},
		},
		{
			name: "with module filter",
			args: []string{"--module", "sensor"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"sensor"},
		},
		{
			name: "with job-name filter",
			args: []string{"--job-name", "sensor-1"},
			mockErrorIncidents: []errorincidents.ErrorIncidentTiny{
				sampleErrorIncidentTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"sensor-1"},
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

			// Register paginated handler for error incidents
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents", testutil.MockPaginatedHandler(
				func(page int) (interface{}, int, bool) {
					if page == 1 {
						return tt.mockErrorIncidents, len(tt.mockErrorIncidents), false
					}
					return []errorincidents.ErrorIncidentTiny{}, len(tt.mockErrorIncidents), false
				},
			))

			// Setup config and credentials
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			// Create and execute command
			rootCmd := setupErrorIncidentsTestCommand()
			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)

			args := append([]string{"get", "error-incidents"}, tt.args...)
			rootCmd.SetArgs(args)

			err := rootCmd.Execute()

			// Check error expectation
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				// Check error message contains expected strings
				for _, want := range tt.wantOutputContains {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error message should contain %q, got %q", want, err.Error())
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

func TestGetErrorIncidentsCommand_JSONOutput(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mockErrorIncidents := []errorincidents.ErrorIncidentTiny{
		sampleErrorIncidentTiny(),
	}

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents", testutil.MockPaginatedHandler(
		func(page int) (interface{}, int, bool) {
			if page == 1 {
				return mockErrorIncidents, len(mockErrorIncidents), false
			}
			return []errorincidents.ErrorIncidentTiny{}, len(mockErrorIncidents), false
		},
	))

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupErrorIncidentsTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"get", "error-incidents", "--output", "json"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify output is valid JSON
	var result []map[string]interface{}
	decodeListItems(t, stdout.Bytes(), &result)

	if len(result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result))
	}

	// Verify expected fields
	if result[0]["module"] != "sensor" {
		t.Errorf("expected module 'sensor', got %v", result[0]["module"])
	}
	if result[0]["msg"] != "Access denied to resource" {
		t.Errorf("expected msg 'Access denied to resource', got %v", result[0]["msg"])
	}
}

func TestGetErrorIncidentsCommand_Pagination(t *testing.T) {
	// Test that command shows pagination hint when more results are available
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Create error incident for first page
	error1 := sampleErrorIncidentTiny()

	pageRequests := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents", func(w http.ResponseWriter, r *http.Request) {
		pageRequests++

		// Return first page with indication that more results exist
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results":    []errorincidents.ErrorIncidentTiny{error1},
			"total_rows": 2,
			"page":       1,
		})
	})

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupErrorIncidentsTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"get", "error-incidents", "--limit", "1", "--output", "json"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify only 1 request is made (no auto-pagination)
	if pageRequests != 1 {
		t.Errorf("expected exactly 1 page request (no auto-pagination), got %d", pageRequests)
	}

	// Verify only first page error incident is in output
	output := stdout.String()
	if !strings.Contains(output, "sensor") {
		t.Errorf("expected first error incident in output, got:\n%s", output)
	}

	// atlas sends no next off its cursor path; total_rows alone says page 2 exists.
	assertEnvelopeNextPage(t, stdout.Bytes(), stderr.String(), 2)
}

func TestGetErrorIncidentsCommand_ServerError(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return server error
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents", testutil.MockInternalServerErrorHandler())

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupErrorIncidentsTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"get", "error-incidents"})

	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for server error response, got nil")
	}
}

func TestGetErrorIncidentsCommand_NotLoggedIn(t *testing.T) {
	// Setup test environment without credentials
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Use a static URL that won't have credentials in the keyring
	env.SetupConfigWithOrg("https://api.notloggedin.example.com", "", testOrgID)

	// Create and execute command
	rootCmd := setupErrorIncidentsTestCommand()
	rootCmd.SetArgs([]string{"get", "error-incidents"})

	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected 'not logged in' error, got: %v", err)
	}
}

func TestGetErrorIncidentsCommand_Aliases(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mockErrorIncidents := []errorincidents.ErrorIncidentTiny{
		sampleErrorIncidentTiny(),
	}

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents", testutil.MockPaginatedHandler(
		func(page int) (interface{}, int, bool) {
			if page == 1 {
				return mockErrorIncidents, len(mockErrorIncidents), false
			}
			return []errorincidents.ErrorIncidentTiny{}, len(mockErrorIncidents), false
		},
	))

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Test "errors" alias
	rootCmd := setupErrorIncidentsTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"get", "errors"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error using 'errors' alias: %v", err)
	}

	if !strings.Contains(stdout.String(), "sensor") {
		t.Errorf("expected output to contain 'sensor', got:\n%s", stdout.String())
	}
}

// atlas capi/views/error_view.py's get_errors filters
// `ErrorIncident.deleted == deleted` — an EQUALITY filter. --deleted
// therefore returns the deleted incidents only, never mixed with the active
// ones, so "Include deleted" is backwards.
func TestGetErrorIncidentsDeletedFlagHelpIsExclusive(t *testing.T) {
	cmd := NewErrorIncidentsCommand()

	flag := cmd.Flags().Lookup("deleted")
	if flag == nil {
		t.Fatal("get error-incidents has no --deleted flag; deleted incidents are unlistable from the CLI")
	}
	usage := flag.Usage
	if strings.Contains(strings.ToLower(usage), "include") {
		t.Errorf("--deleted usage still reads as additive: %q", usage)
	}
	if !strings.Contains(strings.ToLower(usage), "only") {
		t.Errorf("--deleted usage should say it lists deleted incidents only, got: %q", usage)
	}
	if strings.Contains(cmd.Long, "# Include deleted incidents") {
		t.Errorf("the --help example comment still reads as additive:\n%s", cmd.Long)
	}
}

// The second hand-inlined copy of the sort-field check. See the equivalent
// destinations test: both now route through validateSortField, so there is one
// message and one exit code rather than three near-identical implementations.
func TestGetErrorIncidentsCommand_InvalidSortFieldExitsBadUsage(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupErrorIncidentsTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"get", "error-incidents", "--order-by", "invalid_field"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected an error for an unknown --order-by value")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestGetErrorIncidentsTableSelector(t *testing.T) {
	for _, selector := range [][]string{{"--table", "42"}, {"--table-id", "42"}} {
		t.Run(strings.Join(selector, " "), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			env.SetupConfigWithOrg(mock.URL(), "", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")
			lookups := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				lookups++
				if r.URL.Query().Get("name") != "42" {
					t.Errorf("name lookup query = %s", r.URL.RawQuery)
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{
					"results": []map[string]any{{"id": 99, "name": "42", "state": "active"}}, "total_rows": 1,
				})
			})
			id42, id99 := 42, 99
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]any{
					"results": []errorincidents.ErrorIncidentTiny{{ID: 1, DatasetID: &id42}, {ID: 2, DatasetID: &id99}}, "total_rows": 2,
				})
			})
			cmd := setupErrorIncidentsTestCommand()
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs(append([]string{"get", "error-incidents", "-o", "json"}, selector...))
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var items []errorincidents.ErrorIncidentTiny
			decodeListItems(t, out.Bytes(), &items)
			wantID, wantLookups := 1, 0
			if selector[0] == "--table" {
				wantID, wantLookups = 2, 1
			}
			if len(items) != 1 || items[0].ID != wantID || lookups != wantLookups {
				t.Errorf("items = %v, lookups = %d; want incident %d and %d lookups", items, lookups, wantID, wantLookups)
			}
		})
	}
}
