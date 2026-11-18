package alert

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// registerResolveWithUploadFields answers the resolve POST for id with a
// stored_item_url whose fields carry a credential, which no format may print.
func registerResolveWithUploadFields(mock *testutil.MockAPIServer, id int) {
	mock.RegisterHandler("POST", fmt.Sprintf("/api/orgs/%s/alerts/resolve/%d", testOrgID, id),
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
				`{"redirect_post_submit_to":null,"stored_item_url":{"url":"https://bucket","fields":{"x-amz-signature":"UPLOAD-SECRET"}}}`))
		})
}

// resolve --action writes {id, action, resolved, dataset_id} under -o json and
// alert/<id> under -o name; the prose moves to stderr.
func TestResolveWritesTheResultDocument(t *testing.T) {
	for _, format := range []string{"json", "name"} {
		mock := newAlertTestEnv(t)
		registerResolvingGetAlert(mock, migrationAlert())
		registerResolveWithUploadFields(mock, 9)

		stdout, stderr, err := runAlertCommandSplit(t,
			"alert", "resolve", "9", "--action", "discard_migration", "--yes", "-o", format)
		if err != nil {
			t.Fatalf("-o %s: %v\n%s", format, err, stderr)
		}
		if !strings.Contains(stderr, "alert 9 resolved") {
			t.Errorf("-o %s: stderr missing the outcome line:\n%s", format, stderr)
		}
		if strings.Contains(stdout+stderr, "UPLOAD-SECRET") {
			t.Errorf("-o %s printed the resolve response's stored_item_url fields", format)
		}
		if format == "name" {
			if stdout != "alert/9\n" {
				t.Errorf("-o name stdout = %q, want %q", stdout, "alert/9\n")
			}
			continue
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
		}
		want := map[string]any{"id": float64(9), "action": "discard_migration", "resolved": true, "dataset_id": float64(42)}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %v, want %v (document %v)", k, got[k], v, got)
			}
		}
	}
}

// A re-read that fails leaves resolved null rather than guessing.
func TestResolveResultIsNullWhenTheReReadFails(t *testing.T) {
	mock := newAlertTestEnv(t)
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/9",
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls > 1 {
				testutil.RespondJSON(w, http.StatusInternalServerError, map[string]string{"detail": "boom"})
				return
			}
			testutil.RespondJSON(w, http.StatusOK, migrationAlert())
		})
	registerResolveWithUploadFields(mock, 9)

	stdout, stderr, err := runAlertCommandSplit(t,
		"alert", "resolve", "9", "--action", "discard_migration", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if v, ok := got["resolved"]; !ok || v != nil {
		t.Errorf("resolved = %v (present %v), want null", v, ok)
	}
}

