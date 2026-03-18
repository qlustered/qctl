package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupTableRuleTestCommand() *cobra.Command {
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

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewTableRuleCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleDatasetRuleDetail() *dataset_rules.DatasetRuleDetail {
	ruleID := openapi_types.UUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"))
	revisionID := openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"))
	orgID := openapi_types.UUID(uuid.MustParse(testOrgID))
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)

	return &dataset_rules.DatasetRuleDetail{
		ID:             ruleID,
		InstanceName:   "email_check",
		DatasetID:      1,
		OrganizationID: orgID,
		State:          "enabled",
		TreatAsAlert:   false,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		RuleRevision: api.RuleRevisionTinySchema{
			ID:      revisionID,
			Name:    "email_validator",
			Release: "1.0.0",
		},
		Params:            map[string]interface{}{"threshold": float64(0.9)},
		ColumnMappingDict: map[string]string{"email": "email_col"},
	}
}

func TestGetTableRuleCommand_ByName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()

	// Register list endpoint for name resolution (uses instance_name filter)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		// Verify the instance_name filter is sent
		if got := r.URL.Query().Get("instance_name"); got != "email_check" {
			t.Errorf("Expected instance_name=email_check query param, got %q", got)
		}
		totalRows := 1
		page := 1
		response := dataset_rules.DatasetRuleList{
			Results: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Register detail endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+detail.ID.String(), func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", "email_check", "--table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	for _, want := range []string{"email_check", "1.0.0", "Enabled", "Warning", "550e8400"} {
		if !strings.Contains(output, want) {
			t.Errorf("Output should contain %q, got: %s", want, output)
		}
	}
}

func TestGetTableRuleCommand_ByUUID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	fullUUID := detail.ID.String()

	// Only detail endpoint needed — UUID skips list resolution
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "email_check") {
		t.Errorf("Output should contain 'email_check', got: %s", output)
	}
}

func TestGetTableRuleCommand_JSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	fullUUID := detail.ID.String()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table", "1", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("Output is not valid JSON: %v\nOutput: %s", err, output)
	}

	// Check manifest structure
	if parsed["kind"] != "TableRule" {
		t.Errorf("Expected kind 'TableRule', got: %v", parsed["kind"])
	}
	if parsed["apiVersion"] != "qluster.ai/v1" {
		t.Errorf("Expected apiVersion 'qluster.ai/v1', got: %v", parsed["apiVersion"])
	}

	// Verify params and column_mapping are present at verbosity 0
	spec, ok := parsed["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("spec should be a map")
	}
	if _, ok := spec["params"]; !ok {
		t.Error("JSON output should contain spec.params at verbosity 0")
	}
	if _, ok := spec["column_mapping"]; !ok {
		t.Error("JSON output should contain spec.column_mapping at verbosity 0")
	}
}

func TestGetTableRuleCommand_YAMLOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	fullUUID := detail.ID.String()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table", "1", "-o", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	for _, want := range []string{"kind: TableRule", "apiVersion: qluster.ai/v1", "instance_name: email_check", "threshold", "email_col"} {
		if !strings.Contains(output, want) {
			t.Errorf("YAML output should contain %q, got: %s", want, output)
		}
	}
}

func TestGetTableRuleCommand_MissingTableFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", "email_check"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when --table flag is missing")
	}
	if !strings.Contains(err.Error(), "--table flag is required") {
		t.Errorf("Expected '--table flag is required' error, got: %v", err)
	}
}

func TestGetTableRuleCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	fullUUID := "550e8400-e29b-41d4-a716-446655440000"

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}
