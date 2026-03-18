package delete

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testRuleRevisionID = "550e8400-e29b-41d4-a716-446655440000"

func setupDeleteRuleTestCommand() *cobra.Command {
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

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete resources",
	}
	deleteCmd.AddCommand(NewRuleCommand())
	deleteCmd.AddCommand(NewRulesCommand())
	rootCmd.AddCommand(deleteCmd)

	return rootCmd
}

func sampleDeleteRuleFamily() rule_versions.RuleRevisionsFamily {
	familyID := openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"))
	orgID := openapi_types.UUID(uuid.MustParse(testOrgID))
	ruleID := openapi_types.UUID(uuid.MustParse(testRuleRevisionID))
	description := "Validates email format"

	return rule_versions.RuleRevisionsFamily{
		FamilyID:       familyID,
		Name:           "email_validator",
		OrganizationID: orgID,
		Results: []rule_versions.RuleRevisionTiny{
			{
				ID:                   ruleID,
				Name:                 "email_validator",
				Release:              "1.0.0",
				State:                "draft",
				IsDefault:            false,
				IsBuiltin:            false,
				Description:          &description,
				InteractsWithColumns: []string{"email"},
				CreatedAt:            time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				UpdatedAt:            time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
			},
		},
	}
}

func TestDeleteRuleCommand_WithYes(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Mock all-releases endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID+"", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDeleteRuleFamily())
	})

	// Mock delete endpoint
	deleteCalledWith := ""
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID, func(w http.ResponseWriter, r *http.Request) {
		deleteCalledWith = testRuleRevisionID
		w.WriteHeader(http.StatusOK)
	})

	cmd := setupDeleteRuleTestCommand()
	cmd.SetArgs([]string{"delete", "rule", testRuleRevisionID, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if deleteCalledWith != testRuleRevisionID {
		t.Errorf("Delete was not called, expected rule revision ID %s", testRuleRevisionID)
	}
}

func TestDeleteRuleCommand_NoArgs(t *testing.T) {
	cmd := setupDeleteRuleTestCommand()
	cmd.SetArgs([]string{"delete", "rule"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when no arguments provided")
	}
}

func TestDeleteRuleCommand_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID+"", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Not found")
	})

	cmd := setupDeleteRuleTestCommand()
	cmd.SetArgs([]string{"delete", "rule", testRuleRevisionID, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when rule not found")
	}

	if !strings.Contains(err.Error(), "failed to fetch rule") {
		t.Errorf("Expected 'failed to fetch rule' error, got: %v", err)
	}
}

func TestDeleteRuleCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupDeleteRuleTestCommand()
	cmd.SetArgs([]string{"delete", "rule", testRuleRevisionID, "--yes"})

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

func TestDeleteRuleCommand_ByName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleDeleteRuleFamily()

	// Register list endpoint for name resolution
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

	// Register all-releases endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	// Register delete endpoint
	deleteCalledWith := ""
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID, func(w http.ResponseWriter, r *http.Request) {
		deleteCalledWith = testRuleRevisionID
		w.WriteHeader(http.StatusOK)
	})

	cmd := setupDeleteRuleTestCommand()
	cmd.SetArgs([]string{"delete", "rule", "email_validator", "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if deleteCalledWith != testRuleRevisionID {
		t.Errorf("Delete was not called, expected rule revision ID %s", testRuleRevisionID)
	}
}

func TestDeleteRuleCommand_ByShortID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleDeleteRuleFamily()

	// Register list endpoint for short ID resolution
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

	// Register all-releases endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	// Register delete endpoint
	deleteCalledWith := ""
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID, func(w http.ResponseWriter, r *http.Request) {
		deleteCalledWith = testRuleRevisionID
		w.WriteHeader(http.StatusOK)
	})

	// Use short ID: first 8 hex chars of 550e8400-e29b-41d4-a716-446655440000
	cmd := setupDeleteRuleTestCommand()
	cmd.SetArgs([]string{"delete", "rule", "550e8400", "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if deleteCalledWith != testRuleRevisionID {
		t.Errorf("Delete was not called, expected rule revision ID %s", testRuleRevisionID)
	}
}

func sampleDeleteMultiReleaseFamily() rule_versions.RuleRevisionsFamily {
	familyID := openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"))
	orgID := openapi_types.UUID(uuid.MustParse(testOrgID))
	ruleID1 := openapi_types.UUID(uuid.MustParse(testRuleRevisionID))
	ruleID2 := openapi_types.UUID(uuid.MustParse("660e8400-e29b-41d4-a716-446655440001"))
	description := "Validates email format"

	return rule_versions.RuleRevisionsFamily{
		FamilyID:       familyID,
		Name:           "email_validator",
		OrganizationID: orgID,
		Results: []rule_versions.RuleRevisionTiny{
			{
				ID:                   ruleID1,
				Name:                 "email_validator",
				Release:              "1.0.0",
				State:                "draft",
				IsDefault:            false,
				IsBuiltin:            false,
				Description:          &description,
				InteractsWithColumns: []string{"email"},
				CreatedAt:            time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				UpdatedAt:            time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
			},
			{
				ID:                   ruleID2,
				Name:                 "email_validator",
				Release:              "2.0.0",
				State:                "enabled",
				IsDefault:            true,
				IsBuiltin:            false,
				Description:          &description,
				InteractsWithColumns: []string{"email"},
				CreatedAt:            time.Date(2025, 7, 1, 10, 0, 0, 0, time.UTC),
				UpdatedAt:            time.Date(2025, 7, 5, 14, 0, 0, 0, time.UTC),
			},
		},
	}
}

func TestDeleteRuleCommand_MultiReleaseNeedsReleaseFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleDeleteMultiReleaseFamily()

	// Register list endpoint
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

	cmd := setupDeleteRuleTestCommand()
	cmd.SetArgs([]string{"delete", "rule", "email_validator", "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error when deleting a rule with multiple releases without --release")
	}

	if !strings.Contains(err.Error(), "multiple releases") {
		t.Errorf("Expected 'multiple releases' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--release") {
		t.Errorf("Error should suggest --release flag, got: %v", err)
	}
}

func TestDeleteRuleCommand_ResolutionSendsOnlyDefaultFalse(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleDeleteRuleFamily()

	// Capture the only_default query parameter sent during resolution
	var capturedOnlyDefault string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		capturedOnlyDefault = r.URL.Query().Get("only_default")
		totalRows := len(family.Results)
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   family.Results,
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cmd := setupDeleteRuleTestCommand()
	cmd.SetArgs([]string{"delete", "rule", "email_validator", "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if capturedOnlyDefault != "false" {
		t.Errorf("Expected only_default=false in query, got %q", capturedOnlyDefault)
	}
}

func TestDeleteRuleCommand_WithReleaseFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	family := sampleDeleteMultiReleaseFamily()

	// Register list endpoint
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

	// Register all-releases endpoint for confirmation context
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, family)
	})

	// Register delete endpoint
	deleteCalledWith := ""
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevisionID, func(w http.ResponseWriter, r *http.Request) {
		deleteCalledWith = testRuleRevisionID
		w.WriteHeader(http.StatusOK)
	})

	cmd := setupDeleteRuleTestCommand()
	cmd.SetArgs([]string{"delete", "rule", "email_validator", "--release", "1.0.0", "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if deleteCalledWith != testRuleRevisionID {
		t.Errorf("Delete was not called, expected rule revision ID %s", testRuleRevisionID)
	}
}
