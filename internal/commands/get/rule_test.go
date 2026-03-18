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
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupRuleTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewRuleCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleRuleFamilyResponse() rule_versions.RuleRevisionsFamily {
	ruleID := openapi_types.UUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"))
	familyID := openapi_types.UUID(uuid.MustParse("aabbccdd-1122-3344-5566-778899aabbcc"))
	orgID := openapi_types.UUID(uuid.MustParse(testOrgID))
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)
	description := "Validates email format"
	upgradeAvailable := false

	return rule_versions.RuleRevisionsFamily{
		FamilyID:       familyID,
		Name:           "email_validator",
		OrganizationID: orgID,
		Results: []rule_versions.RuleRevisionTiny{
			{
				ID:                   ruleID,
				Name:                 "email_validator",
				Release:              "1.0.0",
				State:                "enabled",
				IsDefault:            true,
				IsBuiltin:            false,
				IsCaf:                false,
				Description:          &description,
				InteractsWithColumns: []string{"email"},
				CreatedAt:            createdAt,
				UpdatedAt:            updatedAt,
				UpgradeAvailable:     &upgradeAvailable,
			},
		},
	}
}

func sampleRuleDetailResponse() rule_versions.RuleRevisionFull {
	ruleID := openapi_types.UUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"))
	familyID := openapi_types.UUID(uuid.MustParse("aabbccdd-1122-3344-5566-778899aabbcc"))
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)
	description := "Validates email format"
	code := "def validate(value):\n    return '@' in value"
	userID := openapi_types.UUID(uuid.MustParse("11111111-2222-3333-4444-555555555555"))

	return rule_versions.RuleRevisionFull{
		ID:                   ruleID,
		FamilyID:             familyID,
		Name:                 "email_validator",
		Release:              "1.0.0",
		State:                "enabled",
		IsDefault:            true,
		IsBuiltin:            false,
		IsCaf:                false,
		Code:                 &code,
		Description:          &description,
		InputColumns:         []string{"email"},
		ValidatesColumns:     []string{"email"},
		CorrectsColumns:      []string{},
		EnrichesColumns:      []string{},
		InteractsWithColumns: []string{"email"},
		ParamSchema:          map[string]interface{}{},
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
		CreatedByUser: &api.UserInfoTinyDictSchema{
			ID:        userID,
			Email:     "user@example.com",
			FirstName: "Test",
			LastName:  "User",
		},
	}
}

func TestGetRuleCommand_TableOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleRuleFamilyResponse()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "550e8400-e29b-41d4-a716-446655440000"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Should contain table data
	for _, want := range []string{"email_validator", "1.0.0", "enabled", "550e8400"} {
		if !strings.Contains(output, want) {
			t.Errorf("Output should contain %q, got: %s", want, output)
		}
	}
}

func TestGetRuleCommand_YAMLOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleRuleDetailResponse()

	// Detail endpoint for structured output
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "550e8400-e29b-41d4-a716-446655440000", "--release", "1.0.0", "-o", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Verify YAML structure: state, is_default, and code should be in spec
	for _, want := range []string{
		"apiVersion: qluster.ai/v1",
		"kind: Rule",
		"name: email_validator",
		"state: enabled",
		"is_default: true",
		"release: 1.0.0",
		"code:",
		"family_id: aabbccdd-1122-3344-5566-778899aabbcc",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("YAML output should contain %q, got:\n%s", want, output)
		}
	}

	// state and is_default should be in spec section (before status:)
	specIdx := strings.Index(output, "spec:")
	statusIdx := strings.Index(output, "status:")
	stateIdx := strings.Index(output, "  state: enabled")

	if specIdx == -1 || statusIdx == -1 || stateIdx == -1 {
		t.Fatalf("Expected spec, status, and state in YAML output, got:\n%s", output)
	}

	if stateIdx > statusIdx {
		t.Errorf("state should be in spec section (before status:), got:\n%s", output)
	}

	// Timestamps should be RFC3339
	if !strings.Contains(output, "2025-06-15T10:00:00Z") {
		t.Errorf("Expected RFC3339 created_at timestamp, got:\n%s", output)
	}
	if !strings.Contains(output, "2025-06-20T14:00:00Z") {
		t.Errorf("Expected RFC3339 updated_at timestamp, got:\n%s", output)
	}
}

func TestGetRuleCommand_JSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleRuleDetailResponse()

	// Detail endpoint for structured output
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "550e8400-e29b-41d4-a716-446655440000", "--release", "1.0.0", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Parse JSON and verify structure
	var manifest map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &manifest); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if manifest["apiVersion"] != "qluster.ai/v1" {
		t.Errorf("Expected apiVersion 'qluster.ai/v1', got %v", manifest["apiVersion"])
	}
	if manifest["kind"] != "Rule" {
		t.Errorf("Expected kind 'Rule', got %v", manifest["kind"])
	}

	// Verify spec contains state, is_default, and code
	spec, ok := manifest["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected spec to be a map")
	}
	if spec["state"] != "enabled" {
		t.Errorf("Expected spec.state 'enabled', got %v", spec["state"])
	}
	if spec["is_default"] != true {
		t.Errorf("Expected spec.is_default true, got %v", spec["is_default"])
	}
	if spec["code"] == nil {
		t.Error("Expected spec.code to be present")
	}

	// Verify status contains family_id and does NOT contain state/is_default
	status, ok := manifest["status"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected status to be a map")
	}
	if status["family_id"] != "aabbccdd-1122-3344-5566-778899aabbcc" {
		t.Errorf("Expected status.family_id, got %v", status["family_id"])
	}
	if _, hasState := status["state"]; hasState {
		t.Error("status should NOT contain state (it now belongs in spec)")
	}
	if _, hasIsDefault := status["is_default"]; hasIsDefault {
		t.Error("status should NOT contain is_default (it now belongs in spec)")
	}
}

