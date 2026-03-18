package submit

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupSubmitRulesTestCommand() *cobra.Command {
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
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	submitCmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit rule definitions",
	}
	submitCmd.AddCommand(NewRulesCommand())
	rootCmd.AddCommand(submitCmd)

	return rootCmd
}

func TestSubmitRulesCommand_PythonSubmit(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	submitCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/submit", func(w http.ResponseWriter, r *http.Request) {
		submitCalled = true
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"message":     "Rules imported successfully",
			"added":       [][]interface{}{{"my_rule", "1.0.0"}},
			"not_changed": [][]interface{}{{"existing_rule", "2.0.0"}},
		})
	})

	tmpDir := t.TempDir()
	pyFile := filepath.Join(tmpDir, "rules.py")
	if err := os.WriteFile(pyFile, []byte("# rule definition\nclass MyRule:\n    pass\n"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", pyFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !submitCalled {
		t.Error("Submit endpoint was not called")
	}
}

func TestSubmitRulesCommand_NoFiles(t *testing.T) {
	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when no files specified")
	}

	if !strings.Contains(err.Error(), "at least one file is required") {
		t.Errorf("Expected 'at least one file is required' error, got: %v", err)
	}
}

func TestSubmitRulesCommand_RejectsYAML(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "rule.yaml")
	if err := os.WriteFile(yamlFile, []byte("apiVersion: qluster.ai/v1\nkind: Rule\n"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", yamlFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error for YAML file")
	}

	if !strings.Contains(err.Error(), "only .py files are supported") {
		t.Errorf("Expected '.py files' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "qctl apply -f") {
		t.Errorf("Expected suggestion to use 'qctl apply -f', got: %v", err)
	}
}

func TestSubmitRulesCommand_RejectsUnsupportedExtension(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "rules.txt")
	if err := os.WriteFile(txtFile, []byte("some content"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitRulesTestCommand()
	cmd.SetArgs([]string{"submit", "rules", "-f", txtFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error for unsupported file type")
	}

	if !strings.Contains(err.Error(), "only .py files are supported") {
		t.Errorf("Expected 'only .py files' error, got: %v", err)
	}
}
