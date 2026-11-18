package get

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
	ruleID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)
	description := "Validates email format"
	upgradeAvailable := false

	return rule_versions.RuleRevisionTiny{
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
	}
}

func sampleRuleRevisionTiny2() rule_versions.RuleRevisionTiny {
	ruleID := uuid.MustParse("660e8400-e29b-41d4-a716-446655440001")
	createdAt := time.Date(2025, 5, 10, 8, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 5, 15, 12, 0, 0, 0, time.UTC)
	description := "Standardizes phone numbers"

	return rule_versions.RuleRevisionTiny{
		ID:              ruleID,
		Name:            "phone_normalizer",
		Slug:            "phone_normalizer",
		Release:         "2.1.0",
		State:           "draft",
		IsDefault:       false,
		IsBuiltin:       true,
		IsCaf:           true,
		Description:     &description,
		AffectedColumns: []string{"phone"},
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
}

func sampleRuleRevisionMatch() api.RuleRevisionMatchSchema {
	return api.RuleRevisionMatchSchema{
		ID:   uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		Slug: "email_validator", Name: "email_validator", Release: "1.0.0",
		State: "enabled", IsDefault: true, MatchKind: "partial",
		MatchedConceptCount: 1, RequiredConceptCount: 2,
		MissingConcepts: []api.RuleRevisionConceptSchema{{Slug: "email-address"}},
	}
}

func TestRuleRevisionMatchesForTable(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	requests := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-revision-matches", func(w http.ResponseWriter, r *http.Request) {
		requests++
		query := r.URL.Query()
		if query.Get("match") != "concept" || query.Get("page") != "2" || query.Get("limit") != "20" {
			t.Errorf("match query = %s", r.URL.RawQuery)
		}
		if len(query) != 3 {
			t.Errorf("unexpected match query: %s", r.URL.RawQuery)
		}
		total, page := 1, 2
		testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionMatchListSchema{
			Results:   []api.RuleRevisionMatchSchema{sampleRuleRevisionMatch()},
			TotalRows: &total, Page: &page, FullCount: 3, PartialCount: 4, GenericCount: 5,
		})
	})
	cmd := setupRuleRevisionsTestCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"get", "rule-revisions", "--for-table", "42", "--page", "2"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if requests != 1 {
		t.Errorf("match requests = %d, want 1", requests)
	}
	for _, want := range []string{"MATCH", "STANDARD-FIELDS", "MISSING", "partial", "1/2", "email-address", "550e8400"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("table output missing %q: %s", want, stdout.String())
		}
	}
	if !strings.Contains(stderr.String(), "Full: 3  Partial: 4  Generic: 5") {
		t.Errorf("counts missing: %s", stderr.String())
	}
}

func TestRuleRevisionMatchesJSONUsesRawItems(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-revision-matches", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("match"); got != "generic" {
			t.Errorf("match = %q", got)
		}
		total, page := 1, 1
		match := sampleRuleRevisionMatch()
		match.MatchKind = "generic"
		match.MatchedConceptCount = 0
		match.RequiredConceptCount = 0
		match.MissingConcepts = []api.RuleRevisionConceptSchema{}
		testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionMatchListSchema{
			Results: []api.RuleRevisionMatchSchema{match}, TotalRows: &total, Page: &page,
		})
	})
	cmd := setupRuleRevisionsTestCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"get", "rule-revisions", "--for-table", "42", "--match", "generic", "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	list, rows := decodeRuleRevisionList(t, "json", stdout.Bytes())
	if len(rows) != 1 || rows[0]["match_kind"] != "generic" || rows[0]["matched_concept_count"] != float64(0) {
		t.Errorf("JSON match items = %#v", rows)
	}
	if _, ok := rows[0]["short_id"]; ok {
		t.Errorf("JSON match item has the display key short_id: %v", rows[0])
	}
	assertListPaging(t, list, map[string]*int{"page": intPtr(1), "limit": intPtr(20), "total": intPtr(1), "next_page": nil})
	if stderr.Len() != 0 {
		t.Errorf("-o json stderr = %q, want nothing", stderr.String())
	}
	nameCmd := setupRuleRevisionsTestCommand()
	var names bytes.Buffer
	nameCmd.SetOut(&names)
	nameCmd.SetArgs([]string{"get", "rule-revisions", "--for-table", "42", "--match", "generic", "-o", "name"})
	if err := nameCmd.Execute(); err != nil {
		t.Fatalf("name Execute: %v", err)
	}
	if got := strings.TrimSpace(names.String()); got != "rule/"+sampleRuleRevisionMatch().ID.String() {
		t.Errorf("name output = %q", got)
	}
}

