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
	"github.com/qlustered/qctl/internal/rule_families"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupRuleFamiliesTestCommand creates a root command with global flags and adds the rules (families) command
func setupRuleFamiliesTestCommand() *cobra.Command {
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
	getCmd.AddCommand(NewRuleFamiliesCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleRuleFamilyItem() rule_families.RuleFamilyItem {
	familyID := openapi_types.UUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"))
	revID := openapi_types.UUID(uuid.MustParse("660e8400-e29b-41d4-a716-446655440001"))
	description := "Validates email format"

	return rule_families.RuleFamilyItem{
		FamilyID:  familyID,
		Name:      "email_validator",
		Slug:      "email_validator",
		IsBuiltin: false,
		PrimaryRevision: &rule_families.RuleFamilyRevisionTiny{
			ID:        revID,
			Release:   "1.0.0",
			State:     "enabled",
			IsDefault: true,
			IsBuiltin: false,
			IsCaf:     false,
			CreatedByUser: &api.UserInfoTinyDictSchema{
				FirstName: "John",
				LastName:  "Doe",
			},
			Description: &description,
			CreatedAt:   time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		},
	}
}

func sampleRuleFamilyItemWithSecondary() rule_families.RuleFamilyItem {
	familyID := openapi_types.UUID(uuid.MustParse("770e8400-e29b-41d4-a716-446655440002"))
	primaryID := openapi_types.UUID(uuid.MustParse("880e8400-e29b-41d4-a716-446655440003"))
	secondaryID := openapi_types.UUID(uuid.MustParse("990e8400-e29b-41d4-a716-446655440004"))
	desc1 := "Normalizes phone numbers"
	desc2 := "Normalizes phone numbers v2"
	hasNewer := true

	return rule_families.RuleFamilyItem{
		FamilyID:            familyID,
		Name:                "phone_normalizer",
		Slug:                "phone_normalizer",
		IsBuiltin:           true,
		HasNewerThanDefault: &hasNewer,
		PrimaryRevision: &rule_families.RuleFamilyRevisionTiny{
			ID:        primaryID,
			Release:   "1.0.0",
			State:     "enabled",
			IsDefault: true,
			IsBuiltin: true,
			IsCaf:     false,
			CreatedByUser: &api.UserInfoTinyDictSchema{
				FirstName: "Sarah",
				LastName:  "Miller",
			},
			Description: &desc1,
			CreatedAt:   time.Date(2025, 5, 10, 8, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2025, 5, 15, 12, 0, 0, 0, time.UTC),
		},
		SecondaryRevision: &rule_families.RuleFamilyRevisionTiny{
			ID:        secondaryID,
			Release:   "2.0.0",
			State:     "draft",
			IsDefault: false,
			IsBuiltin: true,
			IsCaf:     false,
			CreatedByUser: &api.UserInfoTinyDictSchema{
				FirstName: "Alex",
				LastName:  "Kim",
			},
			Description: &desc2,
			CreatedAt:   time.Date(2025, 6, 1, 8, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2025, 6, 5, 12, 0, 0, 0, time.UTC),
		},
	}
}

func TestGetRuleFamiliesCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockFamilies          []rule_families.RuleFamilyItem
		wantErr               bool
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{},
			mockFamilies: []rule_families.RuleFamilyItem{
				sampleRuleFamilyItem(),
			},
			wantOutputContains: []string{"email_validator", "1.0.0", "enabled", "Default", "John D."},
		},
		{
			name: "nil created_by_user shows dash in author column",
			args: []string{},
			mockFamilies: func() []rule_families.RuleFamilyItem {
				item := sampleRuleFamilyItem()
				item.PrimaryRevision.CreatedByUser = nil
				return []rule_families.RuleFamilyItem{item}
			}(),
			wantOutputContains: []string{"email_validator", "AUTHOR"},
		},
		{
			name: "with json output",
			args: []string{"--output", "json"},
			mockFamilies: []rule_families.RuleFamilyItem{
				sampleRuleFamilyItem(),
			},
			wantOutputContains: []string{`"slug": "email_validator"`, `"release": "1.0.0"`},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockFamilies: []rule_families.RuleFamilyItem{
				sampleRuleFamilyItem(),
			},
			wantOutputContains: []string{"slug: email_validator", "release: 1.0.0"},
		},
		{
			name: "multiple families",
			args: []string{},
			mockFamilies: []rule_families.RuleFamilyItem{
				sampleRuleFamilyItem(),
				sampleRuleFamilyItemWithSecondary(),
			},
			wantOutputContains: []string{"email_validator", "phone_normalizer"},
		},
		{
			name: "family with secondary revision shows new tag names",
			args: []string{},
			mockFamilies: []rule_families.RuleFamilyItem{
				sampleRuleFamilyItemWithSecondary(),
			},
			wantOutputContains:    []string{"phone_normalizer", "1.0.0", "Newer than default", "2.0.0", "draft"},
			wantOutputNotContains: []string{"* phone_normalizer", "Upgrade Available"},
		},
		{
			name: "primary revision with default, builtin and update available tags",
			args: []string{},
			mockFamilies: []rule_families.RuleFamilyItem{
				sampleRuleFamilyItemWithSecondary(),
			},
			wantOutputContains: []string{"Default, Built-in, Update available"},
		},
		{
			name: "non-default non-builtin rule shows dash tag",
			args: []string{},
			mockFamilies: func() []rule_families.RuleFamilyItem {
				item := sampleRuleFamilyItem()
				item.PrimaryRevision.IsDefault = false
				return []rule_families.RuleFamilyItem{item}
			}(),
			wantOutputContains: []string{"-"},
		},
		{
			name:         "empty results",
			args:         []string{},
			mockFamilies: []rule_families.RuleFamilyItem{},
			wantErr:      false,
		},
		{
			name:    "invalid sort field",
			args:    []string{"--order-by", "invalid_field"},
			wantErr: true,
		},
		{
			name: "with search query",
			args: []string{"--search", "email"},
			mockFamilies: []rule_families.RuleFamilyItem{
				sampleRuleFamilyItem(),
			},
			wantOutputContains: []string{"email_validator"},
		},
		{
			name: "with exclude-builtin flag",
			args: []string{"--exclude-builtin"},
			mockFamilies: []rule_families.RuleFamilyItem{
				sampleRuleFamilyItem(),
			},
			wantOutputContains: []string{"email_validator"},
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

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockFamilies)
				page := 1
				response := rule_families.RuleFamilyList{
					Results:   tt.mockFamilies,
					TotalRows: &totalRows,
					Page:      &page,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			cmd := setupRuleFamiliesTestCommand()
			args := append([]string{"get", "rules"}, tt.args...)

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

func TestGetRuleFamiliesCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules"})

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

func TestGetRuleFamiliesCommand_Pagination(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		pageNum := 1
		nextStart := 2
		response := rule_families.RuleFamilyList{
			Results:   []rule_families.RuleFamilyItem{sampleRuleFamilyItem()},
			TotalRows: &totalRows,
			Page:      &pageNum,
			Next:      &rule_families.PaginationSchema{Start: &nextStart},
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--output", "json"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 family in output, got %d", len(results))
	}

	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "More available") {
		t.Errorf("Expected pagination hint in stderr, got: %s", stderrOutput)
	}
}

func TestGetRuleFamiliesCommand_CustomColumns(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := rule_families.RuleFamilyList{
			Results:   []rule_families.RuleFamilyItem{sampleRuleFamilyItem()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--output", "table", "--columns", "name,release,state"})

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

func TestGetRuleFamiliesCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()

	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestGetRuleFamiliesCommand_ExcludeBuiltinQueryParam(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var capturedIncludeBuiltin string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		capturedIncludeBuiltin = r.URL.Query().Get("include_builtin")

		totalRows := 0
		page := 1
		response := rule_families.RuleFamilyList{
			Results:   []rule_families.RuleFamilyItem{},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--exclude-builtin"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if capturedIncludeBuiltin != "false" {
		t.Errorf("include_builtin query param: got %q, want %q", capturedIncludeBuiltin, "false")
	}
}

func TestGetRuleFamiliesCommand_SearchQueryParam(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var capturedSearch string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		capturedSearch = r.URL.Query().Get("search_query")

		totalRows := 0
		page := 1
		response := rule_families.RuleFamilyList{
			Results:   []rule_families.RuleFamilyItem{},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--search", "email"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if capturedSearch != "email" {
		t.Errorf("search_query query param: got %q, want %q", capturedSearch, "email")
	}
}
