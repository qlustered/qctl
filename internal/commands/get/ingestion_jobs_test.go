package get

import (
	"bytes"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupTestCommandForIngestion creates a root command with global flags and adds the ingestion-jobs command
func setupTestCommandForIngestion() *cobra.Command {
	rootCmd := newTestRootCmd()
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")

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
		FileName:       testutil.StringPtr("users_2025_01_15.csv"),
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
		FileName:       testutil.StringPtr("products_2025_01_16.csv"),
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
			// dataset_id, not table_id: --order-by is cast straight to
			// api.IngestionJobOrderBy, so it speaks the wire's names.
			name: "with sorting",
			args: []string{"--order-by", "dataset_id", "--reverse"},
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

// The allowlist used to offer table_id, created_at, started_at and
// finished_at, none of which api.IngestionJobOrderBy has: qctl waved them
// through and the server refused them.
func TestGetIngestionJobsSortFieldsMatchTheAPIEnum(t *testing.T) {
	valid := make(map[string]bool, len(validIngestionJobSortFields))
	for _, f := range validIngestionJobSortFields {
		valid[f] = true
	}

	for _, field := range []string{"table_id", "created_at", "started_at", "finished_at"} {
		if valid[field] {
			t.Errorf("%q is not an api.IngestionJobOrderBy member and must be rejected", field)
		}
		err := validateSortField(field, validIngestionJobSortFields)
		if err == nil {
			t.Fatalf("expected %q to be rejected client-side", field)
		}
		// The message has to name a replacement, or the user's only recourse
		// is guessing.
		if !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), "dataset_id") {
			t.Errorf("unhelpful message for %q: %v", field, err)
		}
		// A typo'd --order-by is bad usage (2), not a generic failure (1) —
		// the same code --columns already returns. A script that reads 1 here
		// treats a permanent input error as a transient server one.
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("exit code for %q = %d, want %d (ExitBadUsage)", field, got, qerrors.ExitBadUsage)
		}
	}

	for _, field := range []string{
		string(api.IngestionJobOrderByDatasetID),
		string(api.IngestionJobOrderByUpdatedAt),
		string(api.IngestionJobOrderByWhodunitID),
		string(api.IngestionJobOrderByBadRowsCount),
	} {
		if err := validateSortField(field, validIngestionJobSortFields); err != nil {
			t.Errorf("enum member %q must be accepted: %v", field, err)
		}
	}

	// --help must not advertise anything the validator would reject.
	orderBy := NewIngestionJobsCommand().Flags().Lookup("order-by")
	if !strings.Contains(orderBy.Usage, "dataset_id") || strings.Contains(orderBy.Usage, "table_id") {
		t.Errorf("--order-by help disagrees with the allowlist: %q", orderBy.Usage)
	}
	if !valid[orderBy.DefValue] {
		t.Errorf("--order-by default %q is not a valid sort field", orderBy.DefValue)
	}
}

func TestGetIngestionJobsCommand_NotLoggedIn(t *testing.T) {
	// Test that command fails when not logged in
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

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

// TestGetIngestionJobsCommand_BadFlagValuesShareOneExitCode measures the
// guarantee where the user meets it — through cmd.Execute(), not through the
// validator in isolation. Both flags name something that does not exist, so
// both are bad usage; --columns exited 2 and --order-by exited 1, which meant a
// script had to know which flag it typo'd to interpret the failure. Asserting
// they are *equal*, not merely each 2, is what keeps them from drifting apart
// again.
func TestGetIngestionJobsCommand_BadFlagValuesShareOneExitCode(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// --columns is only reachable if the request succeeds; --order-by fails
	// before any request is made.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsListResponse{
			Results:   []ingestion.IngestionJobTiny{sampleIngestionJobTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	run := func(t *testing.T, args ...string) error {
		t.Helper()
		cmd := setupTestCommandForIngestion()
		cmd.SetArgs(append([]string{"get", "ingestion-jobs"}, args...))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("expected an error for %v, got nil (output: %q)", args, buf.String())
		}
		return err
	}

	columnsErr := run(t, "--output", "table", "--columns", "bogus")
	orderByErr := run(t, "--order-by", "bogus")

	columnsCode := qerrors.GetExitCode(columnsErr)
	orderByCode := qerrors.GetExitCode(orderByErr)

	if columnsCode != orderByCode {
		t.Errorf("--columns bogus exits %d but --order-by bogus exits %d; both are bad usage and must agree (%v / %v)",
			columnsCode, orderByCode, columnsErr, orderByErr)
	}
	if columnsCode != qerrors.ExitBadUsage {
		t.Errorf("--columns bogus exit code = %d, want %d (ExitBadUsage)", columnsCode, qerrors.ExitBadUsage)
	}
	if orderByCode != qerrors.ExitBadUsage {
		t.Errorf("--order-by bogus exit code = %d, want %d (ExitBadUsage)", orderByCode, qerrors.ExitBadUsage)
	}
}

// The conflicting-flag check runs in RunE before anything else, so it is the
// one bad-usage path a command can hit without a server at all.
func TestGetIngestionJobsCommand_ConflictingFlagsExitBadUsage(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--state", "running", "--states", "running,finished"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when --state and --states are both passed")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

// No handler is registered: a request that escapes validation 404s, which
// would show up as exit 3 rather than exit 2.
func TestGetIngestionJobsCommand_InvalidStateExitsBadUsage(t *testing.T) {
	cases := map[string][]string{
		"unknown state":         {"--state", "failed"},
		"unknown states member": {"--states", "pending,failed"},
		"empty states member":   {"--states", "pending,"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupTestCommandForIngestion()
			cmd.SetArgs(append([]string{"get", "ingestion-jobs"}, args...))
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected an error for %v", args)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage); err = %v", got, qerrors.ExitBadUsage, err)
			}
			if got := len(mock.Requests()); got != 0 {
				t.Errorf("made %d request(s), want 0", got)
			}
		})
	}
}

