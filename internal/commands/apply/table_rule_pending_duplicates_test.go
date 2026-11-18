package apply

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const pendingCreatesPath = "/api/orgs/" + testOrgID + "/datasets/1/rule-suggestions"

func pendingCreateSuggestion() api.DatasetRuleSuggestionSchema {
	suggestion := sampleSuggestion(api.SuggestionStatePending)
	suggestion.Action = api.SuggestionActionCreate
	suggestion.ExistingDatasetRuleID = nil
	revisionID := uuid.MustParse(testRuleRevID)
	suggestion.RuleRevisionID = &revisionID
	suggestion.Severity = api.RuleSeverityAsDeclared
	params := map[string]interface{}{}
	suggestion.Params = &params
	return suggestion
}

func suggestionListItem(suggestion api.DatasetRuleSuggestionSchema) api.DatasetRuleSuggestionTinySchema {
	return api.DatasetRuleSuggestionTinySchema{
		ID: suggestion.ID, InstanceName: suggestion.InstanceName,
		Action: suggestion.Action, State: suggestion.State, Severity: suggestion.Severity,
		Source: suggestion.Source, CreatedAt: suggestion.CreatedAt,
	}
}

func registerPendingCreateSuggestions(t *testing.T, mock *testutil.MockAPIServer, suggestions ...api.DatasetRuleSuggestionSchema) {
	t.Helper()
	items := make([]api.DatasetRuleSuggestionTinySchema, 0, len(suggestions))
	for _, suggestion := range suggestions {
		items = append(items, suggestionListItem(suggestion))
		mock.RegisterHandler("GET", pendingCreatesPath+"/"+suggestion.ID.String(), func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, suggestion)
		})
	}
	mock.RegisterHandler("GET", pendingCreatesPath, func(w http.ResponseWriter, r *http.Request) {
		assertPendingCreateQuery(t, r)
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{Results: items})
	})
}

func assertPendingCreateQuery(t *testing.T, r *http.Request) {
	t.Helper()
	query := r.URL.Query()
	if query.Get("state") != "pending" || query.Get("limit") != "1000" || query.Get("source") != "" {
		t.Errorf("pending-create query = %s", r.URL.RawQuery)
	}
}

func TestApplyTableRuleCreate_RepeatedStagedCreateExitsTwo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra string
	}{
		{name: "nameless"},
		{name: "named", extra: "  instance_name: email_check\n"},
		{name: "forced named", extra: "  instance_name: email_check\n  force: true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			revision := sampleRuleRevisionFull(nil)
			revision.EnrichesColumns = []string{"normalized"}
			var request api.InstantiateRuleRequestSchema
			registerInstantiateMocks(t, mock, revision, &request)
			suggestion := pendingCreateSuggestion()
			outputs := []map[string]interface{}{{"rule_field_name": "normalized", "dataset_field_name": "normalized_email", "field_schema": map[string]interface{}{"type": "String"}}}
			suggestion.ProducedFields = &outputs
			staged := false
			mock.RegisterHandler("GET", pendingCreatesPath, func(w http.ResponseWriter, r *http.Request) {
				assertPendingCreateQuery(t, r)
				items := []api.DatasetRuleSuggestionTinySchema{}
				if staged {
					items = append(items, suggestionListItem(suggestion))
				}
				testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{Results: items})
			})
			mock.RegisterHandler("GET", pendingCreatesPath+"/"+testSuggestionID, func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, suggestion)
			})
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+instantiatePath, func(w http.ResponseWriter, r *http.Request) {
				staged = true
				testutil.RespondJSON(w, http.StatusAccepted, suggestion)
			})
			path := env.CreateFile("staged.yaml", namelessCreateDoc()+tc.extra+`  produced_fields:
    - rule_field_name: normalized
      dataset_field_name: normalized_email
      field_schema: {type: String}
`)
			if out, err := execApply(t, path, "--yes"); err != nil || !strings.Contains(out, "staged for review") {
				t.Fatalf("first apply = %s, %v", out, err)
			}
			_, err := execApply(t, path, "--yes")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("second apply exit = %d, want 2: %v", code, err)
			}
			if n := postCount(mock); n != 1 {
				t.Errorf("POSTs after both applies = %d, want 1", n)
			}
			for _, want := range []string{"pending create suggestion " + testSuggestionID, "qctl describe rule-suggestion " + testSuggestionID + " --table-id 1", "qctl suggestion dismiss " + testSuggestionID + " --table-id 1"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "metadata.id") || strings.Contains(err.Error(), "spec.upgradedFrom") {
				t.Errorf("refusal addresses a suggestion as an attached rule: %v", err)
			}
		})
	}
}

