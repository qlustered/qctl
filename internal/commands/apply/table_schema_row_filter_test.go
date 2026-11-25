package apply

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const (
	builtinFilterRuleID  = "11111111-1111-4111-8111-111111111111"
	customFilterRuleID   = "22222222-2222-4222-8222-222222222222"
	unreadFilterRuleID   = "33333333-3333-4333-8333-333333333333"
	disabledFilterRuleID = "44444444-4444-4444-8444-444444444444"
	deletedFilterRuleID  = "55555555-5555-4555-8555-555555555555"
	otherFilterRuleID    = "66666666-6666-4666-8666-666666666666"
)

// filterRule is a rule on table 42 with row filter filter.
func filterRule(id, name string, state api.DatasetRuleState, filter api.RowFilter) dataset_rules.DatasetRuleTiny {
	return dataset_rules.DatasetRuleTiny{
		ID: uuid.MustParse(id), InstanceName: name, State: state, Severity: api.RuleSeverityAsDeclared, RowFilter: &filter,
	}
}

func TestApplyTableSchema_RowFilterTypeWarningStreamsAndPages(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
			mock.RegisterHandler("GET", tableRulesListPath(42), func(w http.ResponseWriter, r *http.Request) {
				rules := []dataset_rules.DatasetRuleTiny{
					filterRule(builtinFilterRuleID, "enabled_check", api.DatasetRuleStateEnabled, conditionOn("amount")),
					filterRule(deletedFilterRuleID, "gone_check", api.DatasetRuleStateDeleted, conditionOn("amount")),
				}
				if r.URL.Query().Get("page") == "2" {
					rules = []dataset_rules.DatasetRuleTiny{
						filterRule(disabledFilterRuleID, "pending_check", api.DatasetRuleStatePendingValidation, api.RowFilter{
							Predicate: &api.RowFilterPredicate{Source: "def applies(row): return True", Columns: []string{"amount"}},
						}),
						filterRule(otherFilterRuleID, "unaffected", api.DatasetRuleStateEnabled, conditionOn("currency")),
					}
				}
				total := 4
				testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{Results: rules, TotalRows: &total})
			})
			stdout, stderr, err := execApplyStreams(t, writeManifest(t, retypeAmount), "--yes", "-o", format)
			if qerrors.GetExitCode(err) != 0 || *body == "" {
				t.Fatalf("exit=%d, PATCH=%q, err=%v", qerrors.GetExitCode(err), *body, err)
			}
			warning, other := stdout, stderr
			if format != "table" {
				warning, other = stderr, stdout
			}
			for _, want := range []string{"changes the type", "enabled_check", "pending_check", "may invalidate", "is refused (422) while the saved filter"} {
				if !strings.Contains(warning, want) {
					t.Errorf("warning missing %q: %s", want, warning)
				}
			}
			if strings.Contains(warning, "gone_check") || strings.Contains(warning, "unaffected") || strings.Contains(other, "Warning:") {
				t.Errorf("wrong readers or stream: stdout=%s stderr=%s", stdout, stderr)
			}
			if countRequests(mock, "GET "+tableRulesListPath(42)) != 2 {
				t.Errorf("did not consume exactly two rule pages: %v", mock.Requests())
			}
			for _, request := range mock.Requests() {
				if strings.Contains(request, "/dataset-rules/") {
					t.Errorf("a type-only warning read a rule detail: %s", request)
				}
			}
			switch format {
			case "json":
				if !json.Valid([]byte(stdout)) {
					t.Errorf("invalid JSON stdout: %s", stdout)
				}
			case "yaml":
				var result any
				if err := yaml.Unmarshal([]byte(stdout), &result); err != nil {
					t.Errorf("invalid YAML stdout: %v", err)
				}
			}
		})
	}
}

// conditionOn is a row filter with one condition on column.
func conditionOn(column string) api.RowFilter {
	return api.RowFilter{Conditions: &[]api.RowFilterCondition{{ColumnName: column, Operator: "is_blank"}}}
}

// registerFilterRuleDetail answers id's rule detail with is_builtin set to
// builtin.
func registerFilterRuleDetail(mock *testutil.MockAPIServer, id string, builtin bool) {
	detail := sampleDatasetRuleDetailForApply()
	detail.ID = uuid.MustParse(id)
	detail.IsBuiltin = builtin
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+id,
		func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, detail) })
}