// offsetPagedRuleRevisionMatch builds row id of an offset-paged
// rule-revision-matches fixture.
func offsetPagedRuleRevisionMatch(id int) interface{} {
	match := sampleRuleRevisionMatch()
	match.ID = uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", id))
	return match
}

// TestRuleRevisionMatchesPagination serves pages the way the match endpoint
// does: total_rows and no next.
func TestRuleRevisionMatchesPagination(t *testing.T) {
	const counts = "Match counts: Full: 0  Partial: 0  Generic: 0\n"
	const hint = "Showing 1-2 of 5 rule revisions (page 1 of 3). More available: use --page 2 or add filters.\n"
	tests := []struct {
		name       string
		format     string
		wantStderr string
	}{
		{name: "json carries the paging in the envelope", format: "json"},
		{name: "yaml carries the paging in the envelope", format: "yaml"},
		{name: "table hints on stderr after the counts", format: "table", wantStderr: counts + hint},
		{name: "name hints on stderr", format: "name", wantStderr: hint},
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
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-revision-matches",
				testutil.MockOffsetPagedHandler(5, offsetPagedRuleRevisionMatch))

			cmd := setupRuleRevisionsTestCommand()
			cmd.SetArgs([]string{"get", "rule-revisions", "--for-table", "42", "--limit", "2", "-o", tt.format})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if stderr.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}
			if tt.format == "table" || tt.format == "name" {
				return
			}
			list, items := decodeRuleRevisionList(t, tt.format, stdout.Bytes())
			if len(items) != 2 || items[0]["match_kind"] != "partial" {
				t.Errorf("items = %v, want 2 API rows", items)
			}
			assertListPaging(t, list, map[string]*int{"page": intPtr(1), "limit": intPtr(2), "total": intPtr(5), "next_page": intPtr(2)})
		})
	}
}

// An empty --for-table page still writes the match counts to stderr: after
// the empty-result message under table, and beside an empty List under json.
func TestRuleRevisionMatchesEmptyPage(t *testing.T) {
	const counts = "Match counts: Full: 3  Partial: 4  Generic: 0\n"
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-revision-matches", func(w http.ResponseWriter, _ *http.Request) {
				total, page := 0, 1
				testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionMatchListSchema{
					Results: []api.RuleRevisionMatchSchema{}, TotalRows: &total, Page: &page, FullCount: 3, PartialCount: 4,
				})
			})
			cmd := setupRuleRevisionsTestCommand()
			cmd.SetArgs([]string{"get", "rule-revisions", "--for-table", "42", "--match", "generic", "-o", format})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.HasSuffix(stderr.String(), counts) {
				t.Errorf("stderr = %q, want it to end with %q", stderr.String(), counts)
			}
			if format == "table" {
				if !strings.HasPrefix(stderr.String(), "No rule revisions matched --for-table") || stdout.Len() != 0 {
					t.Errorf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
				}
				return
			}
			list, items := decodeRuleRevisionList(t, format, stdout.Bytes())
			if len(items) != 0 {
				t.Errorf("items = %v, want []", items)
			}
			assertListPaging(t, list, map[string]*int{"page": intPtr(1), "limit": intPtr(20), "total": intPtr(0), "next_page": nil})
		})
	}
}

func TestRuleRevisionMatchFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--for-table", "42", "--order-by", "name"},
		{"--for-table", "42", "--reverse"},
		{"--for-table", "42", "--state", "enabled"},
		{"--for-table", "42", "--only-default", "true"},
		{"--for-table", "42", "--has-upgrade-available", "true"},
		{"--for-table", "42", "--general"},
		{"--for-table", "42", "--match", "generic", "--table-kind", "vehicle"},
		{"--for-table", "42", "--search", "ab"},
		{"--for-table", "42", "--match", "unknown"},
		{"--for-table", ""},
		{"--match", "generic"},
	} {
		cmd := NewRuleRevisionsCommand()
		cmd.SetArgs(args)
		err := cmd.Execute()
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("%v: exit = %d, want bad usage: %v", args, got, err)
		}
	}
}

