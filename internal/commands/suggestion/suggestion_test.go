package suggestion

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var testSuggestionUUID = uuid.MustParse("3f2a0000-1111-2222-3333-444444444444")

func setupSuggestionTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	// Mirrors root.go: an -o value a verb does not declare exits 2.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

func newSuggestionTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func sampleResolvedSuggestion(state api.SuggestionState) api.DatasetRuleSuggestionSchema {
	return api.DatasetRuleSuggestionSchema{
		ID:             testSuggestionUUID,
		Action:         api.SuggestionActionUpdate,
		State:          state,
		Source:         api.User,
		DatasetID:      8,
		InstanceName:   "high_confidence_dedupe",
		OrganizationID: uuid.MustParse(testOrgID),
		RuleFamilyID:   uuid.MustParse("ffff0000-1111-2222-3333-444444444444"),
		CreatedAt:      time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
	}
}

func runSuggestionCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupSuggestionTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestSuggestionAccept(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	called := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/accept",
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStateApplied))
		})

	out, err := runSuggestionCommand(t, "suggestion", "accept", testSuggestionUUID.String(), "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("accept endpoint was not called")
	}
	want := "suggestion " + testSuggestionUUID.String() + ` accepted — tablerule "high_confidence_dedupe" updated`
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
}

func TestSuggestionDismissSendsFamilyFlag(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	var rawBody []byte
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/dismiss",
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStateDismissed))
		})

	out, err := runSuggestionCommand(t, "suggestion", "dismiss", testSuggestionUUID.String(), "--table-id", "8", "--family")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		t.Fatalf("dismiss body not JSON: %v (%s)", err, rawBody)
	}
	if body["dismiss_family"] != true {
		t.Errorf("--family must send dismiss_family=true, body: %s", rawBody)
	}
	want := "suggestion " + testSuggestionUUID.String() + " dismissed"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
}

func TestSuggestionDismissWithoutFamilyOmitsKey(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	var rawBody []byte
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/dismiss",
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStateDismissed))
		})

	if _, err := runSuggestionCommand(t, "suggestion", "dismiss", testSuggestionUUID.String(), "--table-id", "8"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(rawBody)); got != "{}" {
		t.Errorf("dismiss body without --family must be {}, got: %s", got)
	}
}

func TestSuggestionAccept422APIErrorPassthrough(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/accept",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "suggestion already resolved",
					"error_code": "SUGGESTION_ALREADY_RESOLVED",
				},
			})
		})

	_, err := runSuggestionCommand(t, "suggestion", "accept", testSuggestionUUID.String(), "--table-id", "8")
	if err == nil {
		t.Fatal("expected 422 error")
	}
	var apiErr *rule_suggestions.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *rule_suggestions.APIError in chain, got: %v", err)
	}
	if apiErr.Code != "SUGGESTION_ALREADY_RESOLVED" {
		t.Errorf("Code = %q, want SUGGESTION_ALREADY_RESOLVED", apiErr.Code)
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
}

// registerDatasetsByName serves the table list that --table NAME resolves
// through, recording the name it was asked for.
func registerDatasetsByName(mock *testutil.MockAPIServer, lookedUp *string) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		*lookedUp = r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{{"id": 8, "name": "42"}},
		})
	})
}

func TestSuggestionAcceptTableIDFlag(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	called := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/accept",
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStateApplied))
		})

	if _, err := runSuggestionCommand(t, "suggestion", "accept", testSuggestionUUID.String(), "--table-id", "8"); err != nil {
		t.Fatalf("--table-id must select the table by ID: %v", err)
	}
	if !called {
		t.Error("accept endpoint for table 8 was not called")
	}
}

func TestSuggestionAcceptTableFlagIsAName(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	var lookedUp string
	registerDatasetsByName(mock, &lookedUp)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/accept",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStateApplied))
		})

	// A table named "42" is the case one overloaded flag can never serve.
	if _, err := runSuggestionCommand(t, "suggestion", "accept", testSuggestionUUID.String(), "--table", "42"); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
}

func TestSuggestionDismissTableIDFlag(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	called := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/dismiss",
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStateDismissed))
		})

	if _, err := runSuggestionCommand(t, "suggestion", "dismiss", testSuggestionUUID.String(), "--table-id", "8"); err != nil {
		t.Fatalf("--table-id must select the table by ID: %v", err)
	}
	if !called {
		t.Error("dismiss endpoint for table 8 was not called")
	}
}

func TestSuggestionDismissTableFlagIsAName(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	var lookedUp string
	registerDatasetsByName(mock, &lookedUp)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/dismiss",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStateDismissed))
		})

	// A table named "42" is the case one overloaded flag can never serve.
	if _, err := runSuggestionCommand(t, "suggestion", "dismiss", testSuggestionUUID.String(), "--table", "42"); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
}

