package apply

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestApplyTableSchema_MappedColumnWarning(t *testing.T) {
	for _, tc := range []struct {
		name           string
		builtin        bool
		caf            bool
		mapping        map[string]string
		filter         bool
		detailStatus   int
		revisionStatus int
		want           string
		wantRevisions  int
	}{
		{"builtin", true, false, map[string]string{"value": "amount"}, false, 200, 200, "retires (deletes) the rule", 0},
		{"custom", false, false, map[string]string{"value": "amount"}, false, 200, 200, "custom rule keeps its state and column mapping", 0},
		{"CAF optional", true, true, map[string]string{"context": "amount"}, false, 200, 200, "unbinds these optional CAF mappings", 1},
		{"CAF required", true, true, map[string]string{"value": "amount"}, false, 200, 200, "retires (deletes) the rule", 1},
		{"CAF mixed slots", true, true, map[string]string{"context": "amount", "value": "amount"}, false, 200, 200, "retires (deletes) the rule", 1},
		{"CAF filter overrides optional", true, true, map[string]string{"context": "amount"}, true, 200, 200, "row filter also reads a deleted column", 0},
		{"unreadable rule", false, false, nil, true, 403, 200, "rule detail could not be read", 0},
		{"unreadable revision", true, true, map[string]string{"context": "amount"}, false, 200, 403, "optional columns could not be read", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
			r := dataset_rules.DatasetRuleTiny{ID: uuid.MustParse(builtinFilterRuleID), InstanceName: "mapped_check",
				State: api.DatasetRuleStateEnabled, DatasetColumns: &[]string{"amount"}}
			if tc.filter {
				filter := conditionOn("amount")
				r.RowFilter = &filter
			}
			registerTableRulesList(mock, 42, r)
			detail := sampleDatasetRuleDetailForApply()
			detail.ID, detail.IsBuiltin, detail.IsCaf = r.ID, tc.builtin, tc.caf
			detail.ColumnMappingDict = tc.mapping
			detailPath := "/api/orgs/" + testOrgID + "/dataset-rules/" + builtinFilterRuleID
			mock.RegisterHandler("GET", detailPath, func(w http.ResponseWriter, _ *http.Request) {
				if tc.detailStatus != http.StatusOK {
					testutil.RespondError(w, tc.detailStatus, "cannot read rule")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, detail)
			})
			revisionPath := "/api/orgs/" + testOrgID + "/rule-revisions/" + detail.RuleRevision.ID.String() + "/details"
			mock.RegisterHandler("GET", revisionPath, func(w http.ResponseWriter, _ *http.Request) {
				if tc.revisionStatus != http.StatusOK {
					testutil.RespondError(w, tc.revisionStatus, "cannot read revision")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionFullSchema{OptionalColumns: &[]string{"context"}})
			})
			confirmYes(t)
			out, err := execApply(t, writeManifest(t, deleteAmount))
			if code := qerrors.GetExitCode(err); code != 0 || *body == "" {
				t.Fatalf("warning must allow the PATCH: exit %d, body %q, err %v", code, *body, err)
			}
			for _, want := range []string{"Warning: this migration deletes column(s) a table rule maps:", "mapped_check", "maps amount", tc.want} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
			if strings.Index(out, "a table rule maps") > strings.Index(out, "Changes to apply:") {
				t.Errorf("warning must precede confirmation:\n%s", out)
			}
			for request, want := range map[string]int{"GET " + tableRulesListPath(42): 1, "GET " + detailPath: 1, "GET " + revisionPath: tc.wantRevisions} {
				if got := countRequests(mock, request); got != want {
					t.Errorf("%s requests = %d, want %d", request, got, want)
				}
			}
		})
	}
}