func TestRuleRevisionMatchesFilteredQuery(t *testing.T) {
	t.Run("names", func(t *testing.T) {
		testRuleRevisionMatchesFilteredQuery(t, func(kinds []api.ProblemDomainTinySchema) []string {
			return []string{"--for-table", "42", "--table-kind", kinds[0].Name, "--table-kind", kinds[1].Name}
		})
	})
	t.Run("kind refs", func(t *testing.T) {
		testRuleRevisionMatchesFilteredQuery(t, func(kinds []api.ProblemDomainTinySchema) []string {
			return []string{"--for-table", "table/42", "--table-kind", "table-kind/" + kinds[0].ID.String(), "--table-kind", kinds[1].Name}
		})
	})
}

func testRuleRevisionMatchesFilteredQuery(t *testing.T, selectors func([]api.ProblemDomainTinySchema) []string) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	kinds := []api.ProblemDomainTinySchema{
		{ID: uuid.MustParse("660e8400-e29b-41d4-a716-446655440001"), OrganizationID: uuid.MustParse(testOrgID), Slug: "vehicle-inventory", Name: "Vehicle Inventory", UpdatedAt: time.Now()},
		{ID: uuid.MustParse("770e8400-e29b-41d4-a716-446655440002"), OrganizationID: uuid.MustParse(testOrgID), Slug: "person-records", Name: "Person Records", UpdatedAt: time.Now()},
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
		for _, kind := range kinds {
			if r.URL.Query().Get("search_query") == kind.Name {
				total, page := 1, 1
				testutil.RespondJSON(w, http.StatusOK, api.ProblemDomainsListSchema{Results: []api.ProblemDomainTinySchema{kind}, TotalRows: &total, Page: &page})
				return
			}
		}
		t.Errorf("unexpected kind search: %s", r.URL.RawQuery)
	})
	for _, kind := range kinds {
		kind := kind
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains/"+kind.ID.String(), func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ProblemDomainWithConceptsSchema{ID: kind.ID, OrganizationID: kind.OrganizationID, Slug: kind.Slug, Name: kind.Name})
		})
	}
	requests := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-revision-matches", func(w http.ResponseWriter, r *http.Request) {
		requests++
		query := r.URL.Query()
		if got := query["problem_domain_slug"]; len(got) != 2 || got[0] != kinds[0].Slug || got[1] != kinds[1].Slug {
			t.Errorf("problem_domain_slug = %v", got)
		}
		if got := query.Get("search_query"); got != "email" {
			t.Errorf("search_query = %q", got)
		}
		total, page := 0, 1
		testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionMatchListSchema{Results: []api.RuleRevisionMatchSchema{}, TotalRows: &total, Page: &page})
	})
	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs(append([]string{"get", "rule-revisions", "--search", "email"}, selectors(kinds)...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if requests != 1 {
		t.Errorf("match requests = %d, want 1", requests)
	}
}

func TestRuleRevisionMatchHTTPExitCodes(t *testing.T) {
	for _, tc := range []struct{ status, exit int }{{http.StatusForbidden, qerrors.ExitUnauthorized}, {http.StatusNotFound, qerrors.ExitNotFound}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-revision-matches", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondError(w, tc.status, "table unavailable")
			})
			cmd := setupRuleRevisionsTestCommand()
			cmd.SetArgs([]string{"get", "rule-revisions", "--for-table", "42"})
			if got := qerrors.GetExitCode(cmd.Execute()); got != tc.exit {
				t.Errorf("exit = %d, want %d", got, tc.exit)
			}
		})
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
			wantOutputContains: []string{`"kind": "List"`, `"slug": "email_validator"`, `"release": "1.0.0"`},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockRules: []rule_versions.RuleRevisionTiny{
				sampleRuleRevisionTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"kind: List", "slug: email_validator", "release: 1.0.0"},
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

