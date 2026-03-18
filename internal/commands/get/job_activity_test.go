package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
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
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")
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

	// Should be valid JSON with full response fields
	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, buf.String())
	}
	if result["name"] != "orders" {
		t.Errorf("expected name 'orders', got: %v", result["name"])
	}
	if result["destination_name"] != "snowflake" {
		t.Errorf("expected destination_name 'snowflake', got: %v", result["destination_name"])
	}
}

func TestRenderWatchUpdate_JSON(t *testing.T) {
	var buf bytes.Buffer
	activity := JobActivity{
		RunningIngestionJobs:  1,
		WaitingIngestionJobs:  2,
		RunningProfilingJobs:  0,
		ShouldRefreshDatagrid: true,
	}

	_, err := renderWatchUpdate(&buf, "json", activity, 42, false, 0)
	if err != nil {
		t.Fatalf("renderWatchUpdate error: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid NDJSON: %v\n%s", err, buf.String())
	}

	if result["running_ingestion_jobs"] != float64(1) {
		t.Errorf("running_ingestion_jobs = %v, want 1", result["running_ingestion_jobs"])
	}
}

func TestRenderWatchUpdate_YAML(t *testing.T) {
	var buf bytes.Buffer
	activity := JobActivity{
		RunningIngestionJobs:  1,
		WaitingIngestionJobs:  2,
		RunningProfilingJobs:  3,
		ShouldRefreshDatagrid: false,
	}

	_, err := renderWatchUpdate(&buf, "yaml", activity, 42, false, 0)
	if err != nil {
		t.Fatalf("renderWatchUpdate error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "---") {
		t.Error("YAML watch output should start with document separator")
	}
	if !strings.Contains(output, "running_profiling_jobs: 3") {
		t.Errorf("YAML output missing running_profiling_jobs, got: %s", output)
	}
}

func TestRenderWatchUpdate_Table_NonTTY(t *testing.T) {
	var buf bytes.Buffer
	activity := JobActivity{
		RunningIngestionJobs:  1,
		WaitingIngestionJobs:  3,
		RunningProfilingJobs:  0,
		ShouldRefreshDatagrid: false,
	}

	n, err := renderWatchUpdate(&buf, "table", activity, 42, false, 0)
	if err != nil {
		t.Fatalf("renderWatchUpdate error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Table 42") {
		t.Errorf("expected 'Table 42' in output, got: %s", output)
	}
	if !strings.Contains(output, "Ingestion Running:  1") {
		t.Errorf("expected ingestion running count, got: %s", output)
	}
	if !strings.Contains(output, "Ingestion Waiting:  3") {
		t.Errorf("expected ingestion waiting count, got: %s", output)
	}
	if !strings.Contains(output, "Profiling Running:  0") {
		t.Errorf("expected profiling running count, got: %s", output)
	}
	if n != 4 {
		t.Errorf("expected 4 lines rendered, got %d", n)
	}
	// Non-TTY should end with newline (not \r)
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("non-TTY output should end with newline")
	}
	// Should NOT contain ANSI escape sequences
	if strings.Contains(output, "\033") {
		t.Error("non-TTY output should not contain ANSI escape sequences")
	}
}

func TestRenderWatchUpdate_Table_TTY_Overwrite(t *testing.T) {
	var buf bytes.Buffer
	activity := JobActivity{
		RunningIngestionJobs:  2,
		WaitingIngestionJobs:  0,
		RunningProfilingJobs:  1,
		ShouldRefreshDatagrid: true,
	}

	// Second render with prevLines > 0 should include ANSI cursor movement
	_, err := renderWatchUpdate(&buf, "table", activity, 42, true, 4)
	if err != nil {
		t.Fatalf("renderWatchUpdate error: %v", err)
	}

	output := buf.String()
	// Should contain ANSI escape to move cursor up 4 lines
	if !strings.Contains(output, "\033[4A\033[J") {
		t.Errorf("TTY output with prevLines should contain cursor movement, got: %q", output)
	}
	if !strings.Contains(output, "Ingestion Running:  2") {
		t.Errorf("expected ingestion running count, got: %s", output)
	}
}

func TestChangesOnly_Deduplication(t *testing.T) {
	a := JobActivity{RunningIngestionJobs: 1}
	b := JobActivity{RunningIngestionJobs: 1}
	c := JobActivity{RunningIngestionJobs: 2}

	// Same values should be equal
	if a != b {
		t.Error("identical JobActivity structs should be equal")
	}
	// Different values should not
	if a == c {
		t.Error("different JobActivity structs should not be equal")
	}
}

func TestWatchMode_WithMockWS(t *testing.T) {
	// Create a real WebSocket server
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer conn.Close()

		// Send two messages
		msg1 := `{"running_ingestion_job_count":1,"running_training_job_count":0,"waiting_ingestion_job_count":2,"should_refresh_datagrid":false}`
		conn.WriteMessage(websocket.TextMessage, []byte(msg1))

		time.Sleep(50 * time.Millisecond)

		msg2 := `{"running_ingestion_job_count":0,"running_training_job_count":1,"waiting_ingestion_job_count":0,"should_refresh_datagrid":true}`
		conn.WriteMessage(websocket.TextMessage, []byte(msg2))

		time.Sleep(50 * time.Millisecond)

		// Close normally
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"))
	}))
	defer server.Close()

	// We test renderWatchUpdate directly since runJobActivityWatch uses signal handling
	// that's hard to test in unit tests. Instead, test the WS message parsing + rendering pipeline.
	messages := []json.RawMessage{
		json.RawMessage(`{"running_ingestion_job_count":1,"running_training_job_count":0,"waiting_ingestion_job_count":2,"should_refresh_datagrid":false}`),
		json.RawMessage(`{"running_ingestion_job_count":0,"running_training_job_count":1,"waiting_ingestion_job_count":0,"should_refresh_datagrid":true}`),
	}

	var buf bytes.Buffer
	for _, raw := range messages {
		activity, err := jobActivityFromWS(raw)
		if err != nil {
			t.Fatalf("jobActivityFromWS error: %v", err)
		}
		if _, err := renderWatchUpdate(&buf, "json", activity, 42, false, 0); err != nil {
			t.Fatalf("renderWatchUpdate error: %v", err)
		}
	}

	// Should have two NDJSON lines
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %s", len(lines), buf.String())
	}

	// Verify first line
	var first map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line invalid JSON: %v", err)
	}
	if first["running_ingestion_jobs"] != float64(1) {
		t.Errorf("first line running_ingestion_jobs = %v, want 1", first["running_ingestion_jobs"])
	}

	// Verify second line
	var second map[string]interface{}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("second line invalid JSON: %v", err)
	}
	if second["should_refresh_datagrid"] != true {
		t.Errorf("second line should_refresh_datagrid = %v, want true", second["should_refresh_datagrid"])
	}
}

