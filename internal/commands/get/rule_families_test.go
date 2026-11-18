package get

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/rule_families"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// setupRuleFamiliesTestCommand creates a root command with global flags and adds the rules (families) command
func setupRuleFamiliesTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "qctl",
		SilenceUsage:  true,
		SilenceErrors: true,
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
	familyID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	revID := uuid.MustParse("660e8400-e29b-41d4-a716-446655440001")
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
	familyID := uuid.MustParse("770e8400-e29b-41d4-a716-446655440002")
	primaryID := uuid.MustParse("880e8400-e29b-41d4-a716-446655440003")
	secondaryID := uuid.MustParse("990e8400-e29b-41d4-a716-446655440004")
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
		wantExitCode          int
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
			name: "multi-byte author name is cut by character",
			args: []string{},
			mockFamilies: func() []rule_families.RuleFamilyItem {
				item := sampleRuleFamilyItem()
				item.PrimaryRevision.CreatedByUser = &api.UserInfoTinyDictSchema{FirstName: "Ana-Sølvør", LastName: "Ødegård"}
				return []rule_families.RuleFamilyItem{item}
			}(),
			wantOutputContains:    []string{"Ana-Sølvør Ø."},
			wantOutputNotContains: []string{"\uFFFD", "Ã"},
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
			wantOutputContains:    []string{`"kind": "List"`, `"slug": "email_validator"`, `"primary_revision": {`, `"release": "1.0.0"`},
			wantOutputNotContains: []string{`"short_id"`, `"author"`, `"tags"`},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockFamilies: []rule_families.RuleFamilyItem{
				sampleRuleFamilyItem(),
			},
			wantOutputContains:    []string{"kind: List", "slug: email_validator", "primary_revision:", "release: 1.0.0"},
			wantOutputNotContains: []string{"short_id:", "author:", "tags:"},
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
			name:         "invalid sort field",
			args:         []string{"--order-by", "invalid_field"},
			wantErr:      true,
			wantExitCode: qerrors.ExitBadUsage,
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
			if tt.wantErr && tt.wantExitCode != 0 {
				if got := qerrors.GetExitCode(err); got != tt.wantExitCode {
					t.Errorf("exit code = %d, want %d for %v", got, tt.wantExitCode, err)
				}
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

// pagedRuleFamily is family id of a catalog served by
// testutil.MockOffsetPagedHandler: sampleRuleFamilyItemWithSecondary under the
// slug rule_<id>, so every family is two table rows.
func pagedRuleFamily(id int) interface{} {
	item := sampleRuleFamilyItemWithSecondary()
	item.Slug = fmt.Sprintf("rule_%d", id)
	item.Name = item.Slug
	return item
}

// executeGetRules runs `qctl get rules` with args against mock's handlers and
// returns stdout and stderr separately.
func executeGetRules(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs(append([]string{"get", "rules"}, args...))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v\nstderr: %s", args, err, errOut.String())
	}
	return out.String(), errOut.String()
}