// Each --states member is trimmed and sent as its own state parameter.
func TestGetIngestionJobsCommand_StatesReachWire(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Query()["state"], []string{"staged", "pending"}; !slices.Equal(got, want) {
			t.Errorf("state = %q, want %q", got, want)
		}
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsListResponse{
			Results:   []ingestion.IngestionJobTiny{},
			TotalRows: testutil.IntPtr(0),
			Page:      testutil.IntPtr(1),
		})
	})

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--states", "staged, pending"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(mock.Requests()); got != 1 {
		t.Errorf("made %d request(s), want 1", got)
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
		response := ingestion.IngestionJobsListResponse{
			Results:   []ingestion.IngestionJobTiny{sampleIngestionJobTiny()},
			TotalRows: &totalRows,
			Page:      &pageNum,
		}

		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--limit", "1", "--output", "json"})

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
	decodeListItems(t, stdout.Bytes(), &results)

	if len(results) != 1 {
		t.Errorf("Expected 1 ingestion job in output (single page), got %d", len(results))
	}

	// atlas sends no next off its cursor path; total_rows alone says page 2 exists.
	assertEnvelopeNextPage(t, stdout.Bytes(), stderr.String(), 2)
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
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--output", "table", "--columns", "id,dataset_id,state"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Verify table contains ID, DATASET-ID, STATE headers
	if !strings.Contains(output, "ID") || !strings.Contains(output, "DATASET-ID") || !strings.Contains(output, "STATE") {
		t.Errorf("Expected ID, DATASET-ID, STATE columns in table output")
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

// Each of these filters used to be dropped when passed as 0, returning every
// ingestion job in the org at exit code 0 — the answer looks legitimate and is
// silently about the wrong scope.
func TestGetIngestionJobsRejectsNonPositiveIDFilters(t *testing.T) {
	cases := []struct{ flag, value string }{
		{"--table-id", "0"},
		{"--table-id", "-3"},
		{"--cloud-source-id", "0"},
		{"--file-id", "0"},
	}

	for _, tc := range cases {
		t.Run(tc.flag+"="+tc.value, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"results": []interface{}{}, "total_rows": 0, "page": 1,
				})
			})

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupTestCommandForIngestion()
			cmd.SetArgs([]string{"get", "ingestion-jobs", tc.flag, tc.value})

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected an error for %s %s", tc.flag, tc.value)
			}
			if !strings.Contains(err.Error(), tc.flag+" must be a positive integer") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if calls != 0 {
				t.Errorf("expected /ingestion-jobs never to be called, got %d call(s)", calls)
			}
		})
	}
}

func TestGetIngestionJobsOrderByDeletedLinesCountReachesWire(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("order_by"); got != "deleted_lines_count" {
			t.Errorf("order_by = %q, want deleted_lines_count", got)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--order-by", "deleted_lines_count"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(mock.Requests()); got != 1 {
		t.Errorf("made %d request(s), want 1", got)
	}
}