// A file of several documents prints one List of the alerts it sent an action
// to; the runner's per-document lines go to stderr.
func TestResolveFileWritesOneListForSeveralDocuments(t *testing.T) {
	mock := newAlertTestEnv(t)
	for _, id := range []int{9, 10} {
		registerResolvingGetAlertID(mock, id, mapValueAlertWithID(id))
		registerResolveWithUploadFields(mock, id)
	}
	doc := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: %d\nspec:\n  action: map_value\n  new_value: \"1420.00\"\n"
	path := writeManifest(t, fmt.Sprintf(doc, 9)+"---\n"+fmt.Sprintf(doc, 10))

	stdout, stderr, err := runAlertCommandSplit(t, "alert", "resolve", "-f", path, "-o", "json")
	if err != nil {
		t.Fatalf("resolve -f: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "document 2: alert 10 resolved") {
		t.Errorf("stderr missing the runner's lines:\n%s", stderr)
	}
	var list struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if list.Kind != "List" || len(list.Items) != 2 || list.Items[1]["id"] != float64(10) {
		t.Errorf("stdout = %+v, want a List of alerts 9 and 10", list)
	}

	stdout, _, err = runAlertCommandSplit(t, "alert", "resolve", "-f", path, "-o", "name")
	if err != nil {
		t.Fatalf("resolve -f -o name: %v", err)
	}
	if stdout != "alert/9\nalert/10\n" {
		t.Errorf("-o name stdout = %q, want both alerts", stdout)
	}
}

// The apply_migration pre-flight is prose: under -o json it reaches stderr and
// stdout stays one document.
func TestResolvePreflightMovesToStderrUnderJSON(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	registerPendingSuggestions(mock,
		suggestionDetail("11111111-1111-1111-1111-111111111111", api.SuggestionActionCreate, "Range Rule (1)"))
	registerResolveWithUploadFields(mock, 9)

	stdout, stderr, err := runAlertCommandSplit(t,
		"alert", "resolve", "9", "--action", "apply_migration", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Range Rule (1)") {
		t.Errorf("stderr missing the pre-flight:\n%s", stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
}

// --dry-run writes a YAML manifest: -o yaml keeps it on stdout, and -o json or
// name is refused before anything is read.
func TestResolveDryRunFormats(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, mapValueAlert())

	stdout, _, err := runAlertCommandSplit(t,
		"alert", "resolve", "9", "--action", "map_value", "--dry-run", "-o", "yaml")
	if err != nil {
		t.Fatalf("--dry-run -o yaml: %v", err)
	}
	if !strings.Contains(stdout, "kind: AlertResolution") {
		t.Errorf("--dry-run -o yaml stdout = %q, want the manifest", stdout)
	}
	for _, format := range []string{"json", "name"} {
		_, _, err := runAlertCommandSplit(t,
			"alert", "resolve", "9", "--action", "map_value", "--dry-run", "-o", format)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("--dry-run -o %s: exit %d (%v), want %d", format, code, err, qerrors.ExitBadUsage)
		}
	}
}

// retract writes the server's {issue_resolved, results} under -o json and
// alert/<id> under -o name.
func TestRetractWritesTheResponse(t *testing.T) {
	for _, format := range []string{"json", "name"} {
		mock := newAlertTestEnv(t)
		registerGetAlert8817(mock, userSubmittedAlert())
		registerAlertRetract(mock, new(map[string]interface{}), api.UserSubmittedIssueRetractResponseSchema{
			IssueResolved: true,
			Results:       []api.UserSubmittedIssueRetractResultSchema{{RowID: 1042, Status: api.Retracted}},
		})

		stdout, stderr, err := runAlertCommandSplit(t,
			"alert", "retract", "8817", "--row", "1042", "--yes", "-o", format)
		if err != nil {
			t.Fatalf("-o %s: %v\n%s", format, err, stderr)
		}
		if !strings.Contains(stderr, "alert/8817 retracted from") {
			t.Errorf("-o %s: stderr missing the outcome line:\n%s", format, stderr)
		}
		if format == "name" {
			if stdout != "alert/8817\n" {
				t.Errorf("-o name stdout = %q, want %q", stdout, "alert/8817\n")
			}
			continue
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
		}
		if got["issue_resolved"] != true {
			t.Errorf("issue_resolved = %v, want true", got["issue_resolved"])
		}
		if results, _ := got["results"].([]any); len(results) != 1 {
			t.Errorf("results = %v, want one row", got["results"])
		}
	}
}

const stagedSuggestionID = "33333333-3333-3333-3333-333333333333"

// keywordAlert is an open invalid_keyword alert 9 on table 42 offering the
// plain keyword action and one compound keyword action.
func keywordAlert() alerts.AlertFull {
	resolved := false
	list := []api.AlertAction{
		api.AlertActionValidationAddANewKeyword,
		api.AlertActionValidationAddANewKeywordMapValue,
	}
	return alerts.AlertFull{
		ID:               9,
		DatasetID:        42,
		DatasetName:      "orders",
		IssueType:        api.AlertTypeInvalidKeyword,
		Resolved:         &resolved,
		FieldName:        strPtrForTest("status"),
		FieldValue:       strPtrForTest("in_transit"),
		AlertActionsList: &list,
	}
}

// registerOpenGetAlertID serves alert unresolved on every GET.
func registerOpenGetAlertID(mock *testutil.MockAPIServer, alert alerts.AlertFull) {
	mock.RegisterHandler("GET", fmt.Sprintf("/api/orgs/%s/alerts/%d", testOrgID, alert.ID),
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, alert)
		})
}

// registerStagingResolve answers the resolve POST for alert 9 with 200 and the
// staged suggestion's id.
func registerStagingResolve(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
				`{"redirect_post_submit_to":null,"dataset_rule_suggestion_id":"`+stagedSuggestionID+`"}`))
		})
}