// TestGetRuleFamiliesCommand_Pagination serves pages the way atlas does
// (get_rule_families_calculated, rule_view_controller.py): total_rows and no
// next. total_rows counts families and every family here is two table rows,
// so the hint's range comes from --page and --limit, not from the rows shown.
func TestGetRuleFamiliesCommand_Pagination(t *testing.T) {
	const hint = "Showing 1-2 of 3 rules (page 1 of 2). More available: use --page 2 or add filters.\n"
	tests := []struct {
		format   string
		wantHint string
	}{
		{format: "json"},
		{format: "yaml"},
		{format: "table", wantHint: hint},
		{format: "name", wantHint: hint},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			mock := ruleFamiliesTestEnv(t)
			var requests []url.Values
			paged := testutil.MockOffsetPagedHandler(3, pagedRuleFamily)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Query())
				paged(w, r)
			})

			stdout, stderr := executeGetRules(t, "--limit", "2", "-o", tt.format)

			if len(requests) != 1 {
				t.Errorf("requests = %d, want 1 (no auto-pagination)", len(requests))
			}
			if stderr != tt.wantHint {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantHint)
			}

			switch tt.format {
			case "table":
				if got := strings.Count(stdout, "rule_"); got != 4 {
					t.Errorf("table printed %d revision rows, want 4 (two per family):\n%s", got, stdout)
				}
				return
			case "name":
				want := strings.Join([]string{
					"rule/" + secondaryFamilyPrimaryID, "rule/" + secondaryFamilySecondaryID,
					"rule/" + secondaryFamilyPrimaryID, "rule/" + secondaryFamilySecondaryID,
				}, "\n")
				if got := strings.TrimSpace(stdout); got != want {
					t.Errorf("-o name =\n%s\nwant one revision UUID per table row:\n%s", got, want)
				}
				return
			}

			var list output.List
			var err error
			if tt.format == "json" {
				err = json.Unmarshal([]byte(stdout), &list)
			} else {
				err = yaml.Unmarshal([]byte(stdout), &list)
			}
			if err != nil {
				t.Fatalf("-o %s did not decode as a List: %v\n%s", tt.format, err, stdout)
			}
			var items []map[string]interface{}
			raw, _ := json.Marshal(list.Items)
			_ = json.Unmarshal(raw, &items)
			if list.Kind != "List" || len(items) != 2 {
				t.Fatalf("kind = %q, %d items; want List, 2 (one per family)", list.Kind, len(items))
			}
			if items[0]["slug"] != "rule_1" || items[1]["slug"] != "rule_2" {
				t.Errorf("item slugs = %v, %v; want rule_1, rule_2", items[0]["slug"], items[1]["slug"])
			}
			for name, got := range map[string]*int{"page": list.Page, "limit": list.Limit, "total": list.Total, "next_page": list.NextPage} {
				want := map[string]int{"page": 1, "limit": 2, "total": 3, "next_page": 2}[name]
				if got == nil || *got != want {
					t.Errorf("%s = %v, want %d", name, deref(got), want)
				}
			}
		})
	}
}

// The last page has a null next_page under -o json and no hint under -o table.
func TestGetRuleFamiliesLastPage(t *testing.T) {
	for _, format := range []string{"json", "table"} {
		t.Run(format, func(t *testing.T) {
			mock := ruleFamiliesTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", testutil.MockOffsetPagedHandler(3, pagedRuleFamily))

			stdout, stderr := executeGetRules(t, "--limit", "2", "--page", "2", "-o", format)

			if stderr != "" {
				t.Errorf("stderr = %q, want nothing on the last page", stderr)
			}
			if format != "json" {
				return
			}
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
				t.Fatalf("-o json did not decode: %v\n%s", err, stdout)
			}
			if v, ok := raw["next_page"]; !ok || v != nil {
				t.Errorf("next_page = %v (present %v), want null", v, ok)
			}
			if raw["page"] != float64(2) || raw["total"] != float64(3) {
				t.Errorf("page/total = %v/%v, want 2/3", raw["page"], raw["total"])
			}
			if items, _ := raw["items"].([]interface{}); len(items) != 1 {
				t.Errorf("items = %v, want the one family on page 2", raw["items"])
			}
		})
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
	cmd.SetArgs([]string{"get", "rules", "--output", "table", "--columns", "slug,release,state"})

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
		t.Fatal("Expected error when server returns 500")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d for a 500: %v", got, qerrors.ExitGenericError, err)
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

	var searches []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		searches = append(searches, r.URL.Query().Get("search_query"))

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

	// The empty result triggers the did-you-mean hint's second, search-free
	// fetch; this test is about what the listing request itself sent.
	if len(searches) == 0 || searches[0] != "email" {
		t.Errorf("search_query query param: got %v, want first request %q", searches, "email")
	}
}

// ruleFamilyNamed reuses the standard fixture with a different identity, so a
// catalog can be built without repeating the whole revision block.
func ruleFamilyNamed(slug string) rule_families.RuleFamilyItem {
	item := sampleRuleFamilyItem()
	item.Name = slug
	item.Slug = slug
	return item
}

// ruleCatalogMock answers like the backend does: search is a substring match
// over name and slug (get_rule_families_calculated, rule_view_controller.py),
// so a query that matches nothing returns nothing and the same call without
// search returns the catalog. It records every request so a test can assert how
// many were made and with which params.
func ruleCatalogMock(mock *testutil.MockAPIServer, catalog []rule_families.RuleFamilyItem, requests *[]url.Values) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		*requests = append(*requests, q)

		results := []rule_families.RuleFamilyItem{}
		if q.Get("search_query") == "" {
			results = catalog
		}
		totalRows := len(results)
		page := 1
		testutil.RespondJSON(w, http.StatusOK, rule_families.RuleFamilyList{
			Results:   results,
			TotalRows: &totalRows,
			Page:      &page,
		})
	})
}

func ruleFamiliesTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)

	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// TestGetRulesEmptyWithSearchEchoesFilterAndSuggests is the ticket's own case:
// the user typed the two words of the slug in the wrong order.
func TestGetRulesEmptyWithSearchEchoesFilterAndSuggests(t *testing.T) {
	mock := ruleFamiliesTestEnv(t)
	var requests []url.Values
	ruleCatalogMock(mock, []rule_families.RuleFamilyItem{
		ruleFamilyNamed("required_field"),
		ruleFamilyNamed("email_validator"),
		ruleFamilyNamed("value_map"),
	}, &requests)

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--search", "field_required"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := `No rules matched --search "field_required" in org "` + testOrgID + `" (context: "default").` + "\n" +
		"3 rules match without --search.\n" +
		"\nDid you mean: required_field\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if got := strings.TrimSpace(stdout.String()); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}

	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2 (the listing, then the unfiltered hint fetch)", len(requests))
	}
	if got := requests[0].Get("search_query"); got != "field_required" {
		t.Errorf("first request search_query = %q, want %q", got, "field_required")
	}
	if _, ok := requests[1]["search_query"]; ok {
		t.Errorf("hint request must not send search_query, got %q", requests[1].Get("search_query"))
	}
}

// TestGetRulesEmptyHintKeepsOtherFilters: the hint drops --search and nothing
// else, so it can never suggest a rule the user excluded.
func TestGetRulesEmptyHintKeepsOtherFilters(t *testing.T) {
	mock := ruleFamiliesTestEnv(t)
	var requests []url.Values
	ruleCatalogMock(mock, []rule_families.RuleFamilyItem{
		ruleFamilyNamed("custom_rule"),
	}, &requests)

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--exclude-builtin", "--search", "zzz"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := `No rules matched --exclude-builtin, --search "zzz" in org "` + testOrgID + `" (context: "default").` + "\n" +
		"1 rule matches without --search.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}

	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(requests))
	}
	for i, req := range requests {
		if got := req.Get("include_builtin"); got != "false" {
			t.Errorf("request %d include_builtin = %q, want %q", i, got, "false")
		}
	}
}

// TestGetRulesEmptyWithSearchHintFetchFails: the hint is a courtesy. Losing it
// must not turn an empty list into a failure.
func TestGetRulesEmptyWithSearchHintFetchFails(t *testing.T) {
	mock := ruleFamiliesTestEnv(t)

	var calls int
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 1 {
			testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		totalRows := 0
		page := 1
		testutil.RespondJSON(w, http.StatusOK, rule_families.RuleFamilyList{
			Results:   []rule_families.RuleFamilyItem{},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--search", "field_required"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, want nil (an empty list is not an error)", err)
	}

	want := `No rules matched --search "field_required" in org "` + testOrgID + `" (context: "default").` + "\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if calls != 2 {
		t.Errorf("got %d requests, want 2", calls)
	}
}

// TestGetRulesEmptyNoFiltersUnchanged: no filter, no second request, and the
// original sentence byte for byte.
func TestGetRulesEmptyNoFiltersUnchanged(t *testing.T) {
	mock := ruleFamiliesTestEnv(t)
	var requests []url.Values
	ruleCatalogMock(mock, []rule_families.RuleFamilyItem{}, &requests)

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := `No rules found in org "` + testOrgID + `" (context: "default").` + "\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if len(requests) != 1 {
		t.Errorf("got %d requests, want 1 (no --search, nothing to hint about)", len(requests))
	}
}

func TestGetRulesEmptyJSONKeepsHintOnStderr(t *testing.T) {
	mock := ruleFamiliesTestEnv(t)
	var requests []url.Values
	ruleCatalogMock(mock, []rule_families.RuleFamilyItem{
		ruleFamilyNamed("required_field"),
	}, &requests)

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--search", "field_required", "--output", "json"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("-o json did not decode: %v\n%s", err, stdout.String())
	}
	if items, ok := raw["items"].([]interface{}); raw["kind"] != "List" || !ok || len(items) != 0 {
		t.Errorf("stdout = %s, want a List with items []", stdout.String())
	}
	if v, ok := raw["next_page"]; raw["total"] != float64(0) || !ok || v != nil {
		t.Errorf("total/next_page = %v/%v, want 0/null", raw["total"], v)
	}
	if strings.Contains(stderr.String(), "More available") {
		t.Errorf("stderr = %q, want no paging hint under -o json", stderr.String())
	}
	if !strings.Contains(stderr.String(), "required_field") {
		t.Errorf("stderr = %q, want suggestion", stderr.String())
	}
	if len(requests) != 2 {
		t.Errorf("got %d requests, want 2", len(requests))
	}
}

