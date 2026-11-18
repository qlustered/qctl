package diff

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const diffDeleteAmount = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: String
      args: [64]
      is_nullable: true
      is_deleted: true
`

// TestDiffTableSchema_RowFilterColumnWarning: diff previews the row-filter
// warning apply prints, on stdout between the field changes and the request
// body, and keeps diff's exit code 1. A deletion or retype lists rules;
// only an enabled reader of a deleted column costs a detail read.
func TestDiffTableSchema_RowFilterColumnWarning(t *testing.T) {
	const (
		builtinID  = "11111111-1111-4111-8111-111111111111"
		disabledID = "44444444-4444-4444-8444-444444444444"
	)
	listPath := "/api/orgs/" + testOrgID + "/datasets/42/dataset-rules"
	condition := func(column string) *api.RowFilter {
		return &api.RowFilter{Conditions: &[]api.RowFilterCondition{{ColumnName: column, Operator: "is_blank"}}}
	}
	rules := []dataset_rules.DatasetRuleTiny{
		{ID: uuid.MustParse(builtinID), InstanceName: "caf_check", State: api.DatasetRuleStateEnabled, RowFilter: condition("amount")},
		{ID: uuid.MustParse(disabledID), InstanceName: "paused_check", State: api.DatasetRuleStateDisabled, RowFilter: condition("amount")},
	}
	count := func(mock *testutil.MockAPIServer, request string) int {
		n := 0
		for _, r := range mock.Requests() {
			if r == request {
				n++
			}
		}
		return n
	}

	cases := []struct {
		name       string
		manifest   string
		listStatus int
		wantLists  int
		wantDetail int
		wantIn     []string
	}{
		{
			"a deleted column a rule's filter reads warns", diffDeleteAmount, http.StatusOK, 1, 1,
			[]string{
				"Warning: this migration deletes column(s) a row filter reads, and the schema route does not check that:\n" +
					"  caf_check (" + builtinID + "): enabled builtin rule; its row filter reads amount\n" +
					"    The stale-column retirement pass retires (deletes) this enabled builtin rule.\n" +
					"    No patch restores a deleted rule (409).\n" +
					"  paused_check (" + disabledID + "): disabled rule; its row filter reads amount\n",
			},
		},
		{
			"a list failure warns on one line", diffDeleteAmount, http.StatusInternalServerError, 1, 0,
			[]string{"\nWarning: this migration deletes column(s), and qctl could not list the table's rules to check their column mappings or row filters: " +
				"failed to fetch table rules page 1: request failed: server returned 500 Internal Server Error\n\nrequest body:\n"},
		},
		{"a retype warns without detail reads", diffRetypeAmount, http.StatusOK, 1, 0, []string{
			"Warning: this migration changes the type of column(s) a row filter reads",
			"caf_check (" + builtinID + "): enabled rule; its row filter reads amount",
			"paused_check (" + disabledID + "): disabled rule; its row filter reads amount",
			"Enabling a rule does not re-check its saved filter",
		}},
		{"a retype list failure still previews the request", diffRetypeAmount, http.StatusInternalServerError, 1, 0, []string{
			"Warning: this migration changes column types, and qctl could not list the table's rules",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := newDiffTestEnv(t)
			registerSchemaDetail(mock)
			mock.RegisterHandler("GET", listPath, func(w http.ResponseWriter, _ *http.Request) {
				if tc.listStatus != http.StatusOK {
					testutil.RespondError(w, tc.listStatus, "rules are down")
					return
				}
				total := len(rules)
				testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{Results: rules, TotalRows: &total})
			})
			detail := sampleDetail()
			detail.ID = uuid.MustParse(builtinID)
			detail.IsBuiltin = true
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+builtinID,
				func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, detail) })

			stdout, stderr, err := execDiff(t, writeSchemaManifest(t, tc.manifest))
			if code := qerrors.GetExitCode(err); code != 1 {
				t.Fatalf("GetExitCode = %d, want 1 (there is a difference either way); err: %v", code, err)
			}
			// The kubectl-diff contract: everything diff prints is on stdout.
			if stderr != "" {
				t.Errorf("stderr must stay empty; got %q", stderr)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout is missing %q; got:\n%s", want, stdout)
				}
			}
			if n := count(mock, "GET "+listPath); n != tc.wantLists {
				t.Errorf("rule-list GETs = %d, want %d", n, tc.wantLists)
			}
			if n := count(mock, "GET /api/orgs/"+testOrgID+"/dataset-rules/"+builtinID); n != tc.wantDetail {
				t.Errorf("detail GETs of the enabled reader = %d, want %d", n, tc.wantDetail)
			}
			if n := count(mock, "GET /api/orgs/"+testOrgID+"/dataset-rules/"+disabledID); n != 0 {
				t.Errorf("detail GETs of the disabled reader = %d, want 0", n)
			}
			if tc.wantLists == 0 {
				if strings.Contains(stdout, "Warning:") {
					t.Errorf("an edit that deletes no column warned; got:\n%s", stdout)
				}
				return
			}
			warning, changes := strings.Index(stdout, "Warning:"), strings.Index(stdout, "amount")
			if body := strings.Index(stdout, "request body:"); changes < 0 || warning < changes || body < warning {
				t.Errorf("want the field changes, then the warning, then the request body; got:\n%s", stdout)
			}
		})
	}
}
