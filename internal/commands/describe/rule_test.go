package describe

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
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testRuleRevisionID = "550e8400-e29b-41d4-a716-446655440000"

// setupRuleTestCommand creates a root command with global flags and adds the rule command
func setupRuleTestCommand() *cobra.Command {
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

	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a resource",
	}
	describeCmd.AddCommand(NewRuleCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

var (
	testFamilyID = uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	testRuleUUID = uuid.MustParse(testRuleRevisionID)
)

func sampleRuleDetail() rule_versions.RuleRevisionFull {
	description := "Validates email addresses"
	code := "def validate(self, value, row, params):\n    import re\n    return bool(re.match(r'^[\\w.-]+@[\\w.-]+\\.\\w+$', str(value)))\n"
	domains := []api.RuleRevisionProblemDomainSchema{{Name: "Customer records", Slug: "customer-records"}}
	bindings := []api.RuleRevisionFieldConceptSchema{{RuleFieldName: "email", Concept: api.RuleRevisionConceptSchema{Name: "Email address", Slug: "email-address"}}}

	return rule_versions.RuleRevisionFull{
		ID:               testRuleUUID,
		FamilyID:         testFamilyID,
		Name:             "email_validator",
		Slug:             "email_validator",
		Release:          "1.0.0",
		State:            "enabled",
		IsDefault:        true,
		IsBuiltin:        false,
		IsCaf:            false,
		Description:      &description,
		InputColumns:     []string{"email"},
		ValidatesColumns: []string{"email"},
		CorrectsColumns:  []string{},
		EnrichesColumns:  []string{},
		AffectedColumns:  []string{"email"},
		ProblemDomains:   &domains,
		FieldConcepts:    &bindings,
		ParamSchema:      map[string]interface{}{},
		Code:             &code,
		CreatedAt:        time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2025, 1, 20, 14, 0, 0, 0, time.UTC),
		CreatedByUser: &api.UserInfoTinyDictSchema{
			ID:        uuid.MustParse("bbbb0000-cccc-dddd-eeee-ffffffffffff"),
			Email:     "john@example.com",
			FirstName: "John",
			LastName:  "Doe",
		},
	}
}

func sampleRuleRevisionTiny() rule_versions.RuleRevisionTiny {
	description := "Validates email addresses"
	return rule_versions.RuleRevisionTiny{
		ID:              testRuleUUID,
		Name:            "email_validator",
		Release:         "1.0.0",
		State:           "enabled",
		IsDefault:       true,
		IsBuiltin:       false,
		Description:     &description,
		AffectedColumns: []string{"email"},
		CreatedAt:       time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2025, 1, 20, 14, 0, 0, 0, time.UTC),
	}
}

// multiReleaseTinyList returns two revisions of the same rule with different releases.
func multiReleaseTinyList() []rule_versions.RuleRevisionTiny {
	ruleID2 := uuid.MustParse("660e8400-e29b-41d4-a716-446655440001")
	desc1 := "Initial release"
	desc2 := "Improved accuracy"
	return []rule_versions.RuleRevisionTiny{
		{
			ID:              testRuleUUID,
			Name:            "email_validator",
			Release:         "1.0.0",
			State:           "disabled",
			IsDefault:       false,
			IsBuiltin:       false,
			Description:     &desc1,
			AffectedColumns: []string{"email"},
			CreatedAt:       time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:       time.Date(2025, 1, 20, 14, 0, 0, 0, time.UTC),
		},
		{
			ID:              ruleID2,
			Name:            "email_validator",
			Release:         "2.0.0",
			State:           "enabled",
			IsDefault:       true,
			IsBuiltin:       false,
			Description:     &desc2,
			AffectedColumns: []string{"email", "domain", "domain_type"},
			CreatedAt:       time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC),
			UpdatedAt:       time.Date(2025, 6, 10, 14, 0, 0, 0, time.UTC),
		},
	}
}

const detailEndpoint = "/api/orgs/" + testOrgID + "/rule-revisions/" + testRuleRevisionID + "/details"
const listEndpoint = "/api/orgs/" + testOrgID + "/rule-revisions"

func TestDescribeRuleCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockStatusCode     int
		wantErr            bool
		wantOutputContains []string
	}{
		{
			name:           "successful describe with UUID shows plain text",
			args:           []string{testRuleRevisionID},
			mockStatusCode: http.StatusOK,
			wantErr:        false,
			wantOutputContains: []string{
				"Name:              email_validator",
				"Release:           1.0.0",
				"State:             enabled",
				"Default:           yes",
				"Works with:        Customer records (customer-records)",
				"email          Email address (email-address)",
			},
		},
		{
			name:           "with json output",
			args:           []string{testRuleRevisionID, "--output", "json"},
			mockStatusCode: http.StatusOK,
			wantErr:        false,
			wantOutputContains: []string{
				`"name": "email_validator"`,
				`"release": "1.0.0"`,
				`"rule_field_name": "email"`,
				`"slug": "customer-records"`,
			},
		},
		{
			name:    "no arguments",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "too many arguments",
			args:    []string{testRuleRevisionID, "extra"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip server setup for invalid argument tests
			if strings.Contains(tt.name, "arguments") {
				cmd := setupRuleTestCommand()
				args := append([]string{"describe", "rule"}, tt.args...)
				cmd.SetArgs(args)

				var buf bytes.Buffer
				cmd.SetOut(&buf)
				cmd.SetErr(&buf)

				err := cmd.Execute()

				if (err != nil) != tt.wantErr {
					t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatusCode != http.StatusOK {
					testutil.RespondError(w, tt.mockStatusCode, "Error")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, sampleRuleDetail())
			})

			cmd := setupRuleTestCommand()
			args := append([]string{"describe", "rule"}, tt.args...)

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)

			err := cmd.Execute()

			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				output := buf.String()
				for _, want := range tt.wantOutputContains {
					if !strings.Contains(output, want) {
						t.Errorf("Output should contain %q, got:\n%s", want, output)
					}
				}
			}
		})
	}
}

func TestDescribeRuleCommand_PlainTextSections(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleDetail())
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", testRuleRevisionID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Should NOT look like a YAML manifest
	if strings.Contains(output, "apiVersion") {
		t.Errorf("Output should NOT contain 'apiVersion' (not a manifest), got:\n%s", output)
	}
	if strings.Contains(output, "kind: Rule") {
		t.Errorf("Output should NOT contain 'kind: Rule' (not a manifest), got:\n%s", output)
	}

	// Should contain all diagnostic sections
	checks := []string{
		"Name:",
		"Slug:",
		"Release:",
		"ID:",
		"Family ID:",
		"State:",
		"Default:",
		"Built-in:",
		"CAF:",
		"Description:",
		"Columns:",
		"  Input:",
		"  Validates:",
		"  Affected:",
		"Created by:",
		"Created:",
		"Updated:",
		"Code:",
	}
	for _, want := range checks {
		if !strings.Contains(output, want) {
			t.Errorf("Output should contain %q, got:\n%s", want, output)
		}
	}
}

func TestDescribeRuleCommand_ShowCode(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create a rule with many lines of code to test truncation
	var longCode strings.Builder
	for i := 0; i < 30; i++ {
		longCode.WriteString("    line " + strings.Repeat("x", i) + "\n")
	}
	codeStr := longCode.String()

	detail := sampleRuleDetail()
	detail.Code = &codeStr

	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	// Without --show-code: truncated
	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", testRuleRevisionID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "use --show-code for full") {
		t.Errorf("Truncated output should mention --show-code, got:\n%s", output)
	}
	if !strings.Contains(output, "...") {
		t.Errorf("Truncated output should contain '...', got:\n%s", output)
	}

	// With --show-code: full
	cmd2 := setupRuleTestCommand()
	cmd2.SetArgs([]string{"describe", "rule", testRuleRevisionID, "--show-code"})

	var buf2 bytes.Buffer
	cmd2.SetOut(&buf2)
	cmd2.SetErr(&buf2)

	err = cmd2.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output2 := buf2.String()
	if strings.Contains(output2, "use --show-code for full") {
		t.Errorf("Full output should NOT mention --show-code, got:\n%s", output2)
	}
	if !strings.Contains(output2, "30 lines") {
		t.Errorf("Full output should show '30 lines', got:\n%s", output2)
	}
}

func TestDescribeRuleCommand_VeryVerboseShowsRawDump(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleDetail())
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", testRuleRevisionID, "-vv"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// -vv output is YAML-encoded full API response (not the plain text view)
	if !strings.Contains(output, "email_validator") {
		t.Errorf("-vv output should contain 'email_validator', got:\n%s", output)
	}
	// Wire keys, as -vv -o json prints them.
	if !strings.Contains(output, "family_id:") || strings.Contains(output, "familyid") {
		t.Errorf("-vv output should key the family as family_id, got:\n%s", output)
	}
	// Should NOT be the plain text format
	if strings.Contains(output, "Name:              email_validator") {
		t.Errorf("-vv output should be raw dump, not plain text, got:\n%s", output)
	}
}

// TestDescribeRuleYAMLReadsAsJSON: each YAML view is the tree its -o json twin
// prints, including a union-typed affected_column_types entry.
func TestDescribeRuleYAMLReadsAsJSON(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleRuleDetail()
	var anyType api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties
	if err := json.Unmarshal([]byte(`"any"`), &anyType); err != nil {
		t.Fatal(err)
	}
	detail.AffectedColumnTypes = &map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties{"email": anyType}
	detail.ParamsRenderability = &map[string]api.FieldRenderabilitySchema{"pattern": {RenderableInBasicUI: true}}
	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	run := func(args ...string) string {
		t.Helper()
		cmd := setupRuleTestCommand()
		cmd.SetArgs(append([]string{"describe", "rule", testRuleRevisionID}, args...))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("describe rule %v: %v", args, err)
		}
		return buf.String()
	}

	t.Run("-o yaml", func(t *testing.T) {
		testutil.AssertYAMLReadsAsJSON(t, run("-o", "yaml"), run("-o", "json"))
	})
	t.Run("-vv", func(t *testing.T) {
		testutil.AssertYAMLReadsAsJSON(t, run("-vv"), run("-vv", "-o", "json"))
	})
}

func TestDescribeRuleCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", testRuleRevisionID})

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

func TestDescribeRuleCommand_ByName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register list endpoint for name resolution (single release — no --release needed)
	mock.RegisterHandler("GET", listEndpoint, func(w http.ResponseWriter, r *http.Request) {
		tiny := sampleRuleRevisionTiny()
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   []rule_versions.RuleRevisionTiny{tiny},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Register detail endpoint
	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleDetail())
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", "email_validator"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Name:              email_validator") {
		t.Errorf("Output should contain 'Name:              email_validator', got:\n%s", output)
	}
}

func TestDescribeRuleCommand_ByNameMultiRelease_NeedsReleaseFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	revisions := multiReleaseTinyList()

	// Register list endpoint — returns two releases
	mock.RegisterHandler("GET", listEndpoint, func(w http.ResponseWriter, r *http.Request) {
		totalRows := len(revisions)
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   revisions,
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", "email_validator"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error when describing multi-release rule without --release")
	}
	if !strings.Contains(err.Error(), "multiple releases") {
		t.Errorf("Expected 'multiple releases' error, got: %v", err)
	}
}

func TestDescribeRuleCommand_ByNameWithReleaseFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	revisions := multiReleaseTinyList()

	// Register list endpoint
	mock.RegisterHandler("GET", listEndpoint, func(w http.ResponseWriter, r *http.Request) {
		totalRows := len(revisions)
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   revisions,
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Register detail endpoint
	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleDetail())
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", "email_validator", "--release", "1.0.0"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Name:              email_validator") {
		t.Errorf("Output should contain rule name, got:\n%s", output)
	}
}

func TestDescribeRuleCommand_ByShortID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	tiny := sampleRuleRevisionTiny()

	// Register list endpoint for short ID resolution
	mock.RegisterHandler("GET", listEndpoint, func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_versions.RuleRevisionList{
			Results:   []rule_versions.RuleRevisionTiny{tiny},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Register detail endpoint
	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleDetail())
	})

	// Use short ID (first 8 hex chars of 550e8400-e29b-41d4-a716-446655440000)
	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", "550e8400"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "email_validator") {
		t.Errorf("Output should contain 'email_validator', got:\n%s", output)
	}
}

func TestDescribeRuleCommand_RuleNotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Rule not found")
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", testRuleRevisionID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when rule not found")
	}

	if !strings.Contains(err.Error(), "failed to get rule") {
		t.Errorf("Expected 'failed to get rule' error, got: %v", err)
	}
}

// describeAffectedColumnTypes builds the wire's union map so a fixture can say
// `{"email": ["String"]}` or `{"email": "any"}` without hand-rolling
// json.RawMessage.
func describeAffectedColumnTypes(t *testing.T, entries map[string]interface{}) *map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties {
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

// runDescribeRule serves one detail response and returns describe's stdout.
func runDescribeRule(t *testing.T, detail rule_versions.RuleRevisionFull) string {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", detailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupRuleTestCommand()
	cmd.SetArgs([]string{"describe", "rule", testRuleRevisionID})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return buf.String()
}

// The revision's default_severity decides what an instantiate that omits
// spec.severity gets, and the accepted field types are what the frontend
// shows beside each mapped column. Both have to survive the round trip
// through the HTTP response, not just the formatter -- affected_column_types
// in particular arrives as an opaque union that has to be decoded.
func TestDescribeRuleCommand_ShowsRevisionDefaults(t *testing.T) {
	detail := sampleRuleDetail()
	severity := api.RuleSeverityBlocker
	detail.DefaultSeverity = &severity
	detail.AffectedColumnTypes = describeAffectedColumnTypes(t, map[string]interface{}{
		"email": []string{"String", "Text"},
	})

	output := runDescribeRule(t, detail)

	if !strings.Contains(output, "blocker") {
		t.Errorf("expected the revision's default severity, got:\n%s", output)
	}
	if !strings.Contains(output, "String, Text") {
		t.Errorf("expected the accepted types for the mapped field, got:\n%s", output)
	}
}

// The shared fixture declares neither field -- the shape of every revision
// stored before types were declared. describe must then say nothing about
// either: no accepted-types block, and no severity word the response never
// sent.
func TestDescribeRuleCommand_OmitsUndeclaredRevisionDefaults(t *testing.T) {
	output := runDescribeRule(t, sampleRuleDetail())

	if strings.Contains(output, "Accepted types") {
		t.Errorf("expected no accepted-types block for a revision that declared none, got:\n%s", output)
	}
	for _, severity := range []string{"as_declared", "blocker"} {
		if strings.Contains(output, severity) {
			t.Errorf("output names severity %q, which the response never sent:\n%s", severity, output)
		}
	}
}