func TestApplyTableRuleCreate_PendingCreateDifferentConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*api.DatasetRuleSuggestionSchema)
		manifest string
		warn     bool
	}{
		{name: "another revision", warn: true, change: func(s *api.DatasetRuleSuggestionSchema) {
			id := uuid.MustParse(testRebindRevID)
			s.RuleRevisionID = &id
		}},
		{name: "unknown revision", warn: true, change: func(s *api.DatasetRuleSuggestionSchema) { s.RuleRevisionID = nil }},
		{name: "another severity", warn: true, change: func(s *api.DatasetRuleSuggestionSchema) { s.Severity = api.RuleSeverityWarning }},
		{name: "another param", warn: true, change: func(s *api.DatasetRuleSuggestionSchema) { *s.Params = map[string]interface{}{"threshold": 1} }},
		{name: "another mapping", warn: true, change: func(s *api.DatasetRuleSuggestionSchema) {
			s.RuleColumnMapping = map[string]string{"email": "another_email"}
		}},
		{name: "another field", warn: true, change: func(s *api.DatasetRuleSuggestionSchema) {
			s.RuleColumnMapping = map[string]string{"other": "email_col"}
		}},
		{name: "named override", warn: true, manifest: namelessCreateDoc() + "  instance_name: second_check\n"},
		{name: "name case differs", warn: true, manifest: namelessCreateDoc() + "  instance_name: Email_Check\n"},
		{name: "another family", change: func(s *api.DatasetRuleSuggestionSchema) {
			s.RuleFamilyID = uuid.MustParse("99990000-1111-2222-3333-444444444444")
		}},
		{name: "pending update", change: func(s *api.DatasetRuleSuggestionSchema) { s.Action = api.SuggestionActionUpdate }},
		{name: "pending delete", change: func(s *api.DatasetRuleSuggestionSchema) { s.Action = api.SuggestionActionDelete }},
		{name: "already applied", change: func(s *api.DatasetRuleSuggestionSchema) { s.State = api.SuggestionStateApplied }},
		{name: "dismissed", change: func(s *api.DatasetRuleSuggestionSchema) { s.State = api.SuggestionStateDismissed }},
		{name: "failed", change: func(s *api.DatasetRuleSuggestionSchema) { s.State = api.SuggestionStateFailed }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			var request api.InstantiateRuleRequestSchema
			registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &request)
			suggestion := pendingCreateSuggestion()
			if tc.change != nil {
				tc.change(&suggestion)
			}
			registerPendingCreateSuggestions(t, mock, suggestion)
			manifest := tc.manifest
			if manifest == "" {
				manifest = namelessCreateDoc()
			}
			_, stderr, err := execApplyRoot(t, "apply", "-f", env.CreateFile("different.yaml", manifest), "--yes")
			if err != nil || postCount(mock) != 1 {
				t.Fatalf("create = %v, POSTs = %d", err, postCount(mock))
			}
			if got := strings.Contains(stderr, "beside pending create suggestion"); got != tc.warn {
				t.Errorf("warning = %t, want %t: %s", got, tc.warn, stderr)
			}
		})
	}
}