func sampleMultiReleaseFamilyResponse() rule_versions.RuleRevisionsFamily {
	ruleID1 := openapi_types.UUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"))
	ruleID2 := openapi_types.UUID(uuid.MustParse("660e8400-e29b-41d4-a716-446655440001"))
	familyID := openapi_types.UUID(uuid.MustParse("aabbccdd-1122-3344-5566-778899aabbcc"))
	orgID := openapi_types.UUID(uuid.MustParse(testOrgID))
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)
	description1 := "Validates email format"
	description2 := "Validates email format v2"
	upgradeAvailable := false

	return rule_versions.RuleRevisionsFamily{
		FamilyID:       familyID,
		Name:           "email_validator",
		OrganizationID: orgID,
		Results: []rule_versions.RuleRevisionTiny{
			{
				ID:                   ruleID1,
				Name:                 "email_validator",
				Release:              "1.0.0",
				State:                "disabled",
				IsDefault:            false,
				IsBuiltin:            false,
				IsCaf:                false,
				Description:          &description1,
				InteractsWithColumns: []string{"email"},
				CreatedAt:            createdAt,
				UpdatedAt:            updatedAt,
				UpgradeAvailable:     &upgradeAvailable,
			},
			{
				ID:                   ruleID2,
				Name:                 "email_validator",
				Release:              "2.0.0",
				State:                "enabled",
				IsDefault:            true,
				IsBuiltin:            false,
				IsCaf:                false,
				Description:          &description2,
				InteractsWithColumns: []string{"email"},
				CreatedAt:            createdAt,
				UpdatedAt:            updatedAt,
				UpgradeAvailable:     &upgradeAvailable,
			},
		},
	}
}

func TestGetRuleCommand_MultiRelease_TableOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleMultiReleaseFamilyResponse()

	// Register list endpoint for name resolution (ResolveRuleAny)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   family.Results,
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Register family endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "email_validator"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Should contain both releases in table output with new tag names
	for _, want := range []string{"email_validator", "1.0.0", "2.0.0", "disabled", "enabled", "Newer than default", "Update available"} {
		if !strings.Contains(output, want) {
			t.Errorf("Output should contain %q, got:\n%s", want, output)
		}
	}
}

func TestGetRuleCommand_MultiRelease_YAMLOutput_Error(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleMultiReleaseFamilyResponse()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   family.Results,
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "email_validator", "-o", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error for multi-release YAML output without --release")
	}

	if !strings.Contains(err.Error(), "has 2 releases") {
		t.Errorf("Expected error about multiple releases, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--release") {
		t.Errorf("Expected error to suggest --release, got: %v", err)
	}
}

func TestGetRuleCommand_MultiRelease_JSONOutput_Error(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleMultiReleaseFamilyResponse()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   family.Results,
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "email_validator", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error for multi-release JSON output without --release")
	}

	if !strings.Contains(err.Error(), "has 2 releases") {
		t.Errorf("Expected error about multiple releases, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--release") {
		t.Errorf("Expected error to suggest --release, got: %v", err)
	}
}

func TestGetRuleCommand_ByName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleRuleFamilyResponse()

	// Register GET handler for resolver (name resolution)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   family.Results,
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "email_validator"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "email_validator") {
		t.Errorf("Output should contain 'email_validator', got: %s", output)
	}
}

func TestGetRuleCommand_CodeOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleRuleDetailResponse()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "550e8400-e29b-41d4-a716-446655440000", "--release", "1.0.0", "-o", "code"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Should be just the raw code with exactly one trailing newline
	expected := "def validate(value):\n    return '@' in value\n"
	if output != expected {
		t.Errorf("Expected code output %q, got %q", expected, output)
	}
}

func TestGetRuleCommand_CodeOutput_NoCode(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleRuleDetailResponse()
	detail.Code = nil // no code

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "550e8400-e29b-41d4-a716-446655440000", "--release", "1.0.0", "-o", "code"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error when rule has no code")
	}
	if !strings.Contains(err.Error(), "has no code") {
		t.Errorf("Expected 'has no code' error, got: %v", err)
	}
}

func TestGetRuleCommand_CodeOutput_MultiRelease_Error(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleMultiReleaseFamilyResponse()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   family.Results,
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "email_validator", "-o", "code"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error for multi-release code output without --release")
	}
	if !strings.Contains(err.Error(), "has 2 releases") {
		t.Errorf("Expected error about multiple releases, got: %v", err)
	}
}

func TestGetRuleCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "550e8400-e29b-41d4-a716-446655440000"})

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

func TestGetRuleCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "550e8400-e29b-41d4-a716-446655440000"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}
