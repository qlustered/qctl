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
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupRuleRevisionsTestCommand creates a root command with global flags and adds the rule-revisions command
func setupRuleRevisionsTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	// Add global flags (same as in root command)
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	// Add get command
	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewRuleRevisionsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleRuleRevisionTiny() rule_versions.RuleRevisionTiny {
	ruleID := openapi_types.UUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"))
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)
	description := "Validates email format"
	upgradeAvailable := false

	return rule_versions.RuleRevisionTiny{
		ID:               ruleID,
		Name:             "email_validator",
		Release:          "1.0.0",
		State:            "enabled",
		IsDefault:        true,
		IsBuiltin:        false,
		IsCaf:            false,
		Description:      &description,
		InteractsWithColumns: []string{"email"},
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
		UpgradeAvailable:     &upgradeAvailable,
	}
}

func sampleRuleRevisionTiny2() rule_versions.RuleRevisionTiny {
	ruleID := openapi_types.UUID(uuid.MustParse("660e8400-e29b-41d4-a716-446655440001"))
	createdAt := time.Date(2025, 5, 10, 8, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 5, 15, 12, 0, 0, 0, time.UTC)
	description := "Standardizes phone numbers"

	return rule_versions.RuleRevisionTiny{
		ID:                   ruleID,
		Name:                 "phone_normalizer",
		Release:              "2.1.0",
		State:                "draft",
		IsDefault:            false,
		IsBuiltin:            true,
		IsCaf:                true,
		Description:          &description,
		InteractsWithColumns: []string{"phone"},
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
	}
}

func TestGetRuleRevisionsCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockRules             []rule_versions.RuleRevisionTiny
		wantErr               bool
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{},
			mockRules: []rule_versions.RuleRevisionTiny{
				sampleRuleRevisionTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"email_validator", "1.0.0", "enabled", "550e8400", "Default"},
		},
		{
			name: "with json output",
			args: []string{"--output", "json"},
			mockRules: []rule_versions.RuleRevisionTiny{
				sampleRuleRevisionTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{`"name": "email_validator"`, `"release": "1.0.0"`},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockRules: []rule_versions.RuleRevisionTiny{
				sampleRuleRevisionTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"name: email_validator", "release: 1.0.0"},
		},
		{
			name: "multiple rules with tags",
			args: []string{},
			mockRules: []rule_versions.RuleRevisionTiny{
				sampleRuleRevisionTiny(),
				sampleRuleRevisionTiny2(),
			},
			wantErr: false,
			// email_validator: IsDefault=true → "Default"
			// phone_normalizer: IsBuiltin=true, IsCaf=true → "Built-in, CAF"
			wantOutputContains: []string{"email_validator", "phone_normalizer", "Default", "Built-in, CAF"},
		},
		{
			name: "with state filter",
			args: []string{"--state", "enabled"},
			mockRules: []rule_versions.RuleRevisionTiny{
				sampleRuleRevisionTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"email_validator"},
		},
		{
			name: "with state none filter",
			args: []string{"--state", "none"},
			mockRules: []rule_versions.RuleRevisionTiny{
				sampleRuleRevisionTiny(),
				sampleRuleRevisionTiny2(),
			},
			wantErr:            false,
			wantOutputContains: []string{"email_validator", "phone_normalizer"},
		},
		{
			name: "with search query",
			args: []string{"--search", "email"},
			mockRules: []rule_versions.RuleRevisionTiny{
				sampleRuleRevisionTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"email_validator"},
		},
		{
			name: "with sorting",
			args: []string{"--order-by", "created_at", "--reverse"},
			mockRules: []rule_versions.RuleRevisionTiny{
				sampleRuleRevisionTiny(),
			},
			wantErr: false,
		},
		{
			name:      "empty results",
			args:      []string{},
			mockRules: []rule_versions.RuleRevisionTiny{},
			wantErr:   false,
		},
		{
			name:    "invalid sort field",
			args:    []string{"--order-by", "invalid_field"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			// Create mock API server
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			// Setup config and credentials
			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			// Register mock handler for rule revisions
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockRules)
				page := 1
				response := rule_versions.RuleRevisionList{
					Results:   tt.mockRules,
					TotalRows: &totalRows,
					Page:      &page,
					Next:      nil,
					Previous:  nil,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			// Create command with proper hierarchy
			cmd := setupRuleRevisionsTestCommand()

			// Prepare args with "get rule-revisions" prefix
			args := append([]string{"get", "rule-revisions"}, tt.args...)

			// Capture output
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)

			// Execute command
			err := cmd.Execute()

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				output := buf.String()

				for _, want := range tt.wantOutputContains {
					if !strings.Contains(output, want) {
						t.Errorf("Output should contain %q, got: %s", want, output)
					}
				}

				for _, notWant := range tt.wantOutputNotContains {
					if strings.Contains(output, notWant) {
						t.Errorf("Output should not contain %q, got: %s", notWant, output)
					}
				}
			}
		})
	}
}

func TestGetRuleRevisionsCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs([]string{"get", "rule-revisions"})

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

