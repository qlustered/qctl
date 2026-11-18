package get

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestGetDataRuleSendsEveryFamilyInstance(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := dataTestMock(t)
			familyID := uuid.New()
			first, second, other := sampleDatasetRuleTiny(), sampleDatasetRuleTiny2(), sampleDatasetRuleTiny()
			first.FamilyID, second.FamilyID, other.FamilyID = familyID, familyID, uuid.New()
			other.ID = uuid.New()
			first.FamilyName, second.FamilyName, other.FamilyName = "Email Check", "Email Check", "Email Check"
			second.State = "deleted"
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("search_query") != "email-check" {
					t.Errorf("unexpected family query: %s", r.URL.RawQuery)
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 1, "results": []api.RuleFamilyItemSchema{{FamilyID: familyID, Slug: "email-check", Name: "Email Check"}}})
			})
			var pages []string
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				pages = append(pages, q.Get("page"))
				if q.Get("order_by") != "position" {
					t.Errorf("expected ordered pagination: %v", q)
				}
				rows := []api.DatasetRuleTinySchema{first, other}
				if q.Get("page") == "2" {
					rows = []api.DatasetRuleTinySchema{second}
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 3, "results": rows})
			})
			var ids []string
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data", func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				ids = q["dataset_rule_ids_"]
				if q.Get("is_bad_data") != "true" || q.Get("alert_item_id") != "8817" || q.Get("limit") != "20" {
					t.Errorf("lost other filters: %v", q)
				}
				testutil.RespondJSON(w, http.StatusOK, sampleGetData())
			})
			if out, err := execData(t, []string{"1", "--is-bad-data", "--rule", "email-check", "--alert-item-id", "8817", "-o", format}); err != nil {
				t.Fatalf("execute: %v\n%s", err, out)
			}
			if !reflect.DeepEqual(ids, []string{first.ID.String(), second.ID.String()}) || !reflect.DeepEqual(pages, []string{"1", "2"}) {
				t.Fatalf("ids = %v, pages = %v", ids, pages)
			}
		})
	}
}

func TestGetDataRuleRefusesInvalidBeforeRequests(t *testing.T) {
	for _, args := range [][]string{
		{"--rule", "email-check"},
		{"--is-bad-data=false", "--rule", "email-check"},
		{"--is-bad-data", "--rule", ""},
		{"--is-bad-data", "--rule", "  "},
		{"--is-bad-data", "--rule", "email-check", "--dataset-rule-ids", "550e8400-e29b-41d4-a716-446655440000"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := dataTestMock(t)
			_, err := execData(t, append([]string{"orders"}, args...))
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || len(mock.Requests()) != 0 {
				t.Fatalf("err = %v, requests = %v", err, mock.Requests())
			}
		})
	}
}

func TestGetDataRuleMissNeverReadsData(t *testing.T) {
	for _, missingFamily := range []bool{true, false} {
		t.Run(strconv.FormatBool(missingFamily), func(t *testing.T) {
			mock := dataTestMock(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, _ *http.Request) {
				rows := []api.RuleFamilyItemSchema{}
				if !missingFamily {
					rows = append(rows, api.RuleFamilyItemSchema{FamilyID: uuid.New(), Slug: "email-check"})
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": len(rows), "results": rows})
			})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 0, "results": []any{}})
			})
			_, err := execData(t, []string{"1", "--is-bad-data", "--rule", "email-check"})
			if qerrors.GetExitCode(err) != qerrors.ExitNotFound {
				t.Fatalf("err = %v, want exit 3", err)
			}
			for _, req := range mock.Requests() {
				if strings.HasSuffix(req, "/data") || strings.Contains(req, "/data?") {
					t.Fatalf("unfiltered data request: %s", req)
				}
			}
		})
	}
}
