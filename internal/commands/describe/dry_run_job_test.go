package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupDryRunJobTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	describeCmd := &cobra.Command{Use: "describe", Short: "Describe resource"}
	describeCmd.AddCommand(NewDryRunJobCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

func sampleDryRunJobFull() api.DryRunJobFullSchema {
	snapshotAt := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	summary := "5 rows changed state"
	return api.DryRunJobFullSchema{
		ID:        42,
		DatasetID: 10,
		State:     "finished",
		Summary:   &summary,
		SnapshotAt: &snapshotAt,
		RuleRunSpecs: []api.RuleRunSpec{
			{Position: 0},
		},
		Sampling: api.SamplingMetadata{
			Cap:       1000,
			AlphaPref: 0.05,
		},
	}
}

func TestDescribeDryRunJob_DefaultOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDryRunJobFull())
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "42"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Should contain essential fields
	essentialFields := []string{
		"apiVersion: qluster.ai/v1",
		"kind: DryRunJob",
		"state: finished",
	}
	for _, field := range essentialFields {
		if !strings.Contains(output, field) {
			t.Errorf("output should contain %q, got:\n%s", field, output)
		}
	}
}

func TestDescribeDryRunJob_JSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDryRunJobFull())
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "42", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"kind": "DryRunJob"`) {
		t.Errorf("JSON output should contain kind DryRunJob, got:\n%s", output)
	}
}

func TestDescribeDryRunJob_VeryVerboseRawDump(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDryRunJobFull())
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "42", "-vvv"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "raw_response") {
		t.Errorf("-vvv output should contain raw_response, got:\n%s", output)
	}
	if !strings.Contains(output, "apiVersion") {
		t.Errorf("-vvv output should contain apiVersion, got:\n%s", output)
	}
}

func TestDescribeDryRunJob_Verbose(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDryRunJobFull())
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "42", "-v"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	// -v should include sampling data
	if !strings.Contains(output, "sampling") {
		t.Errorf("-v output should contain sampling, got:\n%s", output)
	}
}

func TestDescribeDryRunJob_InvalidID(t *testing.T) {
	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "abc"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ID")
	}
}

func TestDescribeDryRunJob_NoArgs(t *testing.T) {
	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for no args")
	}
}

func TestDescribeDryRunJob_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/999", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Not found")
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"describe", "dry-run-job", "999"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for not found")
	}
}
