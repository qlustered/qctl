package get

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// setupTableTestCommand creates a root command with global flags and the table command
func setupTableTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Enable verbose output")
	rootCmd.PersistentFlags().String("org", "", "Organization ID or name")

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewDatasetCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func TestJobActivityFromREST(t *testing.T) {
	resp := &datasets.JobRunningCountResponse{
		IngestionJobCount:        2,
		WaitingIngestionJobCount: 5,
		TrainingJobCount:         1,
		ServerTime:               1700000000,
	}

	activity := jobActivityFromREST(resp)

	if activity.RunningIngestionJobs != 2 {
		t.Errorf("RunningIngestionJobs = %d, want 2", activity.RunningIngestionJobs)
	}
	if activity.WaitingIngestionJobs != 5 {
		t.Errorf("WaitingIngestionJobs = %d, want 5", activity.WaitingIngestionJobs)
	}
	if activity.RunningProfilingJobs != 1 {
		t.Errorf("RunningProfilingJobs = %d, want 1", activity.RunningProfilingJobs)
	}
	if activity.ShouldRefreshDatagrid != false {
		t.Error("ShouldRefreshDatagrid should be false for REST")
	}
}

func TestJobActivityFromWS(t *testing.T) {
	raw := json.RawMessage(`{
		"running_ingestion_job_count": 3,
		"running_training_job_count": 0,
		"waiting_ingestion_job_count": 7,
		"should_refresh_datagrid": true
	}`)

	activity, err := jobActivityFromWS(raw)
	if err != nil {
		t.Fatalf("jobActivityFromWS error: %v", err)
	}

	if activity.RunningIngestionJobs != 3 {
		t.Errorf("RunningIngestionJobs = %d, want 3", activity.RunningIngestionJobs)
	}
	if activity.WaitingIngestionJobs != 7 {
		t.Errorf("WaitingIngestionJobs = %d, want 7", activity.WaitingIngestionJobs)
	}
	if activity.RunningProfilingJobs != 0 {
		t.Errorf("RunningProfilingJobs = %d, want 0", activity.RunningProfilingJobs)
	}
	if !activity.ShouldRefreshDatagrid {
		t.Error("ShouldRefreshDatagrid should be true")
	}
}

