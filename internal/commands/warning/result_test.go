package warning

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/warnings"
)

// resolve --action writes {id, action, resolved, dataset_id} under -o json and
// warning/<id> under -o name; the prose moves to stderr.
func TestResolveWritesTheResultDocument(t *testing.T) {
	for _, format := range []string{"json", "name"} {
		mock := newWarningTestEnv(t)
		registerResolvingGetWarning(mock, anomalyWarning(api.NotAnomaly))
		registerResolvePost(mock, nil)

		stdout, stderr, err := runWarningCommandSplit(t,
			"warning", "resolve", "55", "--action", "not_anomaly", "-o", format)
		if err != nil {
			t.Fatalf("-o %s: %v\n%s", format, err, stderr)
		}
		if !strings.Contains(stderr, "warning 55 resolved") {
			t.Errorf("-o %s: stderr missing the outcome line:\n%s", format, stderr)
		}
		if format == "name" {
			if stdout != "warning/55\n" {
				t.Errorf("-o name stdout = %q, want %q", stdout, "warning/55\n")
			}
			continue
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
		}
		want := map[string]any{"id": float64(55), "action": "not_anomaly", "resolved": true, "dataset_id": float64(42)}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %v, want %v (document %v)", k, got[k], v, got)
			}
		}
	}
}

// A file of several documents prints one List; the runner's per-document
// lines go to stderr.
func TestResolveFileWritesOneListForSeveralDocuments(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.NotAnomaly))
	registerResolvePost(mock, nil)
	doc := "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: not_anomaly\n"
	path := writeWarningManifest(t, doc+"---\n"+doc)

	stdout, stderr, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "-o", "json")
	if err != nil {
		t.Fatalf("resolve -f: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "document 2: warning 55") {
		t.Errorf("stderr missing the runner's lines:\n%s", stderr)
	}
	var list struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if list.Kind != "List" || len(list.Items) != 2 || list.Items[0]["resolved"] != false {
		t.Errorf("stdout = %+v, want a List of two unresolved documents", list)
	}
}

// --dry-run writes a YAML manifest: -o yaml keeps it on stdout, and -o json or
// name is refused.
func TestResolveDryRunFormats(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.MapValue))

	stdout, _, err := runWarningCommandSplit(t,
		"warning", "resolve", "55", "--action", "map_value", "--dry-run", "-o", "yaml")
	if err != nil {
		t.Fatalf("--dry-run -o yaml: %v", err)
	}
	if !strings.Contains(stdout, "kind: WarningResolution") {
		t.Errorf("--dry-run -o yaml stdout = %q, want the manifest", stdout)
	}
	for _, format := range []string{"json", "name"} {
		_, _, err := runWarningCommandSplit(t,
			"warning", "resolve", "55", "--action", "map_value", "--dry-run", "-o", format)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("--dry-run -o %s: exit %d (%v), want %d", format, code, err, qerrors.ExitBadUsage)
		}
	}
}

// retract writes the server's {issue_resolved, results} under -o json and
// warning/<id> under -o name.
func TestRetractWritesTheResponse(t *testing.T) {
	for _, format := range []string{"json", "name"} {
		mock := newWarningTestEnv(t)
		registerGetWarning8817(mock, userSubmittedWarning())
		registerWarningRetract(mock, new(map[string]interface{}), api.UserSubmittedIssueRetractResponseSchema{
			IssueResolved: false,
			Results:       []api.UserSubmittedIssueRetractResultSchema{{RowID: 1042, Status: api.Retracted}},
		})

		stdout, stderr, err := runWarningCommandSplit(t,
			"warning", "retract", "8817", "--row", "1042", "--yes", "-o", format)
		if err != nil {
			t.Fatalf("-o %s: %v\n%s", format, err, stderr)
		}
		if !strings.Contains(stderr, "warning/8817 retracted from") {
			t.Errorf("-o %s: stderr missing the outcome line:\n%s", format, stderr)
		}
		if format == "name" {
			if stdout != "warning/8817\n" {
				t.Errorf("-o name stdout = %q, want %q", stdout, "warning/8817\n")
			}
			continue
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
		}
		if got["issue_resolved"] != false {
			t.Errorf("issue_resolved = %v, want false", got["issue_resolved"])
		}
		if results, _ := got["results"].([]any); len(results) != 1 {
			t.Errorf("results = %v, want one row", got["results"])
		}
	}
}

const stagedSuggestionID = "33333333-3333-3333-3333-333333333333"

// keywordWarning is an open invalid_keyword warning 55 on table 42 offering
// the plain keyword action and one compound keyword action.
func keywordWarning() warnings.WarningFull {
	return prefixWarning(api.InvalidKeyword, api.ValidationAddANewKeyword, api.ValidationAddANewKeywordMapValue)
}

// registerStagingWarningResolve answers the resolve POST for id with 200 and
// the staged suggestion's id.
func registerStagingWarningResolve(mock *testutil.MockAPIServer, id int) {
	mock.RegisterHandler("POST", fmt.Sprintf("/api/orgs/%s/warnings/resolve/%d", testOrgID, id),
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
				`{"redirect_post_submit_to":null,"dataset_rule_suggestion_id":"`+stagedSuggestionID+`"}`))
		})
}