// registerStagedSuggestion serves the staged suggestion as a draft-migration
// suggestion, or as a revalidation candidate when candidate is set.
func registerStagedSuggestion(mock *testutil.MockAPIServer, candidate bool) {
	detail := suggestionDetail(stagedSuggestionID, api.SuggestionActionUpdate, "Keyword Rule (1)")
	if candidate {
		detail.MigrationModelID = nil
		detail.IsRevalidationCandidate = &candidate
	}
	registerSuggestionDetail(mock, detail)
}

// A 200 carrying dataset_rule_suggestion_id leaves the alert open: the outcome
// names the suggestion, the step that decides it for its kind, and the re-run
// of the plain action — and the command still exits 0.
func TestResolveReportsAStagedKeywordChange(t *testing.T) {
	cases := []struct {
		name      string
		register  func(*testutil.MockAPIServer)
		want      []string
		forbidden []string
	}{
		{
			name:     "draft-migration suggestion",
			register: func(m *testutil.MockAPIServer) { registerStagedSuggestion(m, false) },
			want: []string{
				"qctl suggestion accept " + stagedSuggestionID + " --table-id 42",
				"Accepting it also resolves the alert when the keyword is the alert's own value",
			},
			forbidden: []string{"revalidation candidate"},
		},
		{
			name:     "revalidation candidate",
			register: func(m *testutil.MockAPIServer) { registerStagedSuggestion(m, true) },
			want: []string{
				"revalidation candidate",
				"It cannot be accepted",
				"qctl describe rule-suggestion " + stagedSuggestionID + " --table-id 42",
			},
			forbidden: []string{"qctl suggestion accept"},
		},
		{
			name: "suggestion read fails",
			register: func(m *testutil.MockAPIServer) {
				m.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions/"+stagedSuggestionID,
					func(w http.ResponseWriter, _ *http.Request) {
						testutil.RespondJSON(w, http.StatusInternalServerError, map[string]string{"detail": "boom"})
					})
			},
			want: []string{
				"could not read the suggestion",
				"qctl describe rule-suggestion " + stagedSuggestionID + " --table-id 42",
			},
			forbidden: []string{"qctl suggestion accept"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerOpenGetAlertID(mock, keywordAlert())
			registerStagingResolve(mock)
			tc.register(mock)

			out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "validation_add_a_new_keyword")
			if err != nil {
				t.Fatalf("resolve: %v\n%s", err, out)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitSuccess)
			}
			want := append([]string{
				"alert 9 — keyword change staged as rule suggestion " + stagedSuggestionID + "; the alert stays open",
				"qctl describe alert 9",
				"qctl alert resolve 9 --action validation_add_a_new_keyword",
			}, tc.want...)
			for _, s := range want {
				if !strings.Contains(out, s) {
					t.Errorf("output missing %q:\n%s", s, out)
				}
			}
			for _, s := range append(tc.forbidden, "still unresolved") {
				if strings.Contains(out, s) {
					t.Errorf("output must not contain %q:\n%s", s, out)
				}
			}
		})
	}
}

// An alert the re-read finds resolved is reported resolved, still naming the
// staged suggestion and without the re-run advice.
func TestResolveReportsAStagedKeywordChangeOnAResolvedAlert(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlertID(mock, 9, keywordAlert())
	registerStagingResolve(mock)
	registerStagedSuggestion(mock, false)

	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "validation_add_a_new_keyword")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if !strings.Contains(out, "alert 9 resolved; its keyword change was staged as rule suggestion "+stagedSuggestionID) {
		t.Errorf("output missing the resolved headline:\n%s", out)
	}
	if strings.Contains(out, "resolve it again") {
		t.Errorf("a resolved alert needs no re-run:\n%s", out)
	}
}

// -o json carries dataset_rule_suggestion_id when the resolve staged a keyword
// change, and omits the key when it staged none.
func TestResolveResultCarriesTheStagedSuggestionID(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerOpenGetAlertID(mock, keywordAlert())
	registerStagingResolve(mock)
	registerStagedSuggestion(mock, false)

	stdout, stderr, err := runAlertCommandSplit(t,
		"alert", "resolve", "9", "--action", "validation_add_a_new_keyword", "-o", "json")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "keyword change staged") {
		t.Errorf("stderr missing the staged outcome:\n%s", stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got["dataset_rule_suggestion_id"] != stagedSuggestionID || got["resolved"] != false {
		t.Errorf("document = %v, want dataset_rule_suggestion_id %s and resolved false", got, stagedSuggestionID)
	}

	mock = newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	registerResolveWithUploadFields(mock, 9)
	stdout, stderr, err = runAlertCommandSplit(t,
		"alert", "resolve", "9", "--action", "discard_migration", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, stderr)
	}
	got = nil
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if _, ok := got["dataset_rule_suggestion_id"]; ok {
		t.Errorf("document = %v, want no dataset_rule_suggestion_id when nothing was staged", got)
	}
}