func TestJobActivityFromWS_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{invalid}`)
	_, err := jobActivityFromWS(raw)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestBuildWSPath(t *testing.T) {
	tests := []struct {
		orgID    string
		tableID  int
		provider string
		want     string
	}{
		{"org-123", 42, "first-party", "/api/orgs/org-123/ws/datasets/42/job-activity"},
		{"org-123", 42, "third-party", "/api/orgs/org-123/ws/third-party/datasets/42/job-activity"},
	}

	for _, tt := range tests {
		got := buildWSPath(tt.orgID, tt.tableID, tt.provider)
		if got != tt.want {
			t.Errorf("buildWSPath(%q, %d, %q) = %q, want %q", tt.orgID, tt.tableID, tt.provider, got, tt.want)
		}
	}
}

func TestOneShotTableOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/job-activity", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.JobRunningCountResponse{
			IngestionJobCount:        1,
			WaitingIngestionJobCount: 3,
			TrainingJobCount:         0,
			ServerTime:               1700000000,
		})
	})

	cmd := setupTableTestCommand()
	cmd.SetArgs([]string{"get", "table", "42", "--job-activity"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "ingestion: running=1 waiting=3") {
		t.Errorf("output missing expected ingestion line, got: %s", output)
	}
	if !strings.Contains(output, "profiling: running=0") {
		t.Errorf("output missing expected profiling line, got: %s", output)
	}
}

func TestOneShotJSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/job-activity", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.JobRunningCountResponse{
			IngestionJobCount:        2,
			WaitingIngestionJobCount: 0,
			TrainingJobCount:         1,
			ServerTime:               1700000000,
		})
	})

	cmd := setupTableTestCommand()
	cmd.SetArgs([]string{"get", "table", "42", "--job-activity", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, buf.String())
	}

	if result["running_ingestion_jobs"] != float64(2) {
		t.Errorf("running_ingestion_jobs = %v, want 2", result["running_ingestion_jobs"])
	}
	if result["running_profiling_jobs"] != float64(1) {
		t.Errorf("running_profiling_jobs = %v, want 1", result["running_profiling_jobs"])
	}
}

func TestOneShotYAMLOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/job-activity", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.JobRunningCountResponse{
			IngestionJobCount:        0,
			WaitingIngestionJobCount: 0,
			TrainingJobCount:         0,
			ServerTime:               1700000000,
		})
	})

	cmd := setupTableTestCommand()
	cmd.SetArgs([]string{"get", "table", "42", "--job-activity", "-o", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "running_ingestion_jobs: 0") {
		t.Errorf("YAML output missing running_ingestion_jobs, got: %s", output)
	}
	if !strings.Contains(output, "should_refresh_datagrid: false") {
		t.Errorf("YAML output missing should_refresh_datagrid, got: %s", output)
	}
}

func TestGetTable_DefaultShowsTableInfo(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	badRows := 5
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID:              42,
			Name:            "vehicles",
			State:           "active",
			DestinationName: "postgres_main",
			BadRowsCount:    &badRows,
		})
	})

	cmd := setupTableTestCommand()
	cmd.SetArgs([]string{"get", "table", "42"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "vehicles") {
		t.Errorf("output should contain table name 'vehicles', got: %s", output)
	}
	if !strings.Contains(output, "active") {
		t.Errorf("output should contain state 'active', got: %s", output)
	}
	if !strings.Contains(output, "postgres_main") {
		t.Errorf("output should contain destination 'postgres_main', got: %s", output)
	}
}

// The default grid is a contract, and it silently rotted once already:
// unresolved_alerts and progress_percent are not fields of DatasetFull, so
// naming them made every `qctl get table` fail --columns validation. Asserting
// the cell VALUES, not just the headers, is the point — a header above an
// empty column is exactly the wrong answer this defends against.
func TestGetTable_DefaultColumns(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	badRows, cleanRows := 5, 1234
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID:              42,
			Name:            "vehicles",
			State:           "active",
			DestinationName: "postgres_main",
			BadRowsCount:    &badRows,
			CleanRowsCount:  &cleanRows,
		})
	})

	cmd := setupTableTestCommand()
	cmd.SetArgs([]string{"get", "table", "42"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected a header and a data row, got: %q", buf.String())
	}
	headers := strings.Fields(lines[0])
	values := strings.Fields(lines[1])

	wantHeaders := []string{"NAME", "STATE", "DESTINATION-NAME", "BAD-ROWS-COUNT", "CLEAN-ROWS-COUNT"}
	if strings.Join(headers, " ") != strings.Join(wantHeaders, " ") {
		t.Errorf("default columns = %v, want %v", headers, wantHeaders)
	}
	// Every default column must carry a value: strings.Fields collapses runs
	// of spaces, so a blank cell shows up here as a short row.
	wantValues := []string{"vehicles", "active", "postgres_main", "5", "1234"}
	if strings.Join(values, " ") != strings.Join(wantValues, " ") {
		t.Errorf("row = %v, want %v (a blank cell means the column does not exist on DatasetFull)", values, wantValues)
	}
}

func TestGetTable_ByName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	badRows := 3
	// Name resolution: list API filtered by name
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsListResponse{
			Results: []datasets.DatasetTiny{
				{ID: 10, Name: "vehicles", State: "active", DestinationName: "pg"},
			},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})
	// Single dataset fetch
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/10", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID:              10,
			Name:            "vehicles",
			State:           "active",
			DestinationName: "pg",
			BadRowsCount:    &badRows,
		})
	})

	cmd := setupTableTestCommand()
	cmd.SetArgs([]string{"get", "table", "vehicles"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "vehicles") {
		t.Errorf("output should contain 'vehicles', got: %s", output)
	}
}

func TestGetTable_JSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	badRows := 7
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/10", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID:              10,
			Name:            "orders",
			State:           "active",
			DestinationName: "snowflake",
			BadRowsCount:    &badRows,
		})
	})

	cmd := setupTableTestCommand()
	cmd.SetArgs([]string{"get", "table", "10", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// -o json emits the apply-compatible manifest, not the raw response:
	// name lives under metadata, destination_name under status.
	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, buf.String())
	}
	metadata, ok := result["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metadata object, got: %v", result["metadata"])
	}
	if metadata["name"] != "orders" {
		t.Errorf("expected metadata.name 'orders', got: %v", metadata["name"])
	}
	status, ok := result["status"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected status object, got: %v", result["status"])
	}
	if status["destination_name"] != "snowflake" {
		t.Errorf("expected status.destination_name 'snowflake', got: %v", status["destination_name"])
	}
}

// jobActivityWatchCmd is a command carrying the flags newJobActivityWriter
// reads, writing to out.
func jobActivityWatchCmd(format string, out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("output", format, "")
	cmd.Flags().Bool("allow-plaintext-secrets", false, "")
	cmd.SetOut(out)
	return cmd
}

// writeJobActivityUpdates hands each websocket message to the watch's writer.
func writeJobActivityUpdates(t *testing.T, format string, changesOnly bool, messages ...string) string {
	t.Helper()
	var out bytes.Buffer
	write, _, err := newJobActivityWriter(jobActivityWatchCmd(format, &out), 42, changesOnly)
	if err != nil {
		t.Fatalf("newJobActivityWriter: %v", err)
	}
	for _, raw := range messages {
		activity, err := jobActivityFromWS(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("jobActivityFromWS: %v", err)
		}
		if err := write(activity); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return out.String()
}

const (
	jobActivityMsg1 = `{"running_ingestion_job_count":1,"running_training_job_count":0,"waiting_ingestion_job_count":2,"should_refresh_datagrid":false}`
	jobActivityMsg2 = `{"running_ingestion_job_count":0,"running_training_job_count":1,"waiting_ingestion_job_count":0,"should_refresh_datagrid":true}`
)

// -o json writes one compact object per update, one per line.
func TestJobActivityWatchJSONIsOneObjectPerLine(t *testing.T) {
	out := writeJobActivityUpdates(t, "json", false, jobActivityMsg1, jobActivityMsg2)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %s", len(lines), out)
	}
	var first, second map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line invalid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("second line invalid JSON: %v", err)
	}
	if first["running_ingestion_jobs"] != float64(1) || first["waiting_ingestion_jobs"] != float64(2) {
		t.Errorf("first line = %s, want 1 running and 2 waiting", lines[0])
	}
	if second["should_refresh_datagrid"] != true || second["running_profiling_jobs"] != float64(1) {
		t.Errorf("second line = %s, want a refresh and 1 profiling job", lines[1])
	}
}

// -o yaml writes each update as its own document.
func TestJobActivityWatchYAMLIsOneDocumentPerUpdate(t *testing.T) {
	out := writeJobActivityUpdates(t, "yaml", false, jobActivityMsg1, jobActivityMsg2)
	if !strings.HasPrefix(out, "---\n") {
		t.Errorf("yaml stream should start with a document separator, got: %q", out)
	}
	dec := yaml.NewDecoder(strings.NewReader(out))
	var docs []JobActivity
	for {
		var doc JobActivity
		if err := dec.Decode(&doc); err != nil {
			break
		}
		docs = append(docs, doc)
	}
	if len(docs) != 2 || docs[1].RunningProfilingJobs != 1 {
		t.Errorf("decoded %+v, want 2 documents, the second with 1 profiling job\n%s", docs, out)
	}
}

// --changes-only drops an update equal to the last one, in every view;
// without it every update is written.
func TestJobActivityWatchChangesOnly(t *testing.T) {
	for _, tc := range []struct {
		format      string
		changesOnly bool
		want        int
		count       string
	}{
		{"json", true, 2, "running_ingestion_jobs"},
		{"json", false, 3, "running_ingestion_jobs"},
		{"yaml", true, 2, "---"},
		{"table", true, 2, "Table 42"},
		{"table", false, 3, "Table 42"},
	} {
		out := writeJobActivityUpdates(t, tc.format, tc.changesOnly, jobActivityMsg1, jobActivityMsg1, jobActivityMsg2)
		if got := strings.Count(out, tc.count); got != tc.want {
			t.Errorf("-o %s, changesOnly=%t: %d updates written, want %d\n%s", tc.format, tc.changesOnly, got, tc.want, out)
		}
	}
}

func TestGetTableWatchAcceptsChangesOnly(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want int
	}{
		{"--changes-only", 1},
		{"--changes-only=false", 2},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			stdout, _, err := jawRunJobActivityWatch(t, func(conn *websocket.Conn) {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(jobActivityMsg1))
				_ = conn.WriteMessage(websocket.TextMessage, []byte(jobActivityMsg1))
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				jawHoldOpen(conn)
			}, tc.flag, "--provider", "first-party", "--timeout", "5s", "-o", "json")
			if err != nil {
				t.Fatalf("watch: %v", err)
			}
			if got := strings.Count(stdout, "running_ingestion_jobs"); got != tc.want {
				t.Errorf("watch wrote %d updates, want %d: %s", got, tc.want, stdout)
			}
		})
	}
}

func TestJobActivityWatchTextNonTTY(t *testing.T) {
	out := writeJobActivityUpdates(t, "table", false, `{"running_ingestion_job_count":1,"running_training_job_count":0,"waiting_ingestion_job_count":3,"should_refresh_datagrid":false}`)
	for _, want := range []string{"Table 42", "Ingestion Running:  1", "Ingestion Waiting:  3", "Profiling Running:  0"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
	if strings.Contains(out, "\033") {
		t.Error("non-TTY output should not contain ANSI escape sequences")
	}
}

func TestRenderJobActivityText(t *testing.T) {
	activity := JobActivity{RunningIngestionJobs: 2, RunningProfilingJobs: 1, ShouldRefreshDatagrid: true}
	out := renderJobActivityText(activity, 42)
	if want := "Table 42\n  Ingestion Running:  2\n  Ingestion Waiting:  0\n  Profiling Running:  1\n"; out != want {
		t.Errorf("text = %q, want %q", out, want)
	}
}

// Job counts have no name, so -o name is refused before any request.
func TestGetTableJobActivityRefusesNameOutput(t *testing.T) {
	for _, args := range [][]string{
		{"get", "table", "42", "--job-activity", "-o", "name"},
		{"get", "table", "42", "--job-activity", "--watch", "-o", "name"},
	} {
		cmd := setupTableTestCommand()
		cmd.SetArgs(args)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		err := cmd.Execute()
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "no name") {
			t.Errorf("%v: exit code = %d, err = %v; want %d naming why", args, code, err, qerrors.ExitBadUsage)
		}
	}
}

func TestJobActivityWatchReadFailureStopsBeforeDialing(t *testing.T) {
	for _, provider := range []string{"first-party", "third-party"} {
		for _, tc := range []struct {
			name    string
			status  int
			message string
			code    int
		}{
			{"not found", http.StatusNotFound, "dataset 42 is deleted.", qerrors.ExitNotFound},
			{"missing or outside org", http.StatusForbidden, "Table 42 is not associated with this organization", qerrors.ExitUnauthorized},
			{"no grant", http.StatusForbidden, "User is not authorized to access table 42", qerrors.ExitUnauthorized},
			{"expired token", http.StatusUnauthorized, "Token expired", qerrors.ExitUnauthorized},
			{"server failure", http.StatusInternalServerError, "Internal server error", qerrors.ExitGenericError},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				defer env.Cleanup()
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
				env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
				env.SetupCredential(endpointKey, testOrgID, "test-token")
				path := "/api/orgs/" + testOrgID + "/datasets/42"
				if provider == "third-party" {
					path = "/api/orgs/" + testOrgID + "/third-party/datasets/42"
				}
				mock.RegisterHandler(http.MethodGet, path, func(w http.ResponseWriter, r *http.Request) {
					if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
						t.Errorf("Authorization = %q", got)
					}
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]string{"msg": tc.message}})
				})

				cmd := setupTableTestCommand()
				cmd.SilenceErrors = true
				cmd.SilenceUsage = true
				var out, errOut bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				cmd.SetArgs([]string{"get", "table", "42", "--job-activity", "--watch", "--provider", provider, "-o", "json"})
				err := cmd.Execute()
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit code = %d (err %v), want %d", code, err, tc.code)
				}
				if err == nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("err = %v, want REST reason %q", err, tc.message)
				}
				if tc.status == http.StatusNotFound || tc.status == http.StatusForbidden {
					if !strings.Contains(err.Error(), "Scoped to organization "+testOrgID) {
						t.Errorf("missing organization-scope advice: %v", err)
					}
				}
				if got := mock.Requests(); len(got) != 1 || got[0] != "GET "+path {
					t.Errorf("requests = %v, want only the table read at %s", got, path)
				}
				if out.Len() != 0 || errOut.Len() != 0 {
					t.Errorf("failed pre-read should emit no frames or watch banner: stdout=%q stderr=%q", out.String(), errOut.String())
				}
			})
		}
	}
}

func TestJobActivityWatchReadsTableBeforeStreaming(t *testing.T) {
	for _, tc := range []struct {
		name     string
		selector string
		provider string
	}{
		{"numeric ID", "42", "first-party"},
		{"table name", "vehicles", "first-party"},
		{"shared table", "42", "third-party"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			basePath := "/api/orgs/" + testOrgID
			detailPath := basePath + "/datasets/42"
			socketPath := basePath + "/ws/datasets/42/job-activity"
			if tc.provider == "third-party" {
				detailPath = basePath + "/third-party/datasets/42"
				socketPath = basePath + "/ws/third-party/datasets/42/job-activity"
			}
			requests := make(chan string, 10)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.URL.Path
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Errorf("Authorization = %q", got)
				}
				switch r.URL.Path {
				case basePath + "/datasets":
					testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsListResponse{
						Results: []datasets.DatasetTiny{{ID: 42, Name: "vehicles"}},
					})
				case detailPath:
					testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{ID: 42, Name: "vehicles", State: "active"})
				case socketPath:
					upgrader := websocket.Upgrader{}
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						t.Errorf("upgrade: %v", err)
						return
					}
					defer conn.Close()
					_ = conn.WriteMessage(websocket.TextMessage, []byte(jobActivityMsg1))
					_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					jawHoldOpen(conn)
				default:
					testutil.RespondError(w, http.StatusForbidden, "wrong provider route")
				}
			}))
			defer server.Close()
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			endpointKey, _ := config.NormalizeEndpointKey(server.URL)
			env.SetupConfigWithOrg(server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			cmd := setupTableTestCommand()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs([]string{"get", "table", tc.selector, "--job-activity", "--watch", "--provider", tc.provider, "--timeout", "5s", "-o", "json"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("watch: %v", err)
			}
			wantPaths := []string{detailPath, socketPath}
			if tc.selector == "vehicles" {
				wantPaths = append([]string{basePath + "/datasets"}, wantPaths...)
			}
			if len(requests) != len(wantPaths) {
				t.Fatalf("got %d requests, want %v", len(requests), wantPaths)
			}
			for _, want := range wantPaths {
				if got := <-requests; got != want {
					t.Errorf("request = %s, want %s", got, want)
				}
			}
			var activity JobActivity
			if err := json.Unmarshal(out.Bytes(), &activity); err != nil {
				t.Fatalf("one watch frame: %v; stdout=%q", err, out.String())
			}
			want, _ := jobActivityFromWS(json.RawMessage(jobActivityMsg1))
			if activity != want {
				t.Errorf("activity = %+v, want %+v", activity, want)
			}
		})
	}
}

// jawRunJobActivityWatch serves table 42's job-activity websocket with
// handle, runs `get table 42 --job-activity --watch` plus args against it, and
// returns stdout and stderr apart.
func jawRunJobActivityWatch(t *testing.T, handle func(*websocket.Conn), args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return jawRunJobActivityWatchAfter(t, 0, handle, args...)
}

// jawRunJobActivityWatchAfter is jawRunJobActivityWatch with the server
// waiting delay before it answers the upgrade.
func jawRunJobActivityWatchAfter(t *testing.T, delay time.Duration, handle func(*websocket.Conn), args ...string) (stdout, stderr string, err error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/orgs/"+testOrgID+"/datasets/42" {
			testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{ID: 42, Name: "vehicles", State: "active"})
			return
		}
		if r.URL.Path != "/api/orgs/"+testOrgID+"/ws/datasets/42/job-activity" {
			http.NotFound(w, r)
			return
		}
		time.Sleep(delay)
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handle(conn)
	}))
	defer server.Close()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	endpointKey, _ := config.NormalizeEndpointKey(server.URL)
	env.SetupConfigWithOrg(server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTableTestCommand()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	var out, errOut testutil.LockedBuffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "table", "42", "--job-activity", "--watch"}, args...))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// jawHoldOpen keeps conn open until the client goes away.
func jawHoldOpen(conn *websocket.Conn) {
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// atlas closes a job-activity socket with 1008 once the token is expired or
// revoked; the watch ends at once with exit 4 instead of redialing.
func TestJobActivityWatchPolicyCloseExitsFour(t *testing.T) {
	_, stderr, err := jawRunJobActivityWatch(t, func(conn *websocket.Conn) {
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Authentication expired or revoked."))
		jawHoldOpen(conn)
	}, "--timeout", "5s")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitUnauthorized)
	}
	if !strings.Contains(err.Error(), "Authentication expired or revoked.") {
		t.Errorf("the error should quote the close reason, got: %v", err)
	}
	if strings.Contains(stderr, "reconnecting") || strings.Contains(stderr, "timed out") {
		t.Errorf("a 1008 close ends the watch without a retry or a timeout\nstderr:\n%s", stderr)
	}
}

// A stream watch that runs out of time is a clean end: exit 0, the updates on
// stdout, and on stderr the banner and one line saying how to resume.
func TestJobActivityWatchTimeoutExitsZeroWithResumeLine(t *testing.T) {
	stdout, stderr, err := jawRunJobActivityWatch(t, func(conn *websocket.Conn) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(jobActivityMsg1))
		jawHoldOpen(conn)
	}, "--timeout", "200ms")
	if err != nil {
		t.Fatalf("a stream watch that runs out of time exits 0, got %v (exit %d)", err, qerrors.GetExitCode(err))
	}
	if !strings.Contains(stdout, "Ingestion Waiting:  2") {
		t.Errorf("the update should be on stdout\ngot:\n%s", stdout)
	}
	want := "watching job activity on table 42, for up to 200ms; resume with: qctl get table 42 --job-activity --timeout 200ms --watch\n" +
		"timed out after 200ms; resume with: qctl get table 42 --job-activity --timeout 200ms --watch\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// A --timeout that runs out during the first handshake ends the watch like
// any other timeout.
func TestJobActivityWatchTimeoutDuringTheHandshakeExitsZero(t *testing.T) {
	_, stderr, err := jawRunJobActivityWatchAfter(t, 600*time.Millisecond, jawHoldOpen, "--timeout", "150ms")
	if err != nil {
		t.Fatalf("a stream watch that runs out of time exits 0, got %v (exit %d)", err, qerrors.GetExitCode(err))
	}
	if want := "timed out after 150ms; resume with: qctl get table 42 --job-activity --timeout 150ms --watch\n"; !strings.HasSuffix(stderr, want) {
		t.Errorf("stderr = %q, want it to end with %q", stderr, want)
	}
}

// A connection the server drops is redialed, and the redial is announced on
// stderr at the default verbosity.
func TestJobActivityWatchAnnouncesAReconnect(t *testing.T) {
	_, stderr, err := jawRunJobActivityWatch(t, func(conn *websocket.Conn) {
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "boom"))
		jawHoldOpen(conn)
	}, "--timeout", "500ms")
	if err != nil {
		t.Fatalf("a stream watch that runs out of time exits 0, got %v (exit %d)", err, qerrors.GetExitCode(err))
	}
	want := "connection lost (websocket: close 1011 (internal server error): boom); reconnecting in 1s (attempt 1)\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr should announce the reconnect with %q\ngot:\n%s", want, stderr)
	}
}

func TestJobActivityWatchRoutesVerboseReconnectLogs(t *testing.T) {
	var connections atomic.Int32
	stdout, stderr, err := jawRunJobActivityWatch(t, func(conn *websocket.Conn) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(jobActivityMsg1))
		if connections.Add(1) == 1 {
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "transient"))
		} else {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(jobActivityMsg2))
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		}
		jawHoldOpen(conn)
	}, "-vvvvvvvv", "--changes-only", "--timeout", "5s", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stdout, "\n") != 2 || strings.Contains(stdout, "ws connect") {
		t.Errorf("stdout must contain two deduplicated frames: %q", stdout)
	}
	if strings.Count(stderr, "ws connect ") != 2 || !strings.Contains(stderr, "ws read error, will reconnect:") {
		t.Errorf("missing verbose connection diagnostics on command stderr: %q", stderr)
	}
	if strings.Contains(stderr, "test-token") {
		t.Errorf("redacted WebSocket diagnostics leaked token: %q", stderr)
	}
}

// A signal ends the watch as interrupted (exit 130 for SIGINT) rather than
// exit 0. The signal is sent once the socket is open, so the wait is armed;
// the test never calls qerrors.Exit, so nothing re-raises it here.
func TestJobActivityWatchSIGINTExits130(t *testing.T) {
	cmdutil.SetInterruptGrace(0)
	t.Cleanup(cmdutil.ResetInterruptGrace)
	if runtime.GOOS == "windows" || signal.Ignored(syscall.SIGINT) {
		t.Skip("SIGINT cannot be delivered to this process")
	}
	sendErr := make(chan error, 1)
	_, _, err := jawRunJobActivityWatch(t, func(conn *websocket.Conn) {
		p, err := os.FindProcess(os.Getpid())
		if err == nil {
			err = p.Signal(syscall.SIGINT)
		}
		sendErr <- err
		jawHoldOpen(conn)
	}, "--timeout", "1m")
	if err := <-sendErr; err != nil {
		t.Fatalf("sending SIGINT: %v", err)
	}
	var interrupted *qerrors.Interrupted
	if !errors.As(err, &interrupted) || interrupted.Signal != syscall.SIGINT {
		t.Fatalf("err = %v, want a *qerrors.Interrupted for SIGINT", err)
	}
	if code := qerrors.GetExitCode(err); code != 130 {
		t.Errorf("exit code = %d, want 130", code)
	}
	if want := "interrupted; resume with: qctl get table 42 --job-activity --timeout 1m0s --watch"; interrupted.Message != want {
		t.Errorf("Message = %q, want %q", interrupted.Message, want)
	}
}

// Watch flags without their required mode exit 2 before any request.
func TestGetTableWatchFlagsNeedTheirMode(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{ID: 42, Name: "vehicles", State: "active"})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/job-activity", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.JobRunningCountResponse{})
	})

	for _, args := range [][]string{
		{"get", "table", "42", "--watch"},
		{"get", "table", "42", "--timeout", "1m"},
		{"get", "table", "42", "--job-activity", "--timeout", "1m"},
		{"get", "table", "42", "--job-activity", "--watch", "--timeout=-1s"},
		{"get", "table", "42", "--changes-only"},
		{"get", "table", "42", "--changes-only=false"},
		{"get", "table", "42", "--provider", "first-party"},
		{"get", "table", "42", "--provider", "third-party"},
		{"get", "table", "42", "--job-activity", "--changes-only"},
		{"get", "table", "42", "--job-activity", "--changes-only=false"},
		{"get", "table", "42", "--job-activity", "--provider", "first-party"},
		{"get", "table", "42", "--job-activity", "--provider", "third-party"},
		{"get", "table", "42", "--job-activity", "--watch=false", "--changes-only"},
		{"get", "table", "42", "--job-activity", "--watch=false", "--provider", "third-party"},
	} {
		cmd := setupTableTestCommand()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%v: exit code = %d (err %v), want %d", args, code, err, qerrors.ExitBadUsage)
		}
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}
