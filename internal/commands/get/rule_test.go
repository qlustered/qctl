package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/rule_families"
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
	ruleID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	familyID := uuid.MustParse("aabbccdd-1122-3344-5566-778899aabbcc")
	orgID := uuid.MustParse(testOrgID)
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
				ID:               ruleID,
				Name:             "email_validator",
				Slug:             "email_validator",
				Release:          "1.0.0",
				State:            "enabled",
				IsDefault:        true,
				IsBuiltin:        false,
				IsCaf:            false,
				Description:      &description,
				AffectedColumns:  []string{"email"},
				CreatedAt:        createdAt,
				UpdatedAt:        updatedAt,
				UpgradeAvailable: &upgradeAvailable,
			},
		},
	}
}

func sampleRuleDetailResponse() rule_versions.RuleRevisionFull {
	ruleID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	familyID := uuid.MustParse("aabbccdd-1122-3344-5566-778899aabbcc")
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)
	description := "Validates email format"
	code := "def validate(value):\n    return '@' in value"
	userID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	domains := []api.RuleRevisionProblemDomainSchema{{Name: "Customer records", Slug: "customer-records"}}
	bindings := []api.RuleRevisionFieldConceptSchema{{RuleFieldName: "email", Concept: api.RuleRevisionConceptSchema{Name: "Email address", Slug: "email-address"}}}

	return rule_versions.RuleRevisionFull{
		ID:               ruleID,
		FamilyID:         familyID,
		Name:             "email_validator",
		Slug:             "email_validator",
		Release:          "1.0.0",
		State:            "enabled",
		IsDefault:        true,
		IsBuiltin:        false,
		IsCaf:            false,
		Code:             &code,
		Description:      &description,
		InputColumns:     []string{"email"},
		ValidatesColumns: []string{"email"},
		CorrectsColumns:  []string{},
		EnrichesColumns:  []string{},
		AffectedColumns:  []string{"email"},
		ProblemDomains:   &domains,
		FieldConcepts:    &bindings,
		ParamSchema:      map[string]interface{}{},
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
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

// The declaration in NewRuleCommand claims -o name renders. This is the check
// that it does: one bare revision ID per line, no header, nothing else -- the
// shape a shell loop can consume. Declaring a format the renderer cannot honor
// would make it un-passable for no reason.
func TestGetRuleCommand_NameOutput(t *testing.T) {
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
	cmd.SetArgs([]string{"get", "rule", "550e8400-e29b-41d4-a716-446655440000", "-o", "name"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := buf.String(); got != "rule/550e8400-e29b-41d4-a716-446655440000\n" {
		t.Errorf("-o name output = %q, want rule/<revision ID> on one line", got)
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
		"problem_domains:",
		"customer-records",
		"field_concepts:",
		"rule_field_name: email",
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
	ruleID1 := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	ruleID2 := uuid.MustParse("660e8400-e29b-41d4-a716-446655440001")
	familyID := uuid.MustParse("aabbccdd-1122-3344-5566-778899aabbcc")
	orgID := uuid.MustParse(testOrgID)
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
				ID:               ruleID1,
				Name:             "email_validator",
				Slug:             "email_validator",
				Release:          "1.0.0",
				State:            "disabled",
				IsDefault:        false,
				IsBuiltin:        false,
				IsCaf:            false,
				Description:      &description1,
				AffectedColumns:  []string{"email"},
				CreatedAt:        createdAt,
				UpdatedAt:        updatedAt,
				UpgradeAvailable: &upgradeAvailable,
			},
			{
				ID:               ruleID2,
				Name:             "email_validator",
				Slug:             "email_validator",
				Release:          "2.0.0",
				State:            "enabled",
				IsDefault:        true,
				IsBuiltin:        false,
				IsCaf:            false,
				Description:      &description2,
				AffectedColumns:  []string{"email"},
				CreatedAt:        createdAt,
				UpdatedAt:        updatedAt,
				UpgradeAvailable: &upgradeAvailable,
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

	// Should contain both releases in table output
	for _, want := range []string{"email_validator", "1.0.0", "2.0.0", "disabled", "enabled"} {
		if !strings.Contains(output, want) {
			t.Errorf("Output should contain %q, got:\n%s", want, output)
		}
	}

	// The default (2.0.0) is the newest release, so nothing is newer than it and
	// there is nothing to upgrade to. The server says so -- every revision comes
	// back with upgrade_available=false -- and qctl must not invent otherwise.
	for _, unwanted := range []string{"Newer than default", "Update available"} {
		if strings.Contains(output, unwanted) {
			t.Errorf("Output should NOT contain %q when the default is the newest release, got:\n%s", unwanted, output)
		}
	}
}

// sampleFamilyWithNewerThanDefaultResponse is a family whose default (2.0.0) is
// NOT the newest release: 3.0.0 sits above it. upgrade_available mirrors what
// capi/capi/views/rule_view.py sets -- true only for revisions above the
// default, false for the default itself and for anything older.
func sampleFamilyWithNewerThanDefaultResponse() rule_versions.RuleRevisionsFamily {
	family := sampleMultiReleaseFamilyResponse()

	newer := true
	older := false
	family.Results[0].UpgradeAvailable = &older // 1.0.0, not default
	family.Results[1].UpgradeAvailable = &older // 2.0.0, default

	third := family.Results[1]
	third.ID = uuid.MustParse("770e8400-e29b-41d4-a716-446655440002")
	third.Release = "3.0.0"
	third.State = "draft"
	third.IsDefault = false
	third.UpgradeAvailable = &newer
	family.Results = append(family.Results, third)

	return family
}

func TestGetRuleCommand_MultiRelease_TagsOnlyOnTheRevisionsTheServerFlagged(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleFamilyWithNewerThanDefaultResponse()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := len(family.Results)
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

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// 1.0.0 is older than the default: neither tag belongs on it.
	assertRowLacks(t, output, "1.0.0", "Newer than default")
	assertRowLacks(t, output, "1.0.0", "Update available")
	// 2.0.0 is the default and something newer exists.
	assertRowHas(t, output, "2.0.0", "Update available")
	assertRowLacks(t, output, "2.0.0", "Newer than default")
	// 3.0.0 is the one the server flagged.
	assertRowHas(t, output, "3.0.0", "Newer than default")
	assertRowLacks(t, output, "3.0.0", "Update available")
}

// ruleRow returns the single table line for a release, failing the test if the
// release is missing or ambiguous.
func ruleRow(t *testing.T, output, release string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, release) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one row for release %s, found %d in:\n%s", release, len(found), output)
	}
	return found[0]
}

func assertRowHas(t *testing.T, output, release, tag string) {
	t.Helper()
	if row := ruleRow(t, output, release); !strings.Contains(row, tag) {
		t.Errorf("release %s should be tagged %q, row was: %s", release, tag, row)
	}
}

func assertRowLacks(t *testing.T, output, release, tag string) {
	t.Helper()
	if row := ruleRow(t, output, release); strings.Contains(row, tag) {
		t.Errorf("release %s should NOT be tagged %q, row was: %s", release, tag, row)
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

// setupRulesAndRuleTestCommand carries both `get rules` and `get rule` on one
// root, so a single test can walk the user's path: list, read a SHORT-ID off
// the table, paste it back.
func setupRulesAndRuleTestCommand() *cobra.Command {
	rootCmd := setupRuleTestCommand()
	for _, sub := range rootCmd.Commands() {
		if sub.Name() == "get" {
			sub.AddCommand(NewRuleFamiliesCommand())
		}
	}
	return rootCmd
}

// The other revision of the family served below -- deliberately not a prefix of
// familyRevShortID, so "the whole family was printed" is distinguishable.
const otherFamilyRevID = "770e8400-e29b-41d4-a716-446655440009"

// TestGetRuleResolvesAShortIDSeededByGetRules is the ticket's headline user
// story end to end: `get rules` prints a SHORT-ID and seeds the cache, and
// pasting that SHORT-ID straight into `get rule` resolves it -- which is what
// internal/resources/pages/rule.md promises with "qctl get rule 550e8400", and
// what every other test in this file misses by driving `get rule` only with
// full UUIDs and names.
//
// The rule-revisions listing here is search-honouring, like atlas
// (get_rule_revision_list, capi/capi/views/rule_view.py): search_query is
// matched against name, slug and release and NEVER against id, so a resolver
// that put the SHORT-ID on the wire as a search would get an empty page and fail
// outright. It is also counted, because a seeded cache still needs exactly one
// exact-name probe but no unfiltered catalog listing. That distinction makes the
// seeding load-bearing rather than incidental.
func TestGetRuleResolvesAShortIDSeededByGetRules(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// What `get rules` lists: one family whose primary revision is familyRevID.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, _ *http.Request) {
		total, page := 1, 1
		testutil.RespondJSON(w, http.StatusOK, rule_families.RuleFamilyList{
			Results:   []rule_families.RuleFamilyItem{sampleRuleFamilyItem()},
			TotalRows: &total,
			Page:      &page,
		})
	})

	// The same rule's two releases, as `get rule` fetches them by full UUID.
	revisions := []rule_versions.RuleRevisionTiny{
		testutil.MakeRuleRevisionTiny(familyRevID, "email_validator", "1.0.0", "enabled"),
		testutil.MakeRuleRevisionTiny(otherFamilyRevID, "email_validator", "2.0.0", "draft"),
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+familyRevID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionsFamily{
			FamilyID:       uuid.MustParse("aabbccdd-1122-3344-5566-778899aabbcc"),
			Name:           "email_validator",
			OrganizationID: uuid.MustParse(testOrgID),
			Results:        revisions,
		})
	})

	var listings []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("search_query")
		listings = append(listings, q)
		results := revisions
		if len(q) > 2 {
			results = nil
			for _, rev := range revisions {
				if strings.Contains(strings.ToLower(rev.Name), strings.ToLower(q)) ||
					strings.Contains(strings.ToLower(rev.Slug), strings.ToLower(q)) ||
					strings.Contains(strings.ToLower(rev.Release), strings.ToLower(q)) {
					results = append(results, rev)
				}
			}
		}
		total, page := len(results), 1
		testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{
			Results: results, TotalRows: &total, Page: &page,
		})
	})

	// 1. List. This is the only thing that teaches the cache the SHORT-ID.
	listCmd := setupRulesAndRuleTestCommand()
	listCmd.SetArgs([]string{"get", "rules"})
	var listBuf bytes.Buffer
	listCmd.SetOut(&listBuf)
	listCmd.SetErr(&listBuf)
	if err := listCmd.Execute(); err != nil {
		t.Fatalf("get rules error = %v\noutput: %s", err, listBuf.String())
	}
	if !strings.Contains(listBuf.String(), familyRevShortID) {
		t.Fatalf("`get rules` printed no SHORT-ID to paste; got:\n%s", listBuf.String())
	}

	// 2. Paste the SHORT-ID the table just showed.
	getCmd := setupRulesAndRuleTestCommand()
	getCmd.SetArgs([]string{"get", "rule", familyRevShortID})
	var getBuf bytes.Buffer
	getCmd.SetOut(&getBuf)
	getCmd.SetErr(&getBuf)
	if err := getCmd.Execute(); err != nil {
		t.Fatalf("get rule %s error = %v\noutput: %s", familyRevShortID, err, getBuf.String())
	}

	out := getBuf.String()
	for _, want := range []string{"email_validator", "1.0.0", "enabled", familyRevShortID} {
		if !strings.Contains(out, want) {
			t.Errorf("`get rule %s` output should contain %q, got:\n%s", familyRevShortID, want, out)
		}
	}
	// A SHORT-ID names one revision, so getRuleAllReleases filters the family
	// down to it instead of listing every release.
	if strings.Contains(out, "2.0.0") {
		t.Errorf("`get rule %s` printed the whole family; a SHORT-ID names one revision, got:\n%s", familyRevShortID, out)
	}
	if got, want := strings.Join(listings, ","), familyRevShortID; got != want {
		t.Errorf("`get rule %s` search queries = %q, want %q (the seeded cache must avoid an unfiltered listing)",
			familyRevShortID, got, want)
	}
}

func TestGetRuleCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

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

// getAffectedColumnTypes builds the wire's union map so a fixture can say
// `{"email": ["String"]}` or `{"email": "any"}` without hand-rolling
// json.RawMessage.
func getAffectedColumnTypes(t *testing.T, entries map[string]interface{}) *map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties {
	t.Helper()
	out := make(map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties, len(entries))
	for field, value := range entries {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal fixture entry %q: %v", field, err)
		}
		var entry api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties
		if err := entry.UnmarshalJSON(raw); err != nil {
			t.Fatalf("build fixture entry %q: %v", field, err)
		}
		out[field] = entry
	}
	return &out
}

// runGetRuleYAML serves one detail response and returns `get rule -o yaml`.
func runGetRuleYAML(t *testing.T, detail rule_versions.RuleRevisionFull) string {
	t.Helper()
	return runGetRuleAs(t, detail, "yaml")
}

// runGetRuleAs serves one detail response and returns `get rule -o <format>`.
func runGetRuleAs(t *testing.T, detail rule_versions.RuleRevisionFull, format string) string {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"get", "rule", "550e8400-e29b-41d4-a716-446655440000", "--release", "1.0.0", "-o", format})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return buf.String()
}

// TestGetRuleCommand_YAMLReadsAsJSON: -o yaml is the tree -o json prints,
// including the per-parameter renderability entries.
func TestGetRuleCommand_YAMLReadsAsJSON(t *testing.T) {
	detail := sampleRuleDetailResponse()
	reason := "NESTED_OBJECT"
	detail.ParamsRenderability = &map[string]api.FieldRenderabilitySchema{
		"pattern": {RenderableInBasicUI: true},
		"options": {Reason: &reason},
	}
	detail.AffectedColumnTypes = getAffectedColumnTypes(t, map[string]interface{}{"email": "any"})

	testutil.AssertYAMLReadsAsJSON(t, runGetRuleAs(t, detail, "yaml"), runGetRuleAs(t, detail, "json"))
}