func TestRuleRevisionTableKindFilterAndFacets(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	kindID := uuid.MustParse("660e8400-e29b-41d4-a716-446655440001")
	kind := api.ProblemDomainTinySchema{ID: kindID, OrganizationID: uuid.MustParse(testOrgID), Slug: "vehicle-inventory", Name: "Vehicle Inventory", UpdatedAt: time.Now()}
	otherID := uuid.MustParse("770e8400-e29b-41d4-a716-446655440002")
	other := api.ProblemDomainTinySchema{ID: otherID, OrganizationID: uuid.MustParse(testOrgID), Slug: "person-records", Name: "Person Records", UpdatedAt: time.Now()}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
		result := kind
		if got := r.URL.Query().Get("search_query"); got == "Person" {
			result = other
		} else if got != "Vehicle" {
			t.Errorf("table-kind search = %q", got)
		}
		total, page := 1, 1
		testutil.RespondJSON(w, http.StatusOK, api.ProblemDomainsListSchema{Results: []api.ProblemDomainTinySchema{result}, TotalRows: &total, Page: &page})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains/"+kindID.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.ProblemDomainWithConceptsSchema{ID: kindID, OrganizationID: uuid.MustParse(testOrgID), Slug: kind.Slug, Name: kind.Name})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains/"+otherID.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.ProblemDomainWithConceptsSchema{ID: otherID, OrganizationID: uuid.MustParse(testOrgID), Slug: other.Slug, Name: other.Name})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query()["problem_domain_slug"]; len(got) != 2 || got[0] != kind.Slug || got[1] != other.Slug {
			t.Errorf("problem_domain_slug = %v", got)
		}
		if _, ok := r.URL.Query()["general_only"]; ok {
			t.Error("general_only should be absent")
		}
		total, page := 1, 1
		testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{
			Results: []rule_versions.RuleRevisionTiny{sampleRuleRevisionTiny()}, TotalRows: &total, Page: &page,
			ProblemDomainFacets: []api.ProblemDomainFacetItem{{Slug: kind.Slug, Name: kind.Name, Count: 3}}, GeneralCount: 2,
		})
	})

	cmd := setupRuleRevisionsTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"get", "rule-revisions", "--table-kind", "Vehicle", "--table-kind", "Person"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"email_validator", "Table-kind counts:", "Vehicle Inventory (vehicle-inventory): 3", "General: 2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q: %s", want, out.String())
		}
	}

	structured := setupRuleRevisionsTestCommand()
	var jsonOut, jsonErr bytes.Buffer
	structured.SetOut(&jsonOut)
	structured.SetErr(&jsonErr)
	structured.SetArgs([]string{"get", "rule-revisions", "--table-kind", "Vehicle", "--table-kind", "Person", "-o", "json"})
	if err := structured.Execute(); err != nil {
		t.Fatalf("JSON Execute: %v", err)
	}
	if jsonErr.Len() != 0 {
		t.Errorf("-o json stderr = %q, want nothing", jsonErr.String())
	}
	if _, items := decodeRuleRevisionList(t, "json", jsonOut.Bytes()); len(items) != 1 || items[0]["slug"] != "email_validator" {
		t.Errorf("JSON items = %v", items)
	}
}

func TestRuleRevisionGeneralFilter(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("general_only"); got != "true" {
			t.Errorf("general_only = %q, want true", got)
		}
		if _, ok := r.URL.Query()["problem_domain_slug"]; ok {
			t.Error("problem_domain_slug should be absent")
		}
		total, page := 0, 1
		testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{Results: []rule_versions.RuleRevisionTiny{}, TotalRows: &total, Page: &page, GeneralCount: 4})
	})
	cmd := setupRuleRevisionsTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"get", "rule-revisions", "--general"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "General: 4") {
		t.Errorf("empty-result facet footer missing: %s", out.String())
	}
}

func TestRuleRevisionTableKindValidation(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		cmd := NewRuleRevisionsCommand()
		cmd.SetArgs([]string{"--table-kind", "vehicle", "--general"})
		err := cmd.Execute()
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("exit code = %d, want bad usage: %v", got, err)
		}
	})
	t.Run("unknown kind", func(t *testing.T) {
		env := testutil.NewTestEnv(t)
		defer env.Cleanup()
		mock := testutil.NewMockAPIServer()
		defer mock.Close()
		endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
		env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
		env.SetupCredential(endpointKey, testOrgID, "test-token")
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, _ *http.Request) {
			total, page := 0, 1
			testutil.RespondJSON(w, http.StatusOK, api.ProblemDomainsListSchema{Results: []api.ProblemDomainTinySchema{}, TotalRows: &total, Page: &page})
		})
		cmd := setupRuleRevisionsTestCommand()
		cmd.SetArgs([]string{"get", "rule-revisions", "--table-kind", "typo"})
		err := cmd.Execute()
		if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
			t.Errorf("exit code = %d, want not found: %v", got, err)
		}
	})
}