// TestGetRulesPastLastPageSuppressesHint: the search matched rows, the page did
// not exist. Re-fetching without --search would answer a question nobody asked,
// so the hint must not fire and there must be exactly one request.
func TestGetRulesPastLastPageSuppressesHint(t *testing.T) {
	mock := ruleFamiliesTestEnv(t)

	var requests []url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Query())
		totalRows := 3
		page := 99
		testutil.RespondJSON(w, http.StatusOK, rule_families.RuleFamilyList{
			Results:   []rule_families.RuleFamilyItem{},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--search", "field_required", "--page", "99", "--limit", "50"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := `No rules matched --search "field_required" on page 99 in org "` + testOrgID + `" (context: "default").` + "\n" +
		`--search "field_required" matches 3 rules — the last page is 1.` + "\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if strings.Contains(stderr.String(), "Did you mean") {
		t.Errorf("stderr must not carry a did-you-mean when the page is what emptied the list: %q", stderr.String())
	}
	if len(requests) != 1 {
		t.Errorf("got %d requests, want 1 (the page branch must not issue the hint fetch)", len(requests))
	}
}

// TestGetRulesEmptyInvalidColumnsSuppressesHint: --columns is validated before
// the hint's second fetch is ever considered. If validateColumnsFlag ran after
// the hint (or not at all on this path), this would either print a Did-you-mean
// line the caller can never use (the command is about to fail) or issue a
// second HTTP request the user never sees the result of. Asserting the request
// count is the point: asserting only the absent text would still pass if a
// second fetch fired and was silently discarded.
func TestGetRulesEmptyInvalidColumnsSuppressesHint(t *testing.T) {
	mock := ruleFamiliesTestEnv(t)
	var requests []url.Values
	ruleCatalogMock(mock, []rule_families.RuleFamilyItem{
		ruleFamilyNamed("required_field"),
	}, &requests)

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--search", "field_required", "--columns", "bogus_column"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unknown column "bogus_column"`) {
		t.Fatalf("expected an unknown-column error, got %v", err)
	}
	if strings.Contains(stderr.String(), "Did you mean") {
		t.Errorf("must not print a did-you-mean hint behind an invalid --columns, got: %q", stderr.String())
	}
	if len(requests) != 1 {
		t.Errorf("got %d requests, want 1 (the listing only; --columns must fail before the hint fetch)", len(requests))
	}
}