// spec.default_severity is what a table rule instantiated from this revision
// inherits when its manifest omits spec.severity, and spec.affected_column_types
// is the type each rule field accepts. Both reached no qctl output before, so a
// manifest author could not see either.
func TestGetRuleCommand_YAMLCarriesTheRevisionDeclarations(t *testing.T) {
	detail := sampleRuleDetailResponse()
	severity := api.RuleSeverityWarning
	detail.DefaultSeverity = &severity
	detail.AffectedColumnTypes = getAffectedColumnTypes(t, map[string]interface{}{
		"email": []string{"String", "Text"},
		"score": "any",
	})

	output := runGetRuleYAML(t, detail)

	for _, want := range []string{
		"default_severity: warning",
		"affected_column_types:",
		"- String",
		"- Text",
		"score: any",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("YAML output should contain %q, got:\n%s", want, output)
		}
	}
}

// The shared fixture declares neither field. A `kind: Rule` manifest is
// advertised as round-trippable, so it must not gain a key the server never
// sent -- `affected_column_types: {}` reads as "this rule accepts nothing",
// and a filled-in default_severity would put an author declaration into a
// manifest that no revision made.
func TestGetRuleCommand_YAMLOmitsUndeclaredRevisionDeclarations(t *testing.T) {
	output := runGetRuleYAML(t, sampleRuleDetailResponse())

	for _, unwanted := range []string{"default_severity", "affected_column_types"} {
		if strings.Contains(output, unwanted) {
			t.Errorf("YAML output carries %q for a revision that declared none, got:\n%s", unwanted, output)
		}
	}
}