func TestRuleRevisionOrderByContract(t *testing.T) {
	t.Run("help lists the complete generated vocabulary", func(t *testing.T) {
		cmd := NewRuleRevisionsCommand()
		usage := cmd.Flags().Lookup("order-by").Usage
		for _, value := range []string{"id", "name", "release", "is_builtin", "created_at", "relevance"} {
			if !strings.Contains(usage, value) {
				t.Errorf("--order-by help omits %q: %s", value, usage)
			}
		}
		for _, explanation := range []string{"--search", "falls back to name"} {
			if !strings.Contains(usage, explanation) {
				t.Errorf("--order-by help does not explain relevance (%q missing): %s", explanation, usage)
			}
		}
	})

	t.Run("unknown value is bad usage", func(t *testing.T) {
		cmd := NewRuleRevisionsCommand()
		if err := cmd.Flags().Set("order-by", "not_a_column"); err != nil {
			t.Fatalf("set flag: %v", err)
		}
		_, err := parseRuleRevisionsParams(cmd)
		if err == nil {
			t.Fatal("expected invalid --order-by to fail")
		}
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
		}
	})

	t.Run("relevance reaches the request", func(t *testing.T) {
		env := testutil.NewTestEnv(t)
		defer env.Cleanup()
		mock := testutil.NewMockAPIServer()
		defer mock.Close()
		endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
		env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
		env.SetupCredential(endpointKey, testOrgID, "test-token")

		var gotOrder string
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
			gotOrder = r.URL.Query().Get("order_by")
			total, page := 0, 1
			testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{
				Results: []rule_versions.RuleRevisionTiny{}, TotalRows: &total, Page: &page,
			})
		})

		cmd := setupRuleRevisionsTestCommand()
		cmd.SetArgs([]string{"get", "rule-revisions", "--search", "email", "--order-by", "relevance"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if gotOrder != "relevance" {
			t.Errorf("order_by = %q, want relevance", gotOrder)
		}
	})
}

func TestGetRuleRevisionsCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

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

// offsetPagedRuleRevision builds row id of an offset-paged rule-revisions
// fixture.
func offsetPagedRuleRevision(id int) interface{} {
	return testutil.MakeRuleRevisionTiny(fmt.Sprintf("00000000-0000-4000-8000-%012d", id), fmt.Sprintf("rule_%d", id), "1.0.0", "enabled")
}

// decodeRuleRevisionList decodes a -o json|yaml List envelope, failing the
// test unless its top-level keys are exactly the envelope's six.
func decodeRuleRevisionList(t *testing.T, format string, raw []byte) (output.List, []map[string]interface{}) {
	t.Helper()
	var keys map[string]interface{}
	var list output.List
	var err error
	if format == "yaml" {
		if err = yaml.Unmarshal(raw, &keys); err == nil {
			err = yaml.Unmarshal(raw, &list)
		}
	} else if err = json.Unmarshal(raw, &keys); err == nil {
		err = json.Unmarshal(raw, &list)
	}
	if err != nil {
		t.Fatalf("-o %s did not decode as a List: %v\n%s", format, err, raw)
	}
	if got, want := slices.Sorted(maps.Keys(keys)), []string{"items", "kind", "limit", "next_page", "page", "total"}; !slices.Equal(got, want) {
		t.Errorf("envelope keys = %v, want %v", got, want)
	}
	if list.Kind != "List" {
		t.Errorf("kind = %q, want List", list.Kind)
	}
	var items []map[string]interface{}
	itemsJSON, _ := json.Marshal(list.Items)
	if err := json.Unmarshal(itemsJSON, &items); err != nil {
		t.Fatalf("items did not decode: %v\n%s", err, itemsJSON)
	}
	return list, items
}

// assertListPaging fails unless list's page, limit, total and next_page are
// want's, with a nil want entry meaning null.
func assertListPaging(t *testing.T, list output.List, want map[string]*int) {
	t.Helper()
	for name, got := range map[string]*int{"page": list.Page, "limit": list.Limit, "total": list.Total, "next_page": list.NextPage} {
		if w := want[name]; (got == nil) != (w == nil) || (got != nil && *got != *w) {
			t.Errorf("%s = %v, want %v", name, deref(got), deref(w))
		}
	}
}