// registerStagedWarningSuggestion serves the staged suggestion, a revalidation
// candidate when candidate is set, or a 500 when fail is.
func registerStagedWarningSuggestion(mock *testutil.MockAPIServer, candidate, fail bool) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions/"+stagedSuggestionID,
		func(w http.ResponseWriter, _ *http.Request) {
			if fail {
				testutil.RespondJSON(w, http.StatusInternalServerError, map[string]string{"detail": "boom"})
				return
			}
			m := 3
			detail := api.DatasetRuleSuggestionSchema{
				ID:               uuid.MustParse(stagedSuggestionID),
				Action:           api.SuggestionActionUpdate,
				State:            api.SuggestionStatePending,
				Source:           api.User,
				InstanceName:     "Keyword Rule (1)",
				MigrationModelID: &m,
			}
			if candidate {
				detail.MigrationModelID = nil
				detail.IsRevalidationCandidate = &candidate
			}
			testutil.RespondJSON(w, http.StatusOK, detail)
		})
}

// A 200 carrying dataset_rule_suggestion_id leaves the warning open: the
// outcome names the suggestion, the step that decides it for its kind, and the
// re-run of the plain action, and the command still exits 0. No rows were
// republished, so the republish note is not printed.
func TestResolveReportsAStagedKeywordChange(t *testing.T) {
	cases := []struct {
		name            string
		candidate, fail bool
		want, forbidden []string
	}{
		{
			name:      "draft-migration suggestion",
			want:      []string{"qctl suggestion accept " + stagedSuggestionID + " --table-id 42", "Accepting it also resolves the warning when the keyword is the warning's own value"},
			forbidden: []string{"revalidation candidate"},
		},
		{
			name:      "revalidation candidate",
			candidate: true,
			want: []string{
				"revalidation candidate", "It cannot be accepted",
				"qctl describe rule-suggestion " + stagedSuggestionID + " --table-id 42",
			},
			forbidden: []string{"qctl suggestion accept"},
		},
		{
			name: "suggestion read fails",
			fail: true,
			want: []string{
				"could not read the suggestion",
				"qctl describe rule-suggestion " + stagedSuggestionID + " --table-id 42",
			},
			forbidden: []string{"qctl suggestion accept"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newWarningTestEnv(t)
			registerGetWarning(mock, keywordWarning())
			registerStagingWarningResolve(mock, 55)
			registerStagedWarningSuggestion(mock, tc.candidate, tc.fail)

			out, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "validation_add_a_new_keyword")
			if err != nil {
				t.Fatalf("resolve: %v\n%s", err, out)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitSuccess)
			}
			want := append([]string{
				"warning 55 — keyword change staged as rule suggestion " + stagedSuggestionID + "; the warning stays open",
				"qctl describe warning 55",
				"qctl warning resolve 55 --action validation_add_a_new_keyword",
			}, tc.want...)
			for _, s := range want {
				if !strings.Contains(out, s) {
					t.Errorf("output missing %q:\n%s", s, out)
				}
			}
			for _, s := range append(tc.forbidden, "still unresolved", "republished") {
				if strings.Contains(out, s) {
					t.Errorf("output must not contain %q:\n%s", s, out)
				}
			}
		})
	}
}

// -o json carries dataset_rule_suggestion_id when the resolve staged a keyword
// change, and omits the key when it staged none.
func TestResolveResultCarriesTheStagedSuggestionID(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, keywordWarning())
	registerStagingWarningResolve(mock, 55)
	registerStagedWarningSuggestion(mock, false, false)

	stdout, stderr, err := runWarningCommandSplit(t,
		"warning", "resolve", "55", "--action", "validation_add_a_new_keyword", "-o", "json")
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

	mock = newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.NotAnomaly))
	registerResolvePost(mock, nil)
	stdout, stderr, err = runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "not_anomaly", "-o", "json")
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
// staged it and on no other.
func TestResolveFileReportsTheStagedSuggestionPerEntry(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, keywordWarning())
	registerStagingWarningResolve(mock, 55)
	registerStagedWarningSuggestion(mock, false, false)
	registerResolvingGetWarningID(mock, 56, anomalyWarningWithID(56, api.NotAnomaly))
	registerWarningResolvePostID(mock, 56)
	path := writeWarningManifest(t,
		"apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: validation_add_a_new_keyword\n"+
			"---\n"+
			"apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 56\nspec:\n  action: not_anomaly\n")

	stdout, stderr, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "-o", "json")
	if err != nil {
		t.Fatalf("resolve -f: %v\n%s", err, stderr)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitSuccess)
	}
	for _, s := range []string{
		"document 1: warning 55 — keyword change staged as rule suggestion " + stagedSuggestionID,
		"qctl warning resolve -f <file>   # the same document",
		"document 2: warning 56 resolved",
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
// and names the two steps; the same 409 on the plain action gets no advice.
func TestResolveExplainsTheCompoundKeywordRefusal(t *testing.T) {
	for _, tc := range []struct {
		action string
		advice bool
	}{
		{"validation_add_a_new_keyword__map_value", true},
		{"validation_add_a_new_keyword", false},
	} {
		t.Run(tc.action, func(t *testing.T) {
			mock := newWarningTestEnv(t)
			registerGetWarning(mock, keywordWarning())
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
						"msg":        "The new keyword has to be validated or reviewed before it reaches the rule.",
						"error_code": nil,
					}})
				})
			path := writeWarningManifest(t, fmt.Sprintf(
				"apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: %s\n  new_value: shipped\n", tc.action))

			_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
			if err == nil {
				t.Fatal("want the 409")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			advice := []string{
				"qctl warning resolve 55 --action validation_add_a_new_keyword --dry-run > keyword.yaml",
				"qctl warning resolve -f keyword.yaml",
				"Then send validation_add_a_new_keyword__map_value again",
			}
			for _, s := range advice {
				if got := strings.Contains(err.Error(), s); got != tc.advice {
					t.Errorf("error contains %q = %v, want %v:\n%v", s, got, tc.advice, err)
				}
			}
		})
	}
}
