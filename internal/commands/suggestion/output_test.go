package suggestion

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// runSuggestionSplit keeps stdout and stderr apart: under -o json|yaml|name
// stdout must hold the document alone.
func runSuggestionSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupSuggestionTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// One id prints the accepted suggestion, whose created_dataset_rule_id is the
// rule a create made — the id an agent addresses next.
func TestSuggestionAcceptJSONPrintsTheAcceptedSuggestion(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	ruleID := uuid.MustParse("7e7e0000-1111-2222-3333-444444444444")
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/accept",
		func(w http.ResponseWriter, r *http.Request) {
			s := sampleResolvedSuggestion(api.SuggestionStateApplied)
			s.Action = api.SuggestionActionCreate
			s.CreatedDatasetRuleID = &ruleID
			testutil.RespondJSON(w, http.StatusOK, s)
		})

	stdout, stderr, err := runSuggestionSplit(t, "suggestion", "accept", testSuggestionUUID.String(),
		"--table-id", "8", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		ID                   string `json:"id"`
		CreatedDatasetRuleID string `json:"created_dataset_rule_id"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.ID != testSuggestionUUID.String() || got.CreatedDatasetRuleID != ruleID.String() {
		t.Errorf("suggestion = %+v, want %s creating %s", got, testSuggestionUUID, ruleID)
	}
	if !strings.Contains(stderr, `accepted — tablerule "high_confidence_dedupe" created`) {
		t.Errorf("stderr = %q, want the result line", stderr)
	}
}

// A partial batch still prints its outcome, then exits 1; the refusals are in
// the document and, as prose, on stderr.
func TestSuggestionAcceptJSONPartialBatchPrintsTheOutcome(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	registerPending(mock, testSuggestionUUID.String())
	registerPending(mock, sugID2)
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{
			Processed: 1, Skipped: 1, Errors: []string{sugID2 + ": rule is disabled"},
		})
	})

	stdout, stderr, err := runSuggestionSplit(t, "suggestion", "accept",
		testSuggestionUUID.String(), sugID2, "--table-id", "8", "-o", "json")
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d (%v), want 1", got, err)
	}
	var got bulkOutcome
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.Processed != 1 || got.Skipped != 1 || len(got.Errors) != 1 || got.Missing != 0 {
		t.Errorf("outcome = %+v, want 1 processed, 1 skipped with its reason, 0 missing", got)
	}
	if !strings.Contains(stderr, "refused  "+sugID2) {
		t.Errorf("stderr = %q, want the refusal", stderr)
	}
}

// The id route's reconcile reaches the document: ids the write did not account
// for are counted as missing.
func TestSuggestionAcceptYAMLCountsMissingIDs(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	registerPending(mock, testSuggestionUUID.String())
	registerPending(mock, sugID2)
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
	})

	stdout, _, err := runSuggestionSplit(t, "suggestion", "accept",
		testSuggestionUUID.String(), sugID2, "--table-id", "8", "-o", "yaml")
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d (%v), want 1", got, err)
	}
	want := "errors: []\nmissing: 2\nprocessed: 0\nskipped: 0\n"
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
}

// The filter route's preview is prose: under -o json it goes to stderr with
// the result line, and stdout is the outcome alone.
func TestSuggestionAcceptFilterPreviewGoesToStderrUnderJSON(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{tinyRow(sugID2, api.SuggestionActionUpdate, api.Profiling)},
		})
	})
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Processed: 1, Errors: []string{}})
	})

	stdout, stderr, err := runSuggestionSplit(t, "suggestion", "accept",
		"--source", "profiling", "--table-id", "8", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got bulkOutcome
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.Processed != 1 {
		t.Errorf("outcome = %+v, want 1 processed", got)
	}
	for _, want := range []string{sugID2, "accepted 1 suggestion\n"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// A selection that matches nothing writes nothing and still prints a document,
// so stdout is never empty at exit 0.
func TestSuggestionAcceptFilterZeroMatchesPrintsAZeroOutcome(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("GET", listURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
			Results: []api.DatasetRuleSuggestionTinySchema{},
		})
	})

	stdout, stderr, err := runSuggestionSplit(t, "suggestion", "accept",
		"--source", "periodic", "--table-id", "8", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("a filter matching nothing is not an error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got["processed"] != float64(0) || got["missing"] != float64(0) {
		t.Errorf("outcome = %v, want zero counts", got)
	}
	if errs, ok := got["errors"].([]interface{}); !ok || len(errs) != 0 {
		t.Errorf("errors = %v, want an empty list, not null", got["errors"])
	}
	if !strings.Contains(stderr, "nothing to accept") {
		t.Errorf("stderr = %q, want the nothing-matched line", stderr)
	}
}

// accept has no -o name (a batch names no single object), and dismiss declares
// table alone.
func TestSuggestionAcceptRefusesNameAndDismissRefusesJSON(t *testing.T) {
	newSuggestionTestEnv(t)
	for _, args := range [][]string{
		{"accept", testSuggestionUUID.String(), "--table-id", "8", "-o", "name"},
		{"dismiss", testSuggestionUUID.String(), "--table-id", "8", "-o", "json"},
	} {
		_, _, err := runSuggestionSplit(t, append([]string{"suggestion"}, args...)...)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage ||
			!strings.Contains(err.Error(), "unknown output format") {
			t.Errorf("suggestion %s: exit %d (%v), want the format refusal", strings.Join(args, " "), code, err)
		}
	}
}

func TestSuggestionEditJSONAndNamePrintTheSuggestion(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	registerPending(mock, testSuggestionUUID.String())
	mock.RegisterHandler("PATCH", editURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStatePending))
	})

	stdout, stderr, err := runSuggestionSplit(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--severity", "warning", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.ID != testSuggestionUUID.String() || got.State != "pending" {
		t.Errorf("suggestion = %+v, want the pending %s", got, testSuggestionUUID)
	}
	if !strings.Contains(stderr, "Next: qctl suggestion accept "+testSuggestionUUID.String()) {
		t.Errorf("stderr = %q, want the Next line", stderr)
	}

	stdout, _, err = runSuggestionSplit(t, "suggestion", "edit", testSuggestionUUID.String(),
		"--table-id", "8", "--severity", "warning", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "rule-suggestion/" + testSuggestionUUID.String() + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}