// TestGetRuleRevisionsCommand_Pagination serves pages the way atlas's OFFSET
// path does: total_rows and no next.
func TestGetRuleRevisionsCommand_Pagination(t *testing.T) {
	const hint = "Showing 1-2 of 5 rule revisions (page 1 of 3). More available: use --page 2 or add filters.\n"
	tests := []struct {
		name      string
		args      []string
		format    string
		wantItems int
		wantPage  map[string]*int
		// wantStderr is the whole of stderr.
		wantStderr string
	}{
		{name: "json carries the paging in the envelope", args: []string{"--limit", "2"}, format: "json", wantItems: 2,
			wantPage: map[string]*int{"page": intPtr(1), "limit": intPtr(2), "total": intPtr(5), "next_page": intPtr(2)}},
		{name: "yaml carries the paging in the envelope", args: []string{"--limit", "2"}, format: "yaml", wantItems: 2,
			wantPage: map[string]*int{"page": intPtr(1), "limit": intPtr(2), "total": intPtr(5), "next_page": intPtr(2)}},
		{name: "last page has a null next_page", args: []string{"--limit", "2", "--page", "3"}, format: "json", wantItems: 1,
			wantPage: map[string]*int{"page": intPtr(3), "limit": intPtr(2), "total": intPtr(5), "next_page": nil}},
		{name: "table hints on stderr after the facets", args: []string{"--limit", "2"}, format: "table",
			wantStderr: "Table-kind counts:\n  General: 0\n" + hint},
		{name: "name hints on stderr", args: []string{"--limit", "2"}, format: "name", wantStderr: hint},
		{name: "table on the last page has no hint", args: []string{"--limit", "2", "--page", "3"}, format: "table",
			wantStderr: "Table-kind counts:\n  General: 0\n"},
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
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", testutil.MockOffsetPagedHandler(5, offsetPagedRuleRevision))

			cmd := setupRuleRevisionsTestCommand()
			cmd.SetArgs(append([]string{"get", "rule-revisions", "-o", tt.format}, tt.args...))
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if tt.format == "table" || tt.format == "name" {
				if stderr.String() != tt.wantStderr {
					t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
				}
				return
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing: the envelope carries the paging", stderr.String())
			}
			list, items := decodeRuleRevisionList(t, tt.format, stdout.Bytes())
			if len(items) != tt.wantItems {
				t.Errorf("%d items, want %d", len(items), tt.wantItems)
			}
			assertListPaging(t, list, tt.wantPage)
		})
	}
}

