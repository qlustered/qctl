package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testDryRunOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

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

	getCmd := &cobra.Command{Use: "get", Short: "Get resources"}
	getCmd.AddCommand(NewDryRunJobCommand())
	getCmd.AddCommand(NewDryRunJobsCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func TestGetDryRunJob_Success(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testDryRunOrgID)
	env.SetupCredential(endpointKey, testDryRunOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testDryRunOrgID+"/dry-runs/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 42, "dataset_id": 10, "state": "finished",
			"rule_run_specs": []map[string]interface{}{{"position": 0, "dataset_rule_id": nil}},
			"sampling":       map[string]interface{}{"alpha_pref": 0, "cap": 0, "eligible_c": 0, "eligible_q": 0, "pins_inserted": 0, "pins_missing": 0, "pins_truncated": 0},
			"metrics":        nil,
		})
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-job", "42", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"id": 42`) {
		t.Errorf("output should contain id 42, got: %s", output)
	}
	if !strings.Contains(output, `"state": "finished"`) {
		t.Errorf("output should contain state finished, got: %s", output)
	}
}

func TestGetDryRunJob_InvalidID(t *testing.T) {
	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-job", "abc"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ID")
	}
}

func TestGetDryRunJob_NoArgs(t *testing.T) {
	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-job"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for no args")
	}
}

func TestGetDryRunJobs_Success(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testDryRunOrgID)
	env.SetupCredential(endpointKey, testDryRunOrgID, "test-token")

	// Mock table lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testDryRunOrgID+"/datasets/10", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 10, "name": "test_table",
		})
	})

	// Mock dry-run jobs list
	mock.RegisterHandler("GET", "/api/orgs/"+testDryRunOrgID+"/datasets/10/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{
				{"id": 1, "dataset_id": 10, "dataset_name": "test_table", "state": "finished"},
				{"id": 2, "dataset_id": 10, "dataset_name": "test_table", "state": "running"},
			},
		})
	})

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-jobs", "--table-id", "10", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "finished") {
		t.Errorf("output should contain state 'finished', got: %s", output)
	}
}

func TestGetDryRunJobs_MissingTableFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testDryRunOrgID)
	env.SetupCredential(endpointKey, testDryRunOrgID, "test-token")

	cmd := setupDryRunJobTestCommand()
	cmd.SetArgs([]string{"get", "dry-run-jobs"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing table flag")
	}
}