func TestGetIngestionJobsCreatedByFilterReachesWire(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	const creatorID = "11111111-2222-3333-4444-555555555555"
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("whodunit_id"); got != creatorID {
			t.Errorf("whodunit_id = %q, want %q", got, creatorID)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--created-by", creatorID})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// newIngestionJobsWireEnv serves pages of jobs and records each request's
// query; it returns the recorded queries.
func newIngestionJobsWireEnv(t *testing.T, jobs ...ingestion.IngestionJobTiny) *[]url.Values {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	var queries []url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		total := len(jobs)
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsListResponse{
			Results: append([]ingestion.IngestionJobTiny{}, jobs...), TotalRows: &total,
		})
	})
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return &queries
}

func runGetIngestionJobs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupTestCommandForIngestion()
	cmd.SetArgs(append([]string{"get", "ingestion-jobs"}, args...))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	return buf.String(), err
}

func TestGetIngestionJobsKindAndFileFiltersReachWire(t *testing.T) {
	queries := newIngestionJobsWireEnv(t)
	if _, err := runGetIngestionJobs(t, "--kind", "dry_run", "--kind", "composite_dry_run", "--kind", "dry_run", "--file-id", "101"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(*queries) != 1 {
		t.Fatalf("made %d request(s), want 1", len(*queries))
	}
	q := (*queries)[0]
	if got, want := q["kind"], []string{"dry_run", "composite_dry_run"}; !slices.Equal(got, want) {
		t.Errorf("kind = %v, want %v", got, want)
	}
	if got := q.Get("stored_item_id"); got != "101" {
		t.Errorf("stored_item_id = %q, want 101", got)
	}
}

func TestGetIngestionJobsRefusesAnUnknownKindBeforeAnyRequest(t *testing.T) {
	queries := newIngestionJobsWireEnv(t)
	_, err := runGetIngestionJobs(t, "--kind", "file", "--kind", "carrier")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
	}
	if len(*queries) != 0 {
		t.Errorf("made %d request(s), want none", len(*queries))
	}
}

// A non-file job has a null file_name: the default table shows its kind and
// leaves FILE-NAME blank.
func TestGetIngestionJobsShowsKindAndABlankFileNameForANonFileJob(t *testing.T) {
	reprocess := sampleIngestionJobTiny2()
	reprocess.Kind = api.IngestionJobKindQuarantineReprocess
	reprocess.FileName = nil
	file := sampleIngestionJobTiny()
	file.Kind = api.IngestionJobKindFile
	newIngestionJobsWireEnv(t, file, reprocess)

	out, err := runGetIngestionJobs(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var header string
	rows := map[string][]string{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "ID" {
			header = line
			continue
		}
		rows[fields[0]] = fields
	}
	if got, want := strings.Fields(header), []string{"ID", "DATASET-ID", "STATE", "KIND", "FILE-NAME", "UPDATED-AT"}; !slices.Equal(got[:len(want)], want) {
		t.Fatalf("header = %q, want it to start %v", header, want)
	}
	if got := rows["1"]; len(got) < 5 || got[3] != "file" || got[4] != "users_2025_01_15.csv" {
		t.Errorf("file job row = %v, want kind file and its file name", got)
	}
	if got := rows["2"]; len(got) < 4 || got[3] != "quarantine_reprocess" || strings.Contains(out, "products_2025_01_16.csv") || strings.Contains(out, "<nil>") {
		t.Errorf("reprocess row = %v, want kind quarantine_reprocess and no file name\n%s", got, out)
	}
}

// --table is a NAME. The fixture is named "42" and has ID 7 — the one case an
// id-first resolver can never serve, so an Atoi short-circuit cannot pass here.
func TestGetIngestionJobsAcceptsTableName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var lookedUp, gotQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		lookedUp = r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
			Results: []api.DataSetTinySchema{{ID: 7, Name: "42"}},
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommandForIngestion()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--table", "42"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
	if !strings.Contains(gotQuery, "dataset_id=7") {
		t.Errorf("--table 42 must resolve to dataset_id=7, query: %s", gotQuery)
	}
}

// A locally-invalid --order-by must fail before --table pays for a name
// lookup that can only be discarded. Resolving --table before
// parseIngestionJobsParams runs makes this test fail: the trap fires because
// the lookup runs first.
func TestGetIngestionJobsInvalidSortFieldSkipsTableLookup(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		t.Error("a bad --order-by must fail before --table is looked up")
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		t.Error("a bad --order-by must fail before the listing request")
	})

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommandForIngestion()
	cmd.SetArgs([]string{"get", "ingestion-jobs", "--table", "nope", "--order-by", "bogus"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid sort field") {
		t.Fatalf("expected the sort-field error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}