// Under -o json the items are the API's rows: a full id, is_default rather
// than a joined tags cell, and no short_id.
func TestGetRuleRevisionsJSONItemsAreRaw(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, _ *http.Request) {
		total, page := 1, 1
		testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{
			Results: []rule_versions.RuleRevisionTiny{sampleRuleRevisionTiny()}, TotalRows: &total, Page: &page,
		})
	})

	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs([]string{"get", "rule-revisions", "-o", "json"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	list, items := decodeRuleRevisionList(t, "json", stdout.Bytes())
	assertListPaging(t, list, map[string]*int{"page": intPtr(1), "limit": intPtr(100), "total": intPtr(1), "next_page": nil})
	if len(items) != 1 {
		t.Fatalf("items = %v, want 1", items)
	}
	item := items[0]
	if item["id"] != sampleRuleRevisionTiny().ID.String() || item["is_default"] != true || item["name"] != "email_validator" {
		t.Errorf("item = %v, want the API row", item)
	}
	for _, projected := range []string{"tags", "short_id"} {
		if _, ok := item[projected]; ok {
			t.Errorf("item has display key %q: %v", projected, item)
		}
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
	cmd.SetArgs([]string{"get", "rule-revisions", "--output", "table", "--columns", "slug,release,state"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	upperOutput := strings.ToUpper(output)
	if !strings.Contains(upperOutput, "SLUG") || !strings.Contains(upperOutput, "RELEASE") || !strings.Contains(upperOutput, "STATE") {
		t.Errorf("Expected SLUG, RELEASE, STATE columns in table output, got: %s", output)
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
		wantState      string
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
			name:          "draft implies all revisions",
			args:          []string{"--state", "draft"},
			wantState:     "draft",
			wantIsDefault: "false",
		},
		{
			name:          "in_review implies all revisions",
			args:          []string{"--state", "in_review"},
			wantState:     "in_review",
			wantIsDefault: "false",
		},
		{
			name:          "disabled implies all revisions",
			args:          []string{"--state", "disabled"},
			wantState:     "disabled",
			wantIsDefault: "false",
		},
		{
			name:      "enabled keeps server default",
			args:      []string{"--state", "enabled"},
			wantState: "enabled",
		},
		{
			name: "none keeps server default",
			args: []string{"--state", "none"},
		},
		{
			name:          "explicit true wins for draft",
			args:          []string{"--state", "draft", "--only-default", "true"},
			wantState:     "draft",
			wantIsDefault: "true",
		},
		{
			name:          "explicit false wins for enabled",
			args:          []string{"--state", "enabled", "--only-default", "false"},
			wantState:     "enabled",
			wantIsDefault: "false",
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

			var capturedIsDefault, capturedState, capturedHasUpgrade string
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
				capturedIsDefault = r.URL.Query().Get("only_default")
				capturedState = r.URL.Query().Get("state_filter")
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
				if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
					t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if capturedIsDefault != tt.wantIsDefault {
				t.Errorf("only_default query param: got %q, want %q", capturedIsDefault, tt.wantIsDefault)
			}
			if capturedState != tt.wantState {
				t.Errorf("state_filter query param: got %q, want %q", capturedState, tt.wantState)
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

// Listing rule revisions is what teaches the cache the short IDs it just
// displayed, the same way `get rules` does for rule families.
func TestGetRuleRevisionsSeedsTheShortIDCache(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	const revID = "def8757c-1111-4222-8333-444444444444"
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		total := 1
		page := 1
		testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{
			Results:   []rule_versions.RuleRevisionTiny{testutil.MakeRuleRevisionTiny(revID, "vehicle_aging_rule", "1.0.0", "enabled")},
			TotalRows: &total,
			Page:      &page,
		})
	})

	cmd := setupRuleRevisionsTestCommand()
	cmd.SetArgs([]string{"get", "rule-revisions"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got, ok, err := aliasstore.ExpandShortID(mock.Server.URL, testOrgID, aliasstore.ResourceRuleRevision, "def8757c")
	if err != nil {
		t.Fatalf("ExpandShortID: %v", err)
	}
	if !ok || got != revID {
		t.Errorf("cache after `get rule-revisions` = (%q, %v), want (%q, true)", got, ok, revID)
	}
}

// TestRuleRevisionMatchStandardFieldAndConceptBothWork verifies that both
// --match standard-field and --match concept send match=concept to the API.
func TestRuleRevisionMatchStandardFieldAndConceptBothWork(t *testing.T) {
	tests := []struct {
		name       string
		matchValue string
	}{
		{name: "standard-field", matchValue: "standard-field"},
		{name: "concept", matchValue: "concept"},
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

			requests := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-revision-matches", func(w http.ResponseWriter, r *http.Request) {
				requests++
				// Both should send match=concept to the API
				if got := r.URL.Query().Get("match"); got != "concept" {
					t.Errorf("match query parameter = %q, want %q", got, "concept")
				}
				total, page := 1, 1
				testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionMatchListSchema{
					Results:   []api.RuleRevisionMatchSchema{sampleRuleRevisionMatch()},
					TotalRows: &total, Page: &page, FullCount: 1, PartialCount: 0, GenericCount: 0,
				})
			})

			cmd := setupRuleRevisionsTestCommand()
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"get", "rule-revisions", "--for-table", "42", "--match", tt.matchValue})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if requests != 1 {
				t.Errorf("requests = %d, want 1", requests)
			}
		})
	}
}

// TestRuleRevisionMatchInvalidValueExits2 verifies that an invalid --match value exits 2.
func TestRuleRevisionMatchInvalidValueExits2(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupRuleRevisionsTestCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"get", "rule-revisions", "--for-table", "42", "--match", "invalid-value"})
	err := cmd.Execute()
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", qerrors.GetExitCode(err), qerrors.ExitBadUsage)
	}
	if !strings.Contains(stderr.String(), "invalid-value") || !strings.Contains(stderr.String(), "standard-field") {
		t.Errorf("error message missing expected text: %s", stderr.String())
	}
}
