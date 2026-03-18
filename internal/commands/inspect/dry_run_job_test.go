package inspect

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupInspectTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "json", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	inspectCmd := NewCommand()
	rootCmd.AddCommand(inspectCmd)
	return rootCmd
}

func TestInspectDryRunJob_CompactView(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42/preview/compact", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.CompactDryRunPreviewResponse{
			DryRunJobID: 42,
			State:       "finished",
			Stats: api.DryRunComparisonStats{
				TotalRowsProcessed:  100,
				RowsWithStateChange: 5,
				RowsWithValueChange: 3,
			},
			Repro: api.AgentReproMetadata{
				SnapshotID: 1,
			},
		})
	})

	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "42"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "42") {
		t.Errorf("output should contain dry_run_job_id 42, got: %s", output)
	}
	if !strings.Contains(output, "100") {
		t.Errorf("output should contain total_rows_processed 100, got: %s", output)
	}
}

func TestInspectDryRunJob_FullView(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42/preview", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DryRunJobPreviewResponse{
			DryRunJobID: 42,
			State:       "finished",
			AllFields:   []string{"name", "email"},
			Stats: api.DryRunComparisonStats{
				TotalRowsProcessed: 50,
			},
		})
	})

	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "42", "--view", "full"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "name") {
		t.Errorf("output should contain field 'name', got: %s", output)
	}
	if !strings.Contains(output, "email") {
		t.Errorf("output should contain field 'email', got: %s", output)
	}
}

func TestInspectDryRunJob_YAMLOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/42/preview/compact", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.CompactDryRunPreviewResponse{
			DryRunJobID: 42,
			State:       "finished",
			Stats: api.DryRunComparisonStats{
				TotalRowsProcessed: 100,
			},
			Repro: api.AgentReproMetadata{
				SnapshotID: 1,
			},
		})
	})

	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "42", "-o", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "dry_run_job_id: 42") {
		t.Errorf("YAML output should contain dry_run_job_id, got: %s", output)
	}
}

func TestInspectDryRunJob_InvalidID(t *testing.T) {
	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "abc"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ID")
	}
}

func TestInspectDryRunJob_NoArgs(t *testing.T) {
	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for no args")
	}
}

func TestInspectDryRunJob_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/999/preview/compact", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Not found")
	})

	cmd := setupInspectTestCommand()
	cmd.SetArgs([]string{"inspect", "dry-run-job", "999"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for not found")
	}
}