func TestSuggestionDismissRequiresTable(t *testing.T) {
	newSuggestionTestEnv(t)
	_, err := runSuggestionCommand(t, "suggestion", "dismiss", testSuggestionUUID.String())
	if err == nil || !strings.Contains(err.Error(), "table is required") {
		t.Fatalf("expected table-required error, got: %v", err)
	}
	// <suggestion-id> is a UUID here, not a table.
	if strings.Contains(err.Error(), "positional") {
		t.Errorf("this command's positional slot is not a table; the error must not offer one: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestSuggestionAcceptRequiresTable(t *testing.T) {
	newSuggestionTestEnv(t)
	_, err := runSuggestionCommand(t, "suggestion", "accept", testSuggestionUUID.String())
	if err == nil || !strings.Contains(err.Error(), "table is required") {
		t.Fatalf("expected table-required error, got: %v", err)
	}
	// <suggestion-id> is a UUID here, not a table.
	if strings.Contains(err.Error(), "positional") {
		t.Errorf("this command's positional slot is not a table; the error must not offer one: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

func TestSuggestionAcceptRequiresTableBeforeCredentialsAreRead(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)

	_, err := runSuggestionCommand(t, "suggestion", "accept", testSuggestionUUID.String())
	if err == nil || !strings.Contains(err.Error(), "table is required") {
		t.Fatalf("missing table with no credential = %v, want table-required error", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}

	_, control := runSuggestionCommand(t, "suggestion", "accept", testSuggestionUUID.String(), "--table-id", "8")
	if control == nil || !strings.Contains(control.Error(), "not logged in") {
		t.Fatalf("control with a valid selector = %v, want not-logged-in error", control)
	}
	if got := qerrors.GetExitCode(control); got == qerrors.ExitBadUsage {
		t.Fatalf("control exit code = %d (ExitBadUsage); credential-free environment was not proved", got)
	}
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("made %d request(s) without a credential: %v", len(reqs), reqs)
	}
}

const (
	sugID2         = "3f2a0000-1111-2222-3333-555555555555"
	sugCandidateID = "3f2a0000-1111-2222-3333-666666666666"
	bulkAcceptURL  = "/api/orgs/" + testOrgID + "/datasets/8/rule-suggestions/bulk-accept"
	bulkDismissURL = "/api/orgs/" + testOrgID + "/datasets/8/rule-suggestions/bulk-dismiss"
)

// registerPending answers the pre-flight's per-id detail read.
func registerPending(mock *testutil.MockAPIServer, id string) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+id,
		func(w http.ResponseWriter, r *http.Request) {
			s := sampleResolvedSuggestion(api.SuggestionStatePending)
			s.ID = uuid.MustParse(id)
			testutil.RespondJSON(w, http.StatusOK, s)
		})
}

func TestSuggestionAcceptTwoIDsUsesBulk(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	registerPending(mock, testSuggestionUUID.String())
	registerPending(mock, sugID2)
	var raw []byte
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		testutil.RespondJSON(w, http.StatusOK,
			api.BulkSuggestionResultSchema{Processed: 2, Skipped: 0, Errors: []string{}})
	})

	out, err := runSuggestionCommand(t, "suggestion", "accept",
		testSuggestionUUID.String(), sugID2, "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("bulk-accept was not called")
	}
	var body struct {
		SuggestionIDs []string `json:"suggestion_ids"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("bulk-accept body not JSON: %v (%s)", err, raw)
	}
	if len(body.SuggestionIDs) != 2 {
		t.Errorf("both ids must reach the bulk route, body: %s", raw)
	}
	// A clean run prints the bare form: the "of <M>" denominator earns its
	// place only when the two numbers differ (see renderBulkResult).
	if !strings.Contains(out, "accepted 2 suggestions") {
		t.Errorf("output = %q", out)
	}
}

func TestSuggestionAcceptRefusesNonPendingBeforeWriting(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	registerPending(mock, testSuggestionUUID.String())
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+sugID2,
		func(w http.ResponseWriter, r *http.Request) {
			s := sampleResolvedSuggestion(api.SuggestionStateDismissed)
			s.ID = uuid.MustParse(sugID2)
			testutil.RespondJSON(w, http.StatusOK, s)
		})
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
	})

	_, err := runSuggestionCommand(t, "suggestion", "accept",
		testSuggestionUUID.String(), sugID2, "--table-id", "8")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if bulkCalled {
		t.Error("nothing may be written when the pre-flight refuses")
	}
}

func TestSuggestionAcceptSingleIDStillUsesSingleRoute(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+testSuggestionUUID.String()+"/accept",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleResolvedSuggestion(api.SuggestionStateApplied))
		})

	out, err := runSuggestionCommand(t, "suggestion", "accept", testSuggestionUUID.String(), "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bulkCalled {
		t.Error("one id must not reach the bulk route")
	}
	// The exact line the command printed before this branch existed.
	want := "suggestion " + testSuggestionUUID.String() + ` accepted — tablerule "high_confidence_dedupe" updated`
	if !strings.Contains(out, want) {
		t.Errorf("single-id output changed\n got: %q\nwant: %q", out, want)
	}
}

func TestSuggestionAcceptPartialExitsOne(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	registerPending(mock, testSuggestionUUID.String())
	registerPending(mock, sugID2)
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{
			Processed: 1, Skipped: 1, Errors: []string{sugID2 + ": rule is disabled"},
		})
	})

	out, err := runSuggestionCommand(t, "suggestion", "accept",
		testSuggestionUUID.String(), sugID2, "--table-id", "8")
	if err == nil {
		t.Fatal("a partial must not exit 0")
	}
	// Without this the test is unfalsifiable: cobra's own arg refusal also
	// exits 1, so it would pass against a command that never reached the route.
	if !bulkCalled {
		t.Fatalf("bulk-accept was not called; the failure came from somewhere else: %v", err)
	}
	if !strings.Contains(out, "refused  "+sugID2+": rule is disabled") {
		t.Errorf("the server's reason must be shown\n%s", out)
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

// TestSuggestionAcceptIDRouteReportsWhatTheWriteMissed pins the one thing that
// tells renderBulkResult which route called it: sentIDs is nil ONLY on the
// filter route. An empty result body is a clean "nothing matched" there, but on
// the id route it means every id the caller named stopped being pending between
// the pre-flight and the write — a nonzero exit, not a silent success. Nothing
// but the id list's non-nilness separates the two, so the real command path is
// where that has to be proven.
func TestSuggestionAcceptIDRouteReportsWhatTheWriteMissed(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	registerPending(mock, testSuggestionUUID.String())
	registerPending(mock, sugID2)
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK,
			api.BulkSuggestionResultSchema{Processed: 0, Skipped: 0, Errors: []string{}})
	})

	out, err := runSuggestionCommand(t, "suggestion", "accept",
		testSuggestionUUID.String(), sugID2, "--table-id", "8")
	if err == nil {
		t.Fatal("a write that touched none of the named ids must not exit 0")
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if strings.Contains(out, "no pending suggestions matched") {
		t.Errorf("that is the filter route's line — the id route lost its id list\n%s", out)
	}
	if !strings.Contains(out, "accepted 0 of 2 suggestions") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "no longer pending when the write ran") {
		t.Errorf("the two missing ids must be named as missing\n%s", out)
	}
}

// TestSuggestionAcceptFilterVocabulariesAreNotSwapped runs the vocabulary check
// through the real command, which selector_test.go's tripwire cannot: it calls
// oneOf directly, so it would still pass if parseSelection handed --source the
// ACTIONS list. Feeding each flag the other's values is what catches that.
func TestSuggestionAcceptFilterVocabulariesAreNotSwapped(t *testing.T) {
	newSuggestionTestEnv(t)
	tests := []struct {
		flag, value, wantVocabulary string
	}{
		{"--source", "create", strings.Join(validSources, ", ")},
		{"--action", "profiling", strings.Join(validActions, ", ")},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			_, err := runSuggestionCommand(t, "suggestion", "accept", tt.flag, tt.value, "--table-id", "8")
			if err == nil {
				t.Fatalf("%s %s must be refused: it belongs to the other flag's vocabulary", tt.flag, tt.value)
			}
			if !strings.Contains(err.Error(), "invalid "+tt.flag) {
				t.Errorf("refusal must name the flag, got %q", err.Error())
			}
			if !strings.Contains(err.Error(), tt.wantVocabulary) {
				t.Errorf("refusal %q does not name %s's own vocabulary %q",
					err.Error(), tt.flag, tt.wantVocabulary)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// TestSuggestionAcceptRefusesRevalidationCandidate is the command-layer guard
// on the verb string. preflight applies the revalidation-candidate refusal only
// when the verb equals verbAccept, so an accept command that passed any other
// spelling would skip the one check whose job is to stop a write, and would
// then write. The candidate itself would still be refused — server-side, by
// _apply_accept's 409 (preflight.go preflight) — so what the skip actually costs
// is atomicity: the OTHER ids in the batch get committed first. That is why this
// test asserts bulkCalled is false and not merely that the run exited nonzero.
// Only a candidate reaching the bulk route can show it.
func TestSuggestionAcceptRefusesRevalidationCandidate(t *testing.T) {
	mock := newSuggestionTestEnv(t)
	registerPending(mock, testSuggestionUUID.String())
	candidate := true
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+sugID2,
		func(w http.ResponseWriter, r *http.Request) {
			s := sampleResolvedSuggestion(api.SuggestionStatePending)
			s.ID = uuid.MustParse(sugID2)
			s.IsRevalidationCandidate = &candidate
			testutil.RespondJSON(w, http.StatusOK, s)
		})
	bulkCalled := false
	mock.RegisterHandler("POST", bulkAcceptURL, func(w http.ResponseWriter, r *http.Request) {
		bulkCalled = true
		testutil.RespondJSON(w, http.StatusOK, api.BulkSuggestionResultSchema{Errors: []string{}})
	})

	_, err := runSuggestionCommand(t, "suggestion", "accept",
		testSuggestionUUID.String(), sugID2, "--table-id", "8")
	if err == nil {
		t.Fatal("a revalidation candidate must be refused by the pre-flight")
	}
	if bulkCalled {
		t.Error("nothing may be written when the pre-flight refuses")
	}
	if !strings.Contains(err.Error(), "revalidation candidate") {
		t.Errorf("refusal must say why, got %q", err.Error())
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}