func TestChangesOnly_WithRendering(t *testing.T) {
	messages := []json.RawMessage{
		json.RawMessage(`{"running_ingestion_job_count":1,"running_training_job_count":0,"waiting_ingestion_job_count":2,"should_refresh_datagrid":false}`),
		json.RawMessage(`{"running_ingestion_job_count":1,"running_training_job_count":0,"waiting_ingestion_job_count":2,"should_refresh_datagrid":false}`), // duplicate
		json.RawMessage(`{"running_ingestion_job_count":0,"running_training_job_count":0,"waiting_ingestion_job_count":0,"should_refresh_datagrid":false}`), // changed
	}

	var buf bytes.Buffer
	var prev *JobActivity

	for _, raw := range messages {
		activity, err := jobActivityFromWS(raw)
		if err != nil {
			t.Fatalf("jobActivityFromWS error: %v", err)
		}

		// Simulate --changes-only logic
		if prev != nil && *prev == activity {
			continue
		}
		prev = &JobActivity{}
		*prev = activity

		if _, err := renderWatchUpdate(&buf, "json", activity, 42, false, 0); err != nil {
			t.Fatalf("renderWatchUpdate error: %v", err)
		}
	}

	// Should have 2 lines (duplicate skipped)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines with --changes-only (1 duplicate skipped), got %d:\n%s", len(lines), buf.String())
	}
}
