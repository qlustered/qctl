package get

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func setupTableRulesTestCommand() *cobra.Command {
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
	getCmd.AddCommand(NewTableRulesCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleDatasetRuleTiny() dataset_rules.DatasetRuleTiny {
	ruleID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	revisionID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)
	cols := []string{"email", "domain"}

	return dataset_rules.DatasetRuleTiny{
		ID:             ruleID,
		InstanceName:   "email_check",
		FamilyName:     "Email Check",
		RuleRevisionID: revisionID,
		Release:        "1.0.0",
		Position:       1,
		State:          "enabled",
		Severity:       api.RuleSeverityAsDeclared,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		DatasetColumns: &cols,
	}
}

func sampleDatasetRuleTiny2() dataset_rules.DatasetRuleTiny {
	ruleID := uuid.MustParse("660e8400-e29b-41d4-a716-446655440001")
	revisionID := uuid.MustParse("bbbb0000-cccc-dddd-eeee-ffffffffffff")
	createdAt := time.Date(2025, 5, 10, 8, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 5, 15, 12, 0, 0, 0, time.UTC)

	return dataset_rules.DatasetRuleTiny{
		ID:             ruleID,
		InstanceName:   "phone_normalizer",
		RuleRevisionID: revisionID,
		Release:        "2.1.0",
		Position:       2,
		State:          "disabled",
		Severity:       api.RuleSeverityBlocker,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
}

func TestGetTableRulesCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockRules             []dataset_rules.DatasetRuleTiny
		wantErr               bool
		wantExitCode          int
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{"--table-id", "1"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr: false,
			// Severity: as_declared — the SEVERITY cell prints the enum
			// member verbatim.
			wantOutputContains: []string{"email_check", "Email Check", "RULE", "1.0.0", "550e8400", "Enabled", "as_declared"},
		},
		{
			name: "with json output",
			args: []string{"--table-id", "1", "--output", "json"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{`"kind": "List"`, `"instance_name": "email_check"`, `"release": "1.0.0"`},
		},
		{
			name: "with yaml output",
			args: []string{"--table-id", "1", "--output", "yaml"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"kind: List", "instance_name: email_check", "release: 1.0.0"},
		},
		{
			name: "multiple rules with state and severity",
			args: []string{"--table-id", "1"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
				sampleDatasetRuleTiny2(),
			},
			wantErr: false,
			// email_check: State=enabled, Severity=as_declared → "Enabled", "as_declared"
			// phone_normalizer: State=disabled, Severity=blocker → "Disabled", "blocker"
			wantOutputContains: []string{"email_check", "phone_normalizer", "Enabled", "Disabled", "as_declared", "blocker"},
		},
		{
			name: "with search query",
			args: []string{"--table-id", "1", "--search", "email"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"email_check"},
		},
		{
			name: "with sorting",
			args: []string{"--table-id", "1", "--order-by", "position", "--reverse"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr: false,
		},
		{
			name:      "empty results",
			args:      []string{"--table-id", "1"},
			mockRules: []dataset_rules.DatasetRuleTiny{},
			wantErr:   false,
		},
		{
			name:               "empty results with json output",
			args:               []string{"--table-id", "1", "--output", "json"},
			mockRules:          []dataset_rules.DatasetRuleTiny{},
			wantOutputContains: []string{`"kind": "List"`, `"items": []`, `"total": 0`, `"next_page": null`},
		},
		{
			name:         "invalid sort field",
			args:         []string{"--table-id", "1", "--order-by", "invalid_field"},
			wantErr:      true,
			wantExitCode: qerrors.ExitBadUsage,
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

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockRules)
				page := 1
				response := dataset_rules.DatasetRuleList{
					Results:   tt.mockRules,
					TotalRows: &totalRows,
					Page:      &page,
					Next:      nil,
					Previous:  nil,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			cmd := setupTableRulesTestCommand()
			args := append([]string{"get", "table-rules"}, tt.args...)

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)

			err := cmd.Execute()

			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				if code := qerrors.GetExitCode(err); code != tt.wantExitCode {
					t.Errorf("exit code = %d, want %d (err: %v)", code, tt.wantExitCode, err)
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

func TestGetTableRulesCommand_MissingTableFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTableRulesTestCommand()
	cmd.SetArgs([]string{"get", "table-rules"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when --table flag is missing")
	}
	if !strings.Contains(err.Error(), "table is required") {
		t.Errorf("Expected 'table is required' error, got: %v", err)
	}
	// This command really does take the table positionally, so its guidance
	// must keep offering that — the flags-only sibling's must not.
	if !strings.Contains(err.Error(), "the positional argument") {
		t.Errorf("get table-rules takes a table positionally; the error must say so: %v", err)
	}
}

// newTableRulesTestEnv returns a mock server for a logged-in test org.
func newTableRulesTestEnv(t *testing.T) *testutil.MockAPIServer {
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

// pagedTableRule is sampleDatasetRuleTiny with an id and name derived from n.
func pagedTableRule(n int) interface{} {
	rule := sampleDatasetRuleTiny()
	rule.ID = uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", n))
	rule.InstanceName = fmt.Sprintf("rule_%d", n)
	rule.Position = n
	return rule
}

// decodeTableRulesList decodes a -o json or -o yaml List of table rules.
func decodeTableRulesList(t *testing.T, format string, raw []byte) (output.List, []map[string]interface{}) {
	t.Helper()
	var list output.List
	var err error
	if format == "yaml" {
		err = yaml.Unmarshal(raw, &list)
	} else {
		err = json.Unmarshal(raw, &list)
	}
	if err != nil {
		t.Fatalf("-o %s did not decode as a List: %v\n%s", format, err, raw)
	}
	if list.Kind != "List" {
		t.Fatalf("kind = %q, want List:\n%s", list.Kind, raw)
	}
	var items []map[string]interface{}
	encoded, _ := json.Marshal(list.Items)
	if err := json.Unmarshal(encoded, &items); err != nil {
		t.Fatalf("items did not decode: %v\n%s", err, raw)
	}
	return list, items
}

// TestGetTableRulesCommand_Pagination serves pages the way atlas's OFFSET
// path does (get_paginated_results_order_by, pagination.py): total_rows and
// no next. The total alone has to say a page follows.
func TestGetTableRulesCommand_Pagination(t *testing.T) {
	const hint = "Showing 1-2 of 3 table rules (page 1 of 2). More available: use --page 2 or add filters.\n"
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
			mock := newTableRulesTestEnv(t)
			requests := 0
			handler := testutil.MockOffsetPagedHandler(3, pagedTableRule)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
				requests++
				handler(w, r)
			})

			cmd := setupTableRulesTestCommand()
			cmd.SetArgs([]string{"get", "table-rules", "--table-id", "1", "--limit", "2", "-o", tt.format})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if requests != 1 {
				t.Errorf("requests = %d, want 1 (no auto-pagination)", requests)
			}
			if stderr.String() != tt.wantHint {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantHint)
			}
			if tt.format == "name" {
				want := "table-rule/00000000-0000-0000-0000-000000000001\ntable-rule/00000000-0000-0000-0000-000000000002\n"
				if stdout.String() != want {
					t.Errorf("-o name stdout = %q, want %q", stdout.String(), want)
				}
			}
			if tt.format != "json" && tt.format != "yaml" {
				return
			}

			list, items := decodeTableRulesList(t, tt.format, stdout.Bytes())
			if len(items) != 2 {
				t.Errorf("%d items, want 2:\n%s", len(items), stdout.String())
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

// The last page has no next_page and, under table, no hint.
func TestGetTableRulesCommand_LastPage(t *testing.T) {
	for _, format := range []string{"json", "table"} {
		t.Run(format, func(t *testing.T) {
			mock := newTableRulesTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", testutil.MockOffsetPagedHandler(3, pagedTableRule))

			cmd := setupTableRulesTestCommand()
			cmd.SetArgs([]string{"get", "table-rules", "--table-id", "1", "--limit", "2", "--page", "2", "-o", format})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing on the last page", stderr.String())
			}
			if format != "json" {
				if !strings.Contains(stdout.String(), "rule_3") {
					t.Errorf("page 2 should list rule_3:\n%s", stdout.String())
				}
				return
			}
			var raw map[string]interface{}
			if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
				t.Fatalf("-o json did not decode: %v", err)
			}
			if v, ok := raw["next_page"]; !ok || v != nil {
				t.Errorf("next_page = %v (present %v), want null", v, ok)
			}
			if raw["page"] != float64(2) || raw["total"] != float64(3) {
				t.Errorf("page/total = %v/%v, want 2/3", raw["page"], raw["total"])
			}
		})
	}
}

// -o json and -o yaml items are the API's rows, not the table projection:
// raw state, RFC3339 times, the family fields, and no display-only columns.
func TestGetTableRulesCommand_StructuredItemsAreRaw(t *testing.T) {
	rule := sampleDatasetRuleTiny()
	rule.FamilyID = uuid.MustParse("ffff0000-1111-2222-3333-444444444444")
	rule.FamilyName = "Email check"
	rule.HasMultipleReleases = true
	validation := api.ValidationEvidenceKindPassed
	migrationID := 42
	rule.ValidationState = &validation
	rule.PendingMigrationModelID = &migrationID
	rule.SchemaChangeBlocksEnable = false
	rule.ValidationBlocksEnable = true
	newest := "2.0.0"
	rule.NewerReleaseAvailable = true
	rule.NewestRelease = &newest
	rule.CreatedByUser = &api.UserInfoTinyDictSchema{Email: "author@example.com"}
	description, value := "  Completed bookings  ", "completed"
	rule.RowFilter = &api.RowFilter{
		Description: &description,
		Conditions:  &[]api.RowFilterCondition{{ColumnName: "status", Operator: api.Eq, Value: &value}},
		Predicate:   &api.RowFilterPredicate{Columns: []string{"amount"}, Source: "def applies(row):\n    return row[\"amount\"] > 0"},
	}
	filterJSON, err := json.Marshal(rule.RowFilter)
	if err != nil {
		t.Fatal(err)
	}
	var wantFilter map[string]interface{}
	if err := json.Unmarshal(filterJSON, &wantFilter); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{
		"created_at", "created_by_user", "dataset_columns", "family_id", "family_name",
		"has_multiple_releases", "id", "instance_name", "newer_release_available", "newest_release", "newest_revision_id",
		"pending_migration_model_id", "position", "release", "row_filter", "rule_revision_id", "schema_change_blocks_enable",
		"severity", "state", "updated_at", "upgraded_from_id", "validation_blocks_enable", "validation_state",
	}

	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := newTableRulesTestEnv(t)
			registerTableRulesListHandler(mock, "1", []dataset_rules.DatasetRuleTiny{rule})

			cmd := setupTableRulesTestCommand()
			cmd.SetArgs([]string{"get", "table-rules", "--table-id", "1", "-o", format})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			_, items := decodeTableRulesList(t, format, stdout.Bytes())
			if len(items) != 1 {
				t.Fatalf("%d items, want 1:\n%s", len(items), stdout.String())
			}
			item := items[0]
			var keys []string
			for k := range item {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			if !slices.Equal(keys, wantKeys) {
				t.Errorf("item keys = %v, want %v", keys, wantKeys)
			}
			if !reflect.DeepEqual(item["row_filter"], wantFilter) {
				t.Errorf("row_filter = %#v, want %#v", item["row_filter"], wantFilter)
			}
			for key, want := range map[string]interface{}{
				"state":                       "enabled",
				"created_at":                  "2025-06-15T10:00:00Z",
				"updated_at":                  "2025-06-20T14:00:00Z",
				"family_id":                   "ffff0000-1111-2222-3333-444444444444",
				"id":                          "550e8400-e29b-41d4-a716-446655440000",
				"validation_state":            "passed",
				"pending_migration_model_id":  float64(42),
				"schema_change_blocks_enable": false,
				"validation_blocks_enable":    true,
				"newer_release_available":     true,
				"newest_release":              "2.0.0",
				"newest_revision_id":          nil,
			} {
				if item[key] != want {
					t.Errorf("%s = %v, want %v", key, item[key], want)
				}
			}
		})
	}
}

func TestGetTableRulesCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupTableRulesTestCommand()
	cmd.SetArgs([]string{"get", "table-rules", "--table-id", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Expected error when server returns 500")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitGenericError, err)
	}
}

func TestGetTableRulesCommand_ShortIDColumn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := dataset_rules.DatasetRuleList{
			Results:   []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTableRulesTestCommand()
	cmd.SetArgs([]string{"get", "table-rules", "--table-id", "1", "--output", "table"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	upperOutput := strings.ToUpper(output)

	if !strings.Contains(upperOutput, "SHORT-ID") {
		t.Errorf("Default table output should contain SHORT-ID header, got: %s", output)
	}

	if !strings.Contains(output, "550e8400") {
		t.Errorf("Default table output should contain short ID '550e8400', got: %s", output)
	}

	if strings.Contains(output, "550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("Default table output should NOT contain full UUID, got: %s", output)
	}

	// Should contain STATE and SEVERITY headers
	if !strings.Contains(upperOutput, "STATE") {
		t.Errorf("Default table output should contain STATE header, got: %s", output)
	}
	if !strings.Contains(upperOutput, "SEVERITY") {
		t.Errorf("Default table output should contain SEVERITY header, got: %s", output)
	}
	if strings.Contains(upperOutput, "ISSUES") {
		t.Errorf("Default table output should no longer contain an ISSUES header, got: %s", output)
	}
}

// registerTableRulesListHandler serves the dataset-rules list for a table, in
// the same typed style as TestGetTableRulesCommand.
func registerTableRulesListHandler(mock *testutil.MockAPIServer, tableID string, rules []dataset_rules.DatasetRuleTiny) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/"+tableID+"/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		totalRows := len(rules)
		page := 1
		testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{
			Results:   rules,
			TotalRows: &totalRows,
			Page:      &page,
		})
	})
}