func TestGetRuleRevisionsCommand_Pagination(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		pageNum := 1
		nextStart := 2
		response := rule_versions.RuleRevisionList{
			Results:   []rule_versions.RuleRevisionTiny{sampleRuleRevisionTiny()},
			TotalRows: &totalRows,
			Page:      &pageNum,
			Next:      &rule_versions.PaginationSchema{Start: &nextStart},
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs([]string{"get", "rule-revisions", "--output", "json"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Verify output contains results (parse as generic to avoid UUID unmarshal issues)
	var results []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 rule in output, got %d", len(results))
	}

	// Verify stderr contains pagination hint
	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "More available") {
		t.Errorf("Expected pagination hint in stderr, got: %s", stderrOutput)
	}
}

func TestGetRuleRevisionsCommand_CustomColumns(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   []rule_versions.RuleRevisionTiny{sampleRuleRevisionTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs([]string{"get", "rule-revisions", "--output", "table", "--columns", "name,release,state"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	upperOutput := strings.ToUpper(output)
	if !strings.Contains(upperOutput, "NAME") || !strings.Contains(upperOutput, "RELEASE") || !strings.Contains(upperOutput, "STATE") {
		t.Errorf("Expected NAME, RELEASE, STATE columns in table output, got: %s", output)
	}
}

func TestGetRuleRevisionsCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs([]string{"get", "rule-revisions"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestGetRuleRevisionsCommand_ShortIDColumn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   []rule_versions.RuleRevisionTiny{sampleRuleRevisionTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs([]string{"get", "rule-revisions", "--output", "table"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	upperOutput := strings.ToUpper(output)

	// Default columns should include SHORT-ID header (printer converts underscores to dashes)
	if !strings.Contains(upperOutput, "SHORT-ID") {
		t.Errorf("Default table output should contain SHORT-ID header, got: %s", output)
	}

	// Should contain the actual short ID value
	if !strings.Contains(output, "550e8400") {
		t.Errorf("Default table output should contain short ID '550e8400', got: %s", output)
	}

	// Default output should NOT contain full UUID
	if strings.Contains(output, "550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("Default table output should NOT contain full UUID, got: %s", output)
	}

	// Should contain TAGS header
	if !strings.Contains(upperOutput, "TAGS") {
		t.Errorf("Default table output should contain TAGS header, got: %s", output)
	}

	// Should contain "Default" tag (since IsDefault=true)
	if !strings.Contains(output, "Default") {
		t.Errorf("Default table output should contain 'Default' tag, got: %s", output)
	}
}

func TestGetRuleRevisionsCommand_VerboseShowsFullID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   []rule_versions.RuleRevisionTiny{sampleRuleRevisionTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs([]string{"get", "rule-revisions", "--output", "table", "-v"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Verbose output should contain full ID
	if !strings.Contains(output, "550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("Verbose table output should contain full UUID, got: %s", output)
	}
}

func TestGetRuleRevisionsCommand_BoolFilterFlags(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantErr        bool
		wantErrContain string
		wantIsDefault  string // expected query param value, "" means not sent
		wantHasUpgrade string
	}{
		{
			name:          "only-default true",
			args:          []string{"--only-default", "true"},
			wantIsDefault: "true",
		},
		{
			name:          "only-default false",
			args:          []string{"--only-default", "false"},
			wantIsDefault: "false",
		},
		{
			name:          "only-default 1",
			args:          []string{"--only-default", "1"},
			wantIsDefault: "true",
		},
		{
			name:          "only-default 0",
			args:          []string{"--only-default", "0"},
			wantIsDefault: "false",
		},
		{
			name:           "only-default invalid value",
			args:           []string{"--only-default", "banana"},
			wantErr:        true,
			wantErrContain: "invalid value",
		},
		{
			name:          "only-default omitted",
			args:          []string{},
			wantIsDefault: "",
		},
		{
			name:           "has-upgrade-available true",
			args:           []string{"--has-upgrade-available", "true"},
			wantHasUpgrade: "true",
		},
		{
			name:           "has-upgrade-available false",
			args:           []string{"--has-upgrade-available", "false"},
			wantHasUpgrade: "false",
		},
		{
			name:           "has-upgrade-available invalid value",
			args:           []string{"--has-upgrade-available", "invalid"},
			wantErr:        true,
			wantErrContain: "invalid value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			var capturedIsDefault, capturedHasUpgrade string
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
				capturedIsDefault = r.URL.Query().Get("only_default")
				capturedHasUpgrade = r.URL.Query().Get("has_upgrade_available")

				totalRows := 0
				page := 1
				response := rule_versions.RuleRevisionList{
					Results:   []rule_versions.RuleRevisionTiny{},
					TotalRows: &totalRows,
					Page:      &page,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			cmd := setupRuleRevisionsTestCommand()
			args := append([]string{"get", "rule-revisions"}, tt.args...)

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)

			err := cmd.Execute()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErrContain, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if capturedIsDefault != tt.wantIsDefault {
				t.Errorf("only_default query param: got %q, want %q", capturedIsDefault, tt.wantIsDefault)
			}
			if capturedHasUpgrade != tt.wantHasUpgrade {
				t.Errorf("has_upgrade_available query param: got %q, want %q", capturedHasUpgrade, tt.wantHasUpgrade)
			}
		})
	}
}

func TestGetRuleRevisionsCommand_StateNoneFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var capturedStateFilter string
	var stateFilterPresent bool
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		capturedStateFilter = r.URL.Query().Get("state_filter")
		stateFilterPresent = r.URL.Query().Has("state_filter")

		totalRows := 0
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   []rule_versions.RuleRevisionTiny{},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs([]string{"get", "rule-revisions", "--state", "none"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stateFilterPresent {
		t.Errorf("state_filter query param should not be present when --state none, got %q", capturedStateFilter)
	}
}
