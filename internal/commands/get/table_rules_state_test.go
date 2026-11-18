package get

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestTableRulesStateFiltersAllPagesBeforePagination(t *testing.T) {
	for _, format := range []string{"json", "yaml", "table", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := newTableRulesTestEnv(t)
			var pages []string
			first, second := sampleDatasetRuleTiny(), sampleDatasetRuleTiny2()
			first.State = "disabled"
			first.FamilyName, second.FamilyName = "Email Check", "Phone Normalizer"
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				pages = append(pages, q.Get("page"))
				if q.Get("order_by") != "instance_name" || q.Get("search_query") != "check" || q.Get("reverse") != "false" || q.Has("state") {
					t.Errorf("unexpected query: %v", q)
				}
				rows := []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny(), first}
				if q.Get("page") == "2" {
					rows = []dataset_rules.DatasetRuleTiny{second}
				}
				total := 3
				testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{Results: rows, TotalRows: &total})
			})
			cmd := setupTableRulesTestCommand()
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"get", "table-rules", "1", "--state", "disabled", "--page", "2", "--limit", "1",
				"--search", "check", "--order-by", "instance_name", "--reverse", "-o", format})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !reflect.DeepEqual(pages, []string{"1", "2"}) {
				t.Fatalf("server pages = %v, want [1 2]", pages)
			}
			switch format {
			case "json":
				var got struct {
					Items []dataset_rules.DatasetRuleTiny `json:"items"`
					Total int                             `json:"total"`
					Page  int                             `json:"page"`
					Limit int                             `json:"limit"`
					Next  *int                            `json:"next_page"`
				}
				if err := json.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Total != 2 || got.Page != 2 || got.Limit != 1 || got.Next != nil || len(got.Items) != 1 ||
					got.Items[0].ID != second.ID || got.Items[0].State != "disabled" || got.Items[0].FamilyName != second.FamilyName {
					t.Fatalf("wrong filtered page: %s", out.String())
				}
			case "name":
				if !strings.Contains(out.String(), second.ID.String()) || strings.Contains(out.String(), first.ID.String()) {
					t.Fatalf("wrong name page: %s", out.String())
				}
			default:
				if !strings.Contains(out.String(), "phone_normalizer") || strings.Contains(out.String(), "email_check") {
					t.Fatalf("wrong filtered page: %s", out.String())
				}
			}
			if format == "table" && (!strings.Contains(out.String(), "RULE") || !strings.Contains(out.String(), second.FamilyName)) {
				t.Fatalf("missing family column: %s", out.String())
			}
		})
	}
}

func TestTableRulesStateRejectsInvalidBeforeTableResolution(t *testing.T) {
	for _, state := range []string{"paused", "queued", "", "ENABLED"} {
		t.Run(state, func(t *testing.T) {
			mock := newTableRulesTestEnv(t)
			cmd := setupTableRulesTestCommand()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs([]string{"get", "table-rules", "orders", "--state", state})
			err := cmd.Execute()
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || len(mock.Requests()) != 0 {
				t.Fatalf("err = %v, requests = %v", err, mock.Requests())
			}
		})
	}
}

func TestTableRulesStateBeyondLastPage(t *testing.T) {
	mock := newTableRulesTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, _ *http.Request) {
		total := 1
		testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{Results: []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny2()}, TotalRows: &total})
	})
	cmd := setupTableRulesTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"get", "table-rules", "1", "--state", "disabled", "--page", strconv.Itoa(math.MaxInt), "--limit", "2", "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"items": []`) || !strings.Contains(out.String(), `"total": 1`) || !strings.Contains(out.String(), `"next_page": null`) {
		t.Fatalf("wrong empty page: %s", out.String())
	}
}
