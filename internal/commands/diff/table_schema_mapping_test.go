package diff

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDiffTableSchema_MappedColumnWarning(t *testing.T) {
	for _, caf := range []bool{false, true} {
		t.Run(map[bool]string{false: "builtin retirement", true: "CAF optional unbinding"}[caf], func(t *testing.T) {
			_, mock := newDiffTestEnv(t)
			registerSchemaDetail(mock)
			detail := sampleDetail()
			detail.IsBuiltin, detail.IsCaf = true, caf
			detail.ColumnMappingDict = map[string]string{"context": "amount"}
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{Results: []dataset_rules.DatasetRuleTiny{
					{ID: detail.ID, InstanceName: "mapped_check", State: api.DatasetRuleStateEnabled, DatasetColumns: &[]string{"amount"}},
				}})
			})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+detail.ID.String(), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, detail)
			})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+detail.RuleRevision.ID.String()+"/details", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.RuleRevisionFullSchema{ID: detail.RuleRevision.ID, OptionalColumns: &[]string{"context"}})
			})
			stdout, stderr, err := execDiff(t, writeSchemaManifest(t, diffDeleteAmount))
			if code := qerrors.GetExitCode(err); code != 1 || stderr != "" {
				t.Fatalf("diff must keep exit 1 and empty stderr: code %d, stderr %q, err %v", code, stderr, err)
			}
			want := "retires (deletes) the rule"
			if caf {
				want = "unbinds these optional CAF mappings"
			}
			if !strings.Contains(stdout, "maps amount") || !strings.Contains(stdout, want) {
				t.Errorf("warning missing:\n%s", stdout)
			}
			changes, warning, body := strings.Index(stdout, "is_deleted"), strings.Index(stdout, "Warning:"), strings.Index(stdout, "request body:")
			if changes < 0 || warning < changes || body < warning {
				t.Errorf("want changes, warning, request body:\n%s", stdout)
			}
			for _, request := range mock.Requests() {
				if !strings.HasPrefix(request, "GET ") {
					t.Errorf("diff sent a mutation: %s", request)
				}
			}
		})
	}
}