// TestApplyTableSchema_RowFilterColumnWarning: deleting a column a table
// rule's row filter reads warns, names each reading rule with its fate, and
// still sends the PATCH with exit 0. Only an enabled reader costs a detail
// read; an edit that neither deletes nor retypes a column lists no rules.
func TestApplyTableSchema_RowFilterColumnWarning(t *testing.T) {
	listGet := "GET " + tableRulesListPath(42)
	detailGet := func(id string) string { return "GET /api/orgs/" + testOrgID + "/dataset-rules/" + id }

	for _, yes := range []bool{true, false} {
		t.Run(fmt.Sprintf("each fate is named, and a deleted rule is skipped (--yes=%v)", yes), func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
			registerTableRulesList(mock, 42,
				filterRule(builtinFilterRuleID, "caf_check", api.DatasetRuleStateEnabled, conditionOn("amount")),
				filterRule(customFilterRuleID, "totals_check\x1b[31m", api.DatasetRuleStateEnabled, api.RowFilter{
					Predicate: &api.RowFilterPredicate{Source: "def applies(row):\n    return True\n", Columns: []string{"amount"}},
				}),
				filterRule(unreadFilterRuleID, "", api.DatasetRuleStateEnabled, conditionOn("amount")),
				filterRule(disabledFilterRuleID, "paused_check", api.DatasetRuleStateDisabled, conditionOn("amount")),
				filterRule(deletedFilterRuleID, "gone_check", api.DatasetRuleStateDeleted, conditionOn("amount")),
				filterRule(otherFilterRuleID, "currency_check", api.DatasetRuleStateEnabled, conditionOn("currency")),
			)
			registerFilterRuleDetail(mock, builtinFilterRuleID, true)
			registerFilterRuleDetail(mock, customFilterRuleID, false)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+unreadFilterRuleID,
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondError(w, http.StatusInternalServerError, "boom")
				})
			confirmYes(t)

			var extra []string
			if yes {
				extra = []string{"--yes"}
			}
			out, err := execApply(t, writeManifest(t, deleteAmount), extra...)
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("--yes=%v: GetExitCode = %d, want 0 (a warning never refuses); err: %v", yes, code, err)
			}
			if *body == "" {
				t.Fatalf("--yes=%v: the PATCH was not sent; got:\n%s", yes, out)
			}
			for _, want := range []string{
				"Warning: this migration deletes column(s) a row filter reads, and the schema route does not check that:\n",
				"  caf_check (" + builtinFilterRuleID + "): enabled builtin rule; its row filter reads amount\n" +
					"    The stale-column retirement pass retires (deletes) this enabled builtin rule.\n",
				"  totals_check\\x1b[31m (" + customFilterRuleID + "): enabled custom rule; its row filter reads amount\n" +
					"    The stale-column retirement pass leaves custom rules alone. If this rule stays enabled\n" +
					"    with this filter, every row it meets gets a validation_problem blocker.\n",
				"  " + unreadFilterRuleID + ": enabled rule; its row filter reads amount\n" +
					"    Whether the rule is builtin could not be read.",
				"  paused_check (" + disabledFilterRuleID + "): disabled rule; its row filter reads amount\n" +
					"    The rule is not enabled, so the stale-column retirement pass leaves it alone.\n",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("--yes=%v: the warning is missing %q; got:\n%s", yes, want, out)
				}
			}
			for _, absent := range []string{"gone_check", "currency_check"} {
				if strings.Contains(out, absent) {
					t.Errorf("--yes=%v: the warning names %s; got:\n%s", yes, absent, out)
				}
			}
			if strings.ContainsRune(out, '\x1b') {
				t.Errorf("--yes=%v: a raw ESC reached the terminal: %q", yes, out)
			}
			if strings.Index(out, "row filter reads") > strings.Index(out, "table-schema/orders updated") {
				t.Errorf("--yes=%v: the warning printed after the PATCH result; got:\n%s", yes, out)
			}
			if prompt := strings.Index(out, "Changes to apply:"); !yes && (prompt < 0 || strings.Index(out, "row filter reads") > prompt) {
				t.Errorf("--yes=false: the warning did not print before the confirmation prompt; got:\n%s", out)
			}
			if n := countRequests(mock, listGet); n != 1 {
				t.Errorf("--yes=%v: rule-list GETs = %d, want 1", yes, n)
			}
			for id, want := range map[string]int{
				builtinFilterRuleID: 1, customFilterRuleID: 1, unreadFilterRuleID: 1,
				disabledFilterRuleID: 0, deletedFilterRuleID: 0, otherFilterRuleID: 0,
			} {
				if n := countRequests(mock, detailGet(id)); n != want {
					t.Errorf("--yes=%v: detail GETs of %s = %d, want %d", yes, id, n, want)
				}
			}
		})
	}

	for _, tc := range []struct {
		name      string
		manifest  string
		wantLists int
	}{
		{"a retype lists rules", retypeAmount, 1},
		{"control: a delete lists them", deleteAmount, 1},
		{"an unrelated property change lists no rules", `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    currency: {type: String, is_nullable: true}
`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
			registerTableRulesList(mock, 42,
				filterRule(builtinFilterRuleID, "caf_check", api.DatasetRuleStateDisabled, conditionOn("amount")))
			out, err := execApply(t, writeManifest(t, tc.manifest), "--yes")
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("GetExitCode = %d, want 0; err: %v", code, err)
			}
			if *body == "" {
				t.Fatalf("the PATCH was not sent; got:\n%s", out)
			}
			if n := countRequests(mock, listGet); n != tc.wantLists {
				t.Errorf("rule-list GETs = %d, want %d; got:\n%s", n, tc.wantLists, out)
			}
			if warned := strings.Contains(out, "row filter reads"); warned != (tc.wantLists > 0) {
				t.Errorf("row-filter warning present = %v, want %v; got:\n%s", warned, tc.wantLists > 0, out)
			}
		})
	}

	t.Run("a list failure warns on one line and the PATCH is still sent", func(t *testing.T) {
		_, mock := setupTableSchemaApplyEnv(t)
		body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
		mock.RegisterHandler("GET", tableRulesListPath(42), func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusInternalServerError, "rules are down")
		})
		out, err := execApply(t, writeManifest(t, deleteAmount), "--yes")
		if code := qerrors.GetExitCode(err); code != 0 {
			t.Fatalf("GetExitCode = %d, want 0 (a failed check never refuses); err: %v", code, err)
		}
		if *body == "" {
			t.Fatalf("the PATCH was not sent; got:\n%s", out)
		}
		const failure = "Warning: this migration deletes column(s), and qctl could not list the table's rules to check their column mappings or row filters: "
		lines := strings.Split(out, "\n")
		found := 0
		for i, line := range lines {
			if !strings.Contains(line, "could not list") {
				continue
			}
			found++
			if !strings.HasPrefix(line, failure) || !strings.HasSuffix(line, "500 Internal Server Error") {
				t.Errorf("the failure line does not name the failure: %q", line)
			}
			if i+1 >= len(lines) || lines[i+1] != "" {
				t.Errorf("the failure line is not followed by a blank line; got:\n%s", out)
			}
		}
		if found != 1 {
			t.Errorf("list-failure lines = %d, want 1; got:\n%s", found, out)
		}
		if strings.Contains(out, "row filter reads, and") {
			t.Errorf("a failed list printed the rule block; got:\n%s", out)
		}
	})
}

func TestApplyTableSchema_DeletionAndRetypeShareRuleReads(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
	registerTableRulesList(mock, 42,
		filterRule(builtinFilterRuleID, "both_columns", api.DatasetRuleStateEnabled, api.RowFilter{
			Conditions: &[]api.RowFilterCondition{
				{ColumnName: "amount", Operator: "is_blank"},
				{ColumnName: "currency", Operator: "is_blank"},
			},
		}))
	registerFilterRuleDetail(mock, builtinFilterRuleID, true)
	out, err := execApply(t, writeManifest(t, `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount: {type: String, is_deleted: true}
    currency: {type: Text}
`), "--yes")
	if qerrors.GetExitCode(err) != 0 {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deletes column(s) a row filter reads") || !strings.Contains(out, "changes the type of column(s) a row filter reads") {
		t.Fatalf("missing a warning: %s", out)
	}
	for _, request := range []string{
		"GET " + tableRulesListPath(42),
		"GET /api/orgs/" + testOrgID + "/dataset-rules/" + builtinFilterRuleID,
	} {
		if n := countRequests(mock, request); n != 1 {
			t.Errorf("%s: %d requests, want 1", request, n)
		}
	}
}