func TestGetTableRulesCommand_PositionalTableID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Integer input fast-paths in datasets.Client.ResolveID: no /datasets call.
	registerTableRulesListHandler(mock, "8", []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()})

	cmd := setupTableRulesTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "table-rules", "8"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "email_check") {
		t.Errorf("expected rule instance name in output, got:\n%s", buf.String())
	}
}

func TestGetTableRulesCommand_PositionalTableName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Name resolution: datasets.Client.ResolveID lists datasets filtered by name.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{{"id": 8, "name": "orders"}},
		})
	})
	registerTableRulesListHandler(mock, "8", []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()})

	cmd := setupTableRulesTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "table-rules", "orders"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "email_check") {
		t.Errorf("expected rule instance name in output, got:\n%s", buf.String())
	}
}

// TestGetTableRulesCommand_EmptyWithPositionalNamesTheTable: `get table-rules
// orders` is scoped to one table, so "found in org" was a claim about the wrong
// thing. The positional form must read like the --table form.
func TestGetTableRulesCommand_EmptyWithPositionalNamesTheTable(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Name resolution: datasets.Client.ResolveID lists datasets filtered by name.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{{"id": 8, "name": "orders"}},
		})
	})
	registerTableRulesListHandler(mock, "8", []dataset_rules.DatasetRuleTiny{})

	cmd := setupTableRulesTestCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"get", "table-rules", "orders"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	want := `No table rules matched "orders" in org "` + testOrgID + `" (context: "default").` + "\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if got := strings.TrimSpace(stdout.String()); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
}