// TestGetRulesEmptySuggestsMultipleFamiliesJoinedByComma: rule_families.go
// joins 2+ suggestions with ", " and no other test yields more than one
// suggestion, so that join is unexercised without this. Both "value_map" and
// "value_mp" contain "val" as a substring (fuzzy's rankSubstring, one rank
// below rotation), so both match; "required_field" contains no "val" and
// rules out "everything matches" as the reason this test passes. Tied rank
// breaks alphabetically, so the expected order is fixed too.
func TestGetRulesEmptySuggestsMultipleFamiliesJoinedByComma(t *testing.T) {
	mock := ruleFamiliesTestEnv(t)
	var requests []url.Values
	ruleCatalogMock(mock, []rule_families.RuleFamilyItem{
		ruleFamilyNamed("value_map"),
		ruleFamilyNamed("value_mp"),
		ruleFamilyNamed("required_field"),
	}, &requests)

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--search", "val"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := `No rules matched --search "val" in org "` + testOrgID + `" (context: "default").` + "\n" +
		"3 rules match without --search.\n" +
		"\nDid you mean: value_map, value_mp\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

// TestSuggestRuleSlugsMatchesOnNameSuggestsSlug: suggestRuleSlugs is why
// fuzzy.Candidate splits Match from Value at all -- a real RuleFamily has a
// human-readable Name distinct from its Slug, and the suggestion must always
// be the Slug, because that is the only thing --search or `get rule` accepts.
// ruleFamilyNamed sets Name == Slug, so it cannot exercise this; every other
// test in this file goes through that fixture and would pass even if the
// function suggested Name instead of Slug. This test searches for the Name
// and asserts the Slug comes back, not the Name.
func TestSuggestRuleSlugsMatchesOnNameSuggestsSlug(t *testing.T) {
	family := sampleRuleFamilyItem()
	family.Name = "Email Validator"
	family.Slug = "email_validator_v2"

	got := suggestRuleSlugs("email validator", []rule_families.RuleFamilyItem{family})

	if len(got) != 1 || got[0] != "email_validator_v2" {
		t.Errorf("suggestRuleSlugs() = %v, want [%q] (the slug, not the matched name %q)",
			got, "email_validator_v2", family.Name)
	}
}

// TestGetRulesShortSearchRejected: the backend drops a search of two characters
// or fewer and answers with the whole catalog (atlas get_search_filters,
// pagination.py), so forwarding one printed every rule under a heading that
// claimed a filter. The handler is a trap -- the point of the check is that no
// request happens.
func TestGetRulesShortSearchRejected(t *testing.T) {
	mock := ruleFamiliesTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families",
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("a search the server would ignore must fail before the listing request")
		})

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs([]string{"get", "rules", "--search", "ab"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want the short-search error")
	}
	want := `--search "ab" is too short (minimum 3 characters); the server ignores shorter queries ` +
		`and would return unfiltered results. Widen the search or drop --search.`
	if err.Error() != want {
		t.Errorf("error = %q,\nwant %q", err.Error(), want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty -- a refused filter prints no rows", stdout.String())
	}
}

// sampleRuleFamilyItem's PrimaryRevision UUID and its first 8 hex digits.
const (
	familyRevID      = "660e8400-e29b-41d4-a716-446655440001"
	familyRevShortID = "660e8400"
)

// sampleRuleFamilyItemWithSecondary's Primary/SecondaryRevision UUIDs and
// their first 8 hex digits.
const (
	secondaryFamilyPrimaryID      = "880e8400-e29b-41d4-a716-446655440003"
	secondaryFamilyPrimaryShortID = "880e8400"
	secondaryFamilySecondaryID    = "990e8400-e29b-41d4-a716-446655440004"
	secondaryFamilySecondaryShort = "990e8400"
)

// runGetRules serves one rule family and runs `qctl get rules` with args,
// returning stdout. Defined once so every assertion below differs only in the
// flags it passes.
func runGetRules(t *testing.T, args ...string) (string, *testutil.MockAPIServer) {
	t.Helper()
	return runGetRulesServing(t, sampleRuleFamilyItem(), args...)
}

// runGetRulesServing is runGetRules generalized to an arbitrary fixture, so a
// test that cares about a secondary revision (which sampleRuleFamilyItem does
// not have) can still share the same setup and command-execution shape.
func runGetRulesServing(t *testing.T, family rule_families.RuleFamilyItem, args ...string) (string, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)

	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
		total := 1
		page := 1
		testutil.RespondJSON(w, http.StatusOK, rule_families.RuleFamilyList{
			Results:   []rule_families.RuleFamilyItem{family},
			TotalRows: &total,
			Page:      &page,
		})
	})

	cmd := setupRuleFamiliesTestCommand()
	cmd.SetArgs(append([]string{"get", "rules"}, args...))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v\noutput: %s", args, err, buf.String())
	}
	return buf.String(), mock
}

// -o json items are the families as the API sent them: the full revision UUID
// is primary_revision.id, and no display field rides along.
func TestGetRuleFamiliesJSONIncludesFullID(t *testing.T) {
	out, _ := runGetRules(t, "-o", "json")

	var items []map[string]interface{}
	decodeListItems(t, []byte(out), &items)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	rev, _ := items[0]["primary_revision"].(map[string]interface{})
	if rev["id"] != familyRevID {
		t.Errorf("primary_revision.id = %v, want %q", rev["id"], familyRevID)
	}
	if items[0]["family_id"] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("family_id = %v, want the family's UUID", items[0]["family_id"])
	}
	for _, key := range []string{"id", "short_id", "release", "state", "tags", "author", "created_at", "updated_at"} {
		if v, ok := items[0][key]; ok {
			t.Errorf("item has display field %s = %v; -o json prints the API's family", key, v)
		}
	}
}