func TestApplyTableRuleCreate_PendingRepeatOnLaterPage(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var request api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &request)
	suggestion := pendingCreateSuggestion()
	registerPendingCreateSuggestions(t, mock, suggestion)
	pages := []int{}
	mock.RegisterHandler("GET", pendingCreatesPath, func(w http.ResponseWriter, r *http.Request) {
		assertPendingCreateQuery(t, r)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pages = append(pages, page)
		item := suggestionListItem(suggestion)
		list := api.DatasetRuleSuggestionListSchema{Results: []api.DatasetRuleSuggestionTinySchema{item}}
		if page == 1 {
			list.Results[0].Action = api.SuggestionActionUpdate
			list.Next = &api.PaginationSchema{Start: testutil.IntPtr(2)}
		}
		testutil.RespondJSON(w, http.StatusOK, list)
	})
	_, err := execApply(t, env.CreateFile("later.yaml", namelessCreateDoc()), "--yes")
	assertRefusedWithoutPost(t, mock, err)
	if len(pages) != 2 || pages[0] != 1 || pages[1] != 2 {
		t.Errorf("pages = %v, want [1 2]", pages)
	}
}

func TestApplyTableRuleCreate_PendingReadFailureKeepsExitCode(t *testing.T) {
	for _, endpoint := range []string{pendingCreatesPath, pendingCreatesPath + "/" + testSuggestionID} {
		for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
			t.Run(endpoint+"/"+strconv.Itoa(status), func(t *testing.T) {
				env, mock := setupTableRuleApplyEnv(t)
				defer env.Cleanup()
				defer mock.Close()
				var request api.InstantiateRuleRequestSchema
				registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &request)
				registerPendingCreateSuggestions(t, mock, pendingCreateSuggestion())
				mock.RegisterHandler("GET", endpoint, func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, status, map[string]interface{}{"detail": map[string]interface{}{"msg": "cannot inspect suggestion", "error_code": "TEST"}})
				})
				_, err := execApply(t, env.CreateFile("failure.yaml", namelessCreateDoc()), "--yes")
				if got, want := qerrors.GetExitCode(err), qerrors.GetExitCode(qerrors.FromHTTPStatus(status, "test")); got != want {
					t.Fatalf("exit = %d, want %d: %v", got, want, err)
				}
				if postCount(mock) != 0 {
					t.Error("read failure sent a create")
				}
			})
		}
	}
}

func TestApplyTableRuleCreate_PendingRepeatPreservesLargeParams(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		refused     bool
	}{
		{name: "same", value: "9007199254740993", refused: true},
		{name: "different", value: "9007199254740992"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			var request api.InstantiateRuleRequestSchema
			registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &request)
			suggestion := pendingCreateSuggestion()
			*suggestion.Params = map[string]interface{}{"threshold": json.Number(tc.value)}
			registerPendingCreateSuggestions(t, mock, suggestion)
			_, err := execApply(t, env.CreateFile("large.yaml", namelessCreateDoc()+"  params: {threshold: 9007199254740993}\n"), "--yes")
			if tc.refused {
				assertRefusedWithoutPost(t, mock, err)
			} else if err != nil || postCount(mock) != 1 {
				t.Fatalf("distinct params refused: %v", err)
			}
		})
	}
}

func TestApplyTableRuleCreate_PendingRepeatUsesConceptFill(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	revision := sampleRuleRevisionFull(nil)
	revision.FieldColumnCandidates = &[]api.RuleFieldColumnCandidatesSchema{conceptCandidate("email", "contact-email", "customer_email")}
	var request api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &request)
	suggestion := pendingCreateSuggestion()
	suggestion.RuleColumnMapping = map[string]string{"email": "customer_email"}
	registerPendingCreateSuggestions(t, mock, suggestion)
	_, err := execApply(t, env.CreateFile("concept.yaml", namelessIdentityDoc()), "--yes")
	assertRefusedWithoutPost(t, mock, err)
	if !strings.Contains(err.Error(), "pending create suggestion") {
		t.Fatalf("unexpected refusal: %v", err)
	}
}

func TestApplyTableRuleCreate_SuggestionResolvedAfterListing(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	var request api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &request)
	suggestion := pendingCreateSuggestion()
	registerPendingCreateSuggestions(t, mock, suggestion)
	suggestion.State = api.SuggestionStateDismissed
	mock.RegisterHandler("GET", pendingCreatesPath+"/"+testSuggestionID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, suggestion)
	})
	_, err := execApply(t, env.CreateFile("resolved.yaml", tableRuleInstantiateDoc("email_check")), "--yes")
	if err != nil || postCount(mock) != 1 {
		t.Fatalf("resolved suggestion blocked create: %v", err)
	}
}
