package download

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDownloadDataRuleSendsEveryFamilyInstance(t *testing.T) {
	f := newDataTestServer(t)
	familyID, firstID, secondID := uuid.New(), uuid.New(), uuid.New()
	f.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 1, "results": []api.RuleFamilyItemSchema{{FamilyID: familyID, Slug: "email-check", Name: "Email Check"}}})
	})
	var pages []string
	f.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		row := api.DatasetRuleTinySchema{ID: firstID, FamilyID: familyID, FamilyName: "Email Check", Release: "1.0.0", State: "disabled"}
		if r.URL.Query().Get("page") == "2" {
			row.ID, row.Release, row.State = secondID, "2.0.0", "deleted"
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 2, "results": []api.DatasetRuleTinySchema{row}})
	})
	_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--is-bad-data", "--rule", "email-check", "--alert-item-id", "8817")
	if err != nil {
		t.Fatal(err)
	}
	_, posts, _, _, raw := f.counters()
	query, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	if posts != 1 || query.Get("is_bad_data") != "true" || query.Get("alert_item_id") != "8817" ||
		!reflect.DeepEqual(query["dataset_rule_ids_"], []string{firstID.String(), secondID.String()}) ||
		!reflect.DeepEqual(pages, []string{"1", "2"}) {
		t.Fatalf("posts = %d, query = %v, pages = %v", posts, query, pages)
	}
}

func TestDownloadDataRuleRefusesInvalidBeforeRequests(t *testing.T) {
	for _, args := range [][]string{
		{"--rule", "email-check"},
		{"--is-bad-data=false", "--rule", "email-check"},
		{"--is-bad-data", "--rule", ""},
		{"--is-bad-data", "--rule", "  "},
		{"--is-bad-data", "--rule", "email-check", "--dataset-rule-ids", "550e8400-e29b-41d4-a716-446655440000"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newDataTestServer(t)
			_, _, err := runDownloadData(t, append([]string{"download", "data", "orders"}, args...)...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || len(f.mock.Requests()) != 0 {
				t.Fatalf("err = %v, requests = %v", err, f.mock.Requests())
			}
		})
	}
}

func TestDownloadDataRuleMissNeverQueuesExport(t *testing.T) {
	f := newDataTestServer(t)
	f.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-families", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 1, "results": []api.RuleFamilyItemSchema{{FamilyID: uuid.New(), Slug: "email-check"}}})
	})
	f.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 0, "results": []any{}})
	})
	_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--is-bad-data", "--rule", "email-check", "--no-wait")
	if qerrors.GetExitCode(err) != qerrors.ExitNotFound {
		t.Fatalf("err = %v, want exit 3", err)
	}
	_, posts, _, _, _ := f.counters()
	if posts != 0 || f.listCalls != 0 {
		t.Fatalf("posts = %d, polls = %d, want neither", posts, f.listCalls)
	}
}