// A file of several documents reports the staged suggestion on the entry that
// staged it and on no other, and its outcome line names the re-send.
func TestResolveFileReportsTheStagedSuggestionPerEntry(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerOpenGetAlertID(mock, keywordAlert())
	registerStagingResolve(mock)
	registerStagedSuggestion(mock, false)
	registerResolvingGetAlertID(mock, 10, mapValueAlertWithID(10))
	registerResolveWithUploadFields(mock, 10)
	path := writeManifest(t,
		"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: validation_add_a_new_keyword\n"+
			"---\n"+
			"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 10\nspec:\n  action: map_value\n  new_value: \"1420.00\"\n")

	stdout, stderr, err := runAlertCommandSplit(t, "alert", "resolve", "-f", path, "-o", "json")
	if err != nil {
		t.Fatalf("resolve -f: %v\n%s", err, stderr)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitSuccess)
	}
	for _, s := range []string{
		"document 1: alert 9 — keyword change staged as rule suggestion " + stagedSuggestionID,
		"qctl alert resolve -f <file>   # the same document",
		"document 2: alert 10 resolved",
	} {
		if !strings.Contains(stderr, s) {
			t.Errorf("stderr missing %q:\n%s", s, stderr)
		}
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if len(list.Items) != 2 {
		t.Fatalf("items = %v, want two", list.Items)
	}
	if list.Items[0]["dataset_rule_suggestion_id"] != stagedSuggestionID {
		t.Errorf("item 0 = %v, want dataset_rule_suggestion_id %s", list.Items[0], stagedSuggestionID)
	}
	if _, ok := list.Items[1]["dataset_rule_suggestion_id"]; ok {
		t.Errorf("item 1 = %v, want no dataset_rule_suggestion_id", list.Items[1])
	}
}

// A compound keyword action refused with a code-less 409 keeps its exit code
// and names the two steps: the keyword on its own, then the compound action.
func TestResolveExplainsTheCompoundKeywordRefusal(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerOpenGetAlertID(mock, keywordAlert())
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
				"msg":        "The new keyword has to be validated or reviewed before it reaches the rule, so it cannot be added together with a value fix.",
				"error_code": nil,
			}})
		})
	path := writeManifest(t,
		"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: validation_add_a_new_keyword__map_value\n  new_value: shipped\n")

	_, err := runAlertCommand(t, "alert", "resolve", "-f", path)
	if err == nil {
		t.Fatal("want the 409")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	for _, s := range []string{
		"qctl alert resolve 9 --action validation_add_a_new_keyword --dry-run > keyword.yaml",
		"qctl alert resolve -f keyword.yaml",
		"Then send validation_add_a_new_keyword__map_value again",
	} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error missing %q:\n%v", s, err)
		}
	}
}

// The same 409 on a plain keyword action, or one that carries an error code,
// is not the compound refusal and gets no two-step advice.
func TestResolveLeavesOtherConflictsAlone(t *testing.T) {
	for _, tc := range []struct {
		name, action, body string
	}{
		{"plain action", "validation_add_a_new_keyword", `{"detail":{"msg":"conflict"}}`},
		{"coded conflict", "validation_add_a_new_keyword__map_value", `{"detail":{"msg":"locked","error_code":"PERIOD_LOCKED"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerOpenGetAlertID(mock, keywordAlert())
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusConflict, json.RawMessage(tc.body))
				})
			path := writeManifest(t, fmt.Sprintf(
				"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: %s\n  new_value: shipped\n", tc.action))
			_, err := runAlertCommand(t, "alert", "resolve", "-f", path)
			if err == nil {
				t.Fatal("want the 409")
			}
			if strings.Contains(err.Error(), "keyword.yaml") {
				t.Errorf("error carries the compound advice:\n%v", err)
			}
		})
	}
}
