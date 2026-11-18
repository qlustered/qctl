package apply

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
)

const savedFilterYAML = `  row_filter:
    description: Completed rows
    conditions:
      - column_name: status
        operator: eq
        value: completed
`

func TestApplyTableRuleRowFilterInstantiate(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var req api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
	doc := tableRuleInstantiateDoc("filtered_email") + "  column_mapping: {email: email_col}\n" + savedFilterYAML
	if _, err := execApply(t, env.CreateFile("filter.yaml", doc), "--yes"); err != nil {
		t.Fatal(err)
	}
	if req.RowFilter == nil || req.RowFilter.Description != "Completed rows" || req.RowFilter.Conditions == nil || (*req.RowFilter.Conditions)[0].ColumnName != "status" {
		t.Fatalf("filter lost: %+v", req.RowFilter)
	}
}

func TestApplyTableRuleRowFilterPatchAndOffer(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(map[bool]string{false: "set", true: "clear"}[clear], func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			before := sampleDatasetRuleDetailForApply()
			after := before
			var f api.RowFilter
			if err := json.Unmarshal([]byte(`{"description":"Completed rows","conditions":[{"column_name":"status","operator":"eq","value":"completed"}]}`), &f); err != nil {
				t.Fatal(err)
			}
			if clear {
				before.RowFilter = &f
				after.RowFilter = nil
			} else {
				after.RowFilter = &f
			}
			registerRuleDetailGet(mock, before)
			var body map[string]json.RawMessage
			base := "/api/orgs/" + testOrgID + "/datasets/1/dataset-rules/" + testTableRuleID
			mock.RegisterHandler("PATCH", base, func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Error(err)
				}
				testutil.RespondJSON(w, 200, after)
			})
			counts := 0
			mock.RegisterHandler("GET", base+"/recheck-clean-rows", func(w http.ResponseWriter, _ *http.Request) {
				counts++
				testutil.RespondJSON(w, 200, map[string]any{"count": 4, "capped": true, "predicate_errors": 1})
			})
			doc := tableRulePatchDoc(before.InstanceName)
			if clear {
				doc += "  row_filter: null\n"
			} else {
				doc += savedFilterYAML
			}
			stdout, stderr, err := execApplyStreams(t, env.CreateFile("filter.yaml", doc), "--yes", "-o", "json")
			if err != nil {
				t.Fatal(err)
			}
			if len(body) != 1 || body["row_filter"] == nil {
				t.Fatalf("patch=%v", body)
			}
			if clear && string(body["row_filter"]) != "null" {
				t.Fatalf("clear=%s", body["row_filter"])
			}
			if counts != 1 || !strings.Contains(stderr, "at least 4") || !strings.Contains(stderr, "--dry-run") {
				t.Fatalf("offer: %d %s", counts, stderr)
			}
			var result any
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatalf("stdout=%s: %v", stdout, err)
			}
			for _, request := range mock.Requests() {
				if strings.HasPrefix(request, "POST ") {
					t.Fatalf("apply automatically rechecked: %s", request)
				}
			}
		})
	}
}

func TestCandidateDescriptionOnlyFilterIsAppliedNow(t *testing.T) {
	var before dataset_rules.DatasetRuleDetail
	if err := json.Unmarshal([]byte(`{"row_filter":{"description":"Old","conditions":[{"column_name":"status","operator":"is_blank"}]}}`), &before); err != nil {
		t.Fatal(err)
	}
	var manifest dataset_rules.TableRuleApplyManifest
	if err := json.Unmarshal([]byte(`{"spec":{"row_filter":{"description":"New","conditions":[{"column_name":"status","operator":"is_blank"}]}}}`), &manifest); err != nil {
		t.Fatal(err)
	}
	applied, staged := dataset_rules.SplitPatchOutcome(dataset_rules.BuildPatchMap(&manifest), true, &before)
	if len(applied) != 1 || applied[0] != "row_filter" || len(staged) != 0 {
		t.Fatalf("outcome=%v %v", applied, staged)
	}
}

func TestApplyTableRuleDuplicateComparisonIncludesRowFilter(t *testing.T) {
	for _, pending := range []bool{false, true} {
		kind := map[bool]string{false: "attached", true: "pending create"}[pending]
		for _, tc := range []struct {
			name      string
			filter    string
			duplicate bool
		}{
			{"same filter", savedFilterYAML, true},
			{"description only", strings.Replace(savedFilterYAML, "Completed rows", "Already completed", 1), true},
			{"no filter", "", false},
			{"another operand", strings.Replace(savedFilterYAML, "value: completed", "value: pending", 1), false},
			{"case sensitive", savedFilterYAML + "        case_sensitive: true\n", false},
			{"predicate added", savedFilterYAML + "    predicate:\n      source: 'def applies(row): return True'\n      columns: [status]\n", false},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				env, mock := setupTableRuleApplyEnv(t)
				defer env.Cleanup()
				defer mock.Close()
				var req api.InstantiateRuleRequestSchema
				registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &req)
				var filter api.RowFilter
				if err := json.Unmarshal([]byte(`{"description":"Completed rows","conditions":[{"column_name":"status","operator":"eq","value":"completed","case_sensitive":false}]}`), &filter); err != nil {
					t.Fatal(err)
				}
				if pending {
					suggestion := pendingCreateSuggestion()
					suggestion.RowFilter = &filter
					registerPendingCreateSuggestions(t, mock, suggestion)
				} else {
					registerTableRulesList(mock, 1, listedTableRule(testSiblingRuleID, "email_check", api.DatasetRuleStateEnabled, "email_col"))
					mock.RegisterHandler("GET", siblingDetailPath, func(w http.ResponseWriter, _ *http.Request) {
						detail := sampleDatasetRuleDetailForApply()
						detail.ColumnMappingDict = map[string]string{"email": "email_col"}
						detail.Params = map[string]interface{}{}
						detail.RowFilter = &filter
						testutil.RespondJSON(w, http.StatusOK, detail)
					})
				}
				_, err := execApply(t, env.CreateFile("repeat-filter.yaml", namelessCreateDoc()+tc.filter), "--yes")
				if tc.duplicate {
					assertRefusedWithoutPost(t, mock, err)
				} else if err != nil || postCount(mock) != 1 {
					t.Fatalf("distinct filter create = %v, POSTs = %d", err, postCount(mock))
				}
			})
		}
	}
}
