package create

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupCreateTestCommand() *cobra.Command {
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

	createCmd := NewCommand()
	rootCmd.AddCommand(createCmd)
	return rootCmd
}

func writeSpecFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write spec file: %v", err)
	}
	return path
}

func TestCreateDryRunJob_WithExplicitCloudSourceID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Mock cloud source lookup (no table needed when cloud-source-id is given)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 456, "name": "test_source",
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 99,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "99") {
		t.Errorf("output should contain dry_run_job_id 99, got: %s", output)
	}
}

func TestCreateDryRunJob_WithAutoDetectedCloudSource(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Mock table lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/123", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 123, "name": "test_table",
		})
	})

	// Mock cloud source list (exactly 1 for auto-detection)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{
				{"id": 456, "name": "auto_source"},
			},
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 88,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table_id: 123
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "88") {
		t.Errorf("output should contain dry_run_job_id 88, got: %s", output)
	}
}

func TestCreateDryRunJob_MissingFile(t *testing.T) {
	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --file flag")
	}
}

func TestCreateDryRunJob_RuleNameResolution(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	ruleRevisionID := "550e8400-e29b-41d4-a716-446655440099"

	// Mock rule revisions list (for name resolution)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results: []rule_versions.RuleRevisionTiny{
				testutil.MakeRuleRevisionTiny(ruleRevisionID, "email_validator", "1.0.0", "enabled"),
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Mock cloud source lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 456, "name": "test_source",
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 77,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - rule: email_validator
      release: "1.0.0"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "77") {
		t.Errorf("output should contain dry_run_job_id 77, got: %s", output)
	}
}

func TestCreateDryRunJob_RuleNameWithoutRelease(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	ruleRevisionID := "550e8400-e29b-41d4-a716-446655440099"

	// Mock rule revisions list (single release — no release filter needed)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results: []rule_versions.RuleRevisionTiny{
				testutil.MakeRuleRevisionTiny(ruleRevisionID, "email_validator", "1.0.0", "enabled"),
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Mock cloud source lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 456, "name": "test_source",
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 76,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - rule: email_validator
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "76") {
		t.Errorf("output should contain dry_run_job_id 76, got: %s", output)
	}
}

func TestCreateDryRunJob_ShortIDResolution(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	ruleRevisionID := "550e8400-e29b-41d4-a716-446655440099"

	// Mock rule revisions list (for short ID resolution)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results: []rule_versions.RuleRevisionTiny{
				testutil.MakeRuleRevisionTiny(ruleRevisionID, "email_validator", "1.0.0", "enabled"),
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Mock cloud source lookup
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/456", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": 456, "name": "test_source",
		})
	})

	// Mock dry-run launch
	mock.RegisterHandler("PUT", "/api/orgs/"+testOrgID+"/dry-runs", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"dry_run_job_id": 75,
		})
	})

	specFile := writeSpecFile(t, `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - rule_revision_id: "550e8400"
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile, "--cloud-source-id", "456"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "75") {
		t.Errorf("output should contain dry_run_job_id 75, got: %s", output)
	}
}

func TestCreateDryRunJob_InvalidSpecFile(t *testing.T) {
	specFile := writeSpecFile(t, `apiVersion: v2
kind: Wrong
metadata: {}
spec:
  rule_run_specs: []
`)

	cmd := setupCreateTestCommand()
	cmd.SetArgs([]string{"create", "dry-run-job", "-f", specFile})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid spec file")
	}
}