func TestGetTableRulesCommand_TableIDFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Only table 5 is served: a wrong table would 404 rather than pass.
	registerTableRulesListHandler(mock, "5", []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()})

	cmd := setupTableRulesTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "table-rules", "--table-id", "5"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--table-id must select the table by ID: %v", err)
	}
	if !strings.Contains(buf.String(), "email_check") {
		t.Errorf("expected rule instance name in output, got:\n%s", buf.String())
	}
}

func TestGetTableRulesCommand_TableFlagIsAName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var lookedUp string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		lookedUp = r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{{"id": 8, "name": "42"}},
		})
	})
	registerTableRulesListHandler(mock, "8", []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()})

	cmd := setupTableRulesTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// A table named "42" is the case one overloaded flag can never serve.
	cmd.SetArgs([]string{"get", "table-rules", "--table", "42"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
}

func TestGetTableRulesCommand_PositionalAndFlagConflict(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTableRulesTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "table-rules", "8", "--table", "9"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when both positional table and --table are given")
	}
	if !strings.Contains(err.Error(), "table specified more than once") {
		t.Errorf("expected 'table specified more than once' in error, got: %v", err)
	}
}

func TestGetTableRulesCommand_VerboseShowsFullID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := dataset_rules.DatasetRuleList{
			Results:   []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTableRulesTestCommand()
	cmd.SetArgs([]string{"get", "table-rules", "--table-id", "1", "--output", "table", "-v"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("Verbose table output should contain full UUID, got: %s", output)
	}
}