func TestApplyTableSchema_MappedWarningPagesAndCachesRevisionReads(t *testing.T) {
	for _, revisionStatus := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(strconv.Itoa(revisionStatus), func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
			rules := []dataset_rules.DatasetRuleTiny{
				{ID: uuid.MustParse(otherFilterRuleID), InstanceName: "unrelated", State: api.DatasetRuleStateEnabled, DatasetColumns: &[]string{"currency"}},
				{ID: uuid.MustParse(builtinFilterRuleID), InstanceName: "first_CAF", State: api.DatasetRuleStateEnabled, DatasetColumns: &[]string{"amount"}},
				{ID: uuid.MustParse(customFilterRuleID), InstanceName: "second_CAF", State: api.DatasetRuleStateEnabled, DatasetColumns: &[]string{"amount"}},
				{ID: uuid.MustParse(disabledFilterRuleID), InstanceName: "paused", State: api.DatasetRuleStateDisabled, DatasetColumns: &[]string{"amount"}},
				{ID: uuid.MustParse(deletedFilterRuleID), InstanceName: "gone", State: api.DatasetRuleStateDeleted, DatasetColumns: &[]string{"amount"}},
			}
			mock.RegisterHandler("GET", tableRulesListPath(42), func(w http.ResponseWriter, r *http.Request) {
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page < 1 || page > len(rules) {
					t.Fatalf("unexpected rule page %d", page)
				}
				total := len(rules)
				testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{Results: rules[page-1 : page], TotalRows: &total})
			})
			for _, r := range rules[1:3] {
				detail := sampleDatasetRuleDetailForApply()
				detail.ID, detail.IsBuiltin, detail.IsCaf = r.ID, true, true
				detail.ColumnMappingDict = map[string]string{"context": "amount"}
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+r.ID.String(), func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, detail)
				})
			}
			revisionPath := "/api/orgs/" + testOrgID + "/rule-revisions/" + sampleDatasetRuleDetailForApply().RuleRevision.ID.String() + "/details"
			mock.RegisterHandler("GET", revisionPath, func(w http.ResponseWriter, _ *http.Request) {
				if revisionStatus != http.StatusOK {
					testutil.RespondError(w, revisionStatus, "denied")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionFullSchema{OptionalColumns: &[]string{"context"}})
			})
			out, err := execApply(t, writeManifest(t, deleteAmount), "--yes")
			if qerrors.GetExitCode(err) != 0 {
				t.Fatalf("apply: %v", err)
			}
			for _, name := range []string{"first_CAF", "second_CAF", "paused"} {
				if !strings.Contains(out, name) {
					t.Errorf("missing %s:\n%s", name, out)
				}
			}
			for _, name := range []string{"unrelated", "gone"} {
				if strings.Contains(out, name) {
					t.Errorf("unexpected %s:\n%s", name, out)
				}
			}
			if n := countRequests(mock, "GET "+revisionPath); n != 1 {
				t.Errorf("revision GETs = %d, want 1", n)
			}
			for _, r := range rules {
				want := 0
				if r.InstanceName == "first_CAF" || r.InstanceName == "second_CAF" {
					want = 1
				}
				if n := countRequests(mock, "GET /api/orgs/"+testOrgID+"/dataset-rules/"+r.ID.String()); n != want {
					t.Errorf("%s detail GETs = %d, want %d", r.InstanceName, n, want)
				}
			}
		})
	}
}

func TestApplyTableSchema_MappedWarningKeepsStructuredStdout(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
			registerTableRulesList(mock, 42, dataset_rules.DatasetRuleTiny{ID: uuid.MustParse(builtinFilterRuleID),
				InstanceName: "mapped_check", State: api.DatasetRuleStateEnabled, DatasetColumns: &[]string{"amount"}})
			registerFilterRuleDetail(mock, builtinFilterRuleID, true)
			stdout, stderr, err := execApplyStreams(t, writeManifest(t, deleteAmount), "--yes", "-o", format)
			if qerrors.GetExitCode(err) != 0 {
				t.Fatalf("apply: %v", err)
			}
			var doc map[string]any
			switch format {
			case "json":
				err = json.Unmarshal([]byte(stdout), &doc)
			case "yaml":
				err = yaml.Unmarshal([]byte(stdout), &doc)
			}
			if err != nil || strings.Contains(stdout, "Warning:") || !strings.Contains(stderr, "a table rule maps") {
				t.Errorf("stdout must stay structured with warning on stderr: err %v, stdout %q, stderr %q", err, stdout, stderr)
			}
		})
	}
}