// A family's newer-than-default revision rides in the same item, with the
// timestamps and user the API sent.
func TestGetRuleFamiliesJSONNestsTheSecondaryRevision(t *testing.T) {
	out, _ := runGetRulesServing(t, sampleRuleFamilyItemWithSecondary(), "-o", "json")

	var items []map[string]interface{}
	decodeListItems(t, []byte(out), &items)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (both revisions belong to one family)", len(items))
	}
	primary, _ := items[0]["primary_revision"].(map[string]interface{})
	secondary, _ := items[0]["secondary_revision"].(map[string]interface{})
	if primary["id"] != secondaryFamilyPrimaryID || secondary["id"] != secondaryFamilySecondaryID {
		t.Errorf("revision ids = %v, %v; want %s, %s", primary["id"], secondary["id"], secondaryFamilyPrimaryID, secondaryFamilySecondaryID)
	}
	if secondary["created_at"] != "2025-06-01T08:00:00Z" {
		t.Errorf("secondary_revision.created_at = %v, want the API's timestamp", secondary["created_at"])
	}
	user, _ := secondary["created_by_user"].(map[string]interface{})
	if user["first_name"] != "Alex" || user["last_name"] != "Kim" {
		t.Errorf("secondary_revision.created_by_user = %v, want the API's user", secondary["created_by_user"])
	}
	if items[0]["has_newer_than_default"] != true {
		t.Errorf("has_newer_than_default = %v, want true", items[0]["has_newer_than_default"])
	}
}

func TestGetRuleFamiliesAcceptsIDColumn(t *testing.T) {
	out, _ := runGetRules(t, "--columns", "slug,id")
	if !strings.Contains(out, familyRevID) {
		t.Errorf("--columns slug,id did not print the full UUID; got:\n%s", out)
	}
}

// -o name lists rule/<revision UUID>, the same as `get rule-revisions -o name`.
func TestGetRuleFamiliesNameOutputListsFullIDs(t *testing.T) {
	out, _ := runGetRules(t, "-o", "name")
	if got := strings.TrimSpace(out); got != "rule/"+familyRevID {
		t.Errorf("-o name = %q, want rule/<full revision UUID>", got)
	}
}

// The default table must not grow a column.
func TestGetRuleFamiliesTableStillOmitsID(t *testing.T) {
	out, _ := runGetRules(t)
	if strings.Contains(out, familyRevID) {
		t.Error("the default table printed a full UUID; only SHORT-ID belongs there")
	}
	if !strings.Contains(out, familyRevShortID) {
		t.Errorf("the default table lost its SHORT-ID column; got:\n%s", out)
	}
}

// Listing rules is what teaches the cache the short IDs it just displayed.
func TestGetRuleFamiliesSeedsTheShortIDCache(t *testing.T) {
	_, mock := runGetRules(t)

	got, ok, err := aliasstore.ExpandShortID(mock.Server.URL, testOrgID, aliasstore.ResourceRuleRevision, familyRevShortID)
	if err != nil {
		t.Fatalf("ExpandShortID: %v", err)
	}
	if !ok || got != familyRevID {
		t.Errorf("cache after `get rules` = (%q, %v), want (%q, true)", got, ok, familyRevID)
	}
}

// TestGetRuleFamiliesSeedsSecondaryRevisionShortID: rememberRuleFamilyShortIDs
// must seed BOTH revisions of a family, not just the primary. sampleRuleFamilyItem
// (used by every other test above) has no SecondaryRevision, so none of them can
// exercise that branch -- this test serves sampleRuleFamilyItemWithSecondary
// specifically to catch a regression that silently drops it.
func TestGetRuleFamiliesSeedsSecondaryRevisionShortID(t *testing.T) {
	_, mock := runGetRulesServing(t, sampleRuleFamilyItemWithSecondary())

	gotPrimary, ok, err := aliasstore.ExpandShortID(mock.Server.URL, testOrgID, aliasstore.ResourceRuleRevision, secondaryFamilyPrimaryShortID)
	if err != nil {
		t.Fatalf("ExpandShortID(primary): %v", err)
	}
	if !ok || gotPrimary != secondaryFamilyPrimaryID {
		t.Errorf("cache primary revision = (%q, %v), want (%q, true)", gotPrimary, ok, secondaryFamilyPrimaryID)
	}

	gotSecondary, ok, err := aliasstore.ExpandShortID(mock.Server.URL, testOrgID, aliasstore.ResourceRuleRevision, secondaryFamilySecondaryShort)
	if err != nil {
		t.Fatalf("ExpandShortID(secondary): %v", err)
	}
	if !ok || gotSecondary != secondaryFamilySecondaryID {
		t.Errorf("cache secondary revision = (%q, %v), want (%q, true)", gotSecondary, ok, secondaryFamilySecondaryID)
	}
}
