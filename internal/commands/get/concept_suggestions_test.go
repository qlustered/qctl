package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const standardFieldSuggestionsRoute = "/api/orgs/" + testOrgID + "/datasets/42/column-concept-suggestions"

func newTableSuggestionEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "test-token")
	return mock
}

func runTableSuggestion(t *testing.T, sub *cobra.Command, args ...string) (string, string, error) {
	t.Helper()
	root := setupTestCommandForTable(sub)
	root.SilenceUsage = true
	root.SilenceErrors = true
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"get", sub.Name()}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestGetStandardFieldSuggestionsShowsUnavailableAndRawResponse(t *testing.T) {
	mock := newTableSuggestionEnv(t)
	mock.RegisterHandler("GET", standardFieldSuggestionsRoute, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"columns":[{"dataset_col":"policy_no","suggestions":[{"concept":{"id":"550e8400-e29b-41d4-a716-446655440000","slug":"policy-number","name":"Policy Number","description":null},"source":"org_name","matched_text":"Policy Number","similarity":null,"table_count":2}]}],"similarity_unavailable":true}`))
	})
	out, stderr, err := runTableSuggestion(t, NewStandardFieldSuggestionsCommand(), "42")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"policy_no", "policy-number", "org_name", "Policy Number", "2"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q: %s", want, out)
		}
	}
	if !strings.Contains(stderr, "Similarity suggestions unavailable") {
		t.Errorf("missing warning: %s", stderr)
	}
	out, stderr, err = runTableSuggestion(t, NewStandardFieldSuggestionsCommand(), "42", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		SimilarityUnavailable bool `json:"similarity_unavailable"`
		Columns               []struct {
			Suggestions []struct {
				Concept struct {
					Slug string `json:"slug"`
				} `json:"concept"`
			} `json:"suggestions"`
		} `json:"columns"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	if !raw.SimilarityUnavailable || raw.Columns[0].Suggestions[0].Concept.Slug != "policy-number" {
		t.Errorf("raw response changed: %s", out)
	}
	if strings.Contains(stderr, "Similarity suggestions unavailable") {
		t.Errorf("structured output warning should be carried in the object: %s", stderr)
	}
}

// -o name is a documented exception to kind/<id>: one <column>/<standard field slug>
// per suggestion, sanitized like every other human line.
func TestGetStandardFieldSuggestionsNamePrintsColumnAndSlug(t *testing.T) {
	mock := newTableSuggestionEnv(t)
	mock.RegisterHandler("GET", standardFieldSuggestionsRoute, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"columns":[`+
			`{"dataset_col":"policy_no","suggestions":[{"concept":{"id":"550e8400-e29b-41d4-a716-446655440000","slug":"policy-number","name":"Policy Number"},"source":"org_name","table_count":2}]},`+
			`{"dataset_col":"bad\u001b[31m","suggestions":[{"concept":{"id":"550e8400-e29b-41d4-a716-446655440001","slug":"vin","name":"VIN"},"source":"org_name","table_count":1}]}]}`))
	})
	out, _, err := runTableSuggestion(t, NewStandardFieldSuggestionsCommand(), "42", "-o", "name")
	if err != nil {
		t.Fatal(err)
	}
	if want := "policy_no/policy-number\nbad\\x1b[31m/vin\n"; out != want {
		t.Errorf("-o name = %q, want %q", out, want)
	}
}

func TestGetStandardFieldSuggestionsEmptyTable(t *testing.T) {
	mock := newTableSuggestionEnv(t)
	mock.RegisterHandler("GET", standardFieldSuggestionsRoute, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"columns":[]}`))
	})
	out, _, err := runTableSuggestion(t, NewStandardFieldSuggestionsCommand(), "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if columns, ok := result["columns"].([]any); !ok || len(columns) != 0 {
		t.Errorf("empty table should retain columns: [], got %s", out)
	}
}

func TestGetStandardFieldSuggestionsRetries504(t *testing.T) {
	mock := newTableSuggestionEnv(t)
	attempts := 0
	mock.RegisterHandler("GET", standardFieldSuggestionsRoute, func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			testutil.RespondJSON(w, http.StatusGatewayTimeout, json.RawMessage(`{"detail":{"msg":"request timed out","error_code":"ERR_TIMEOUT"}}`))
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"columns":[]}`))
	})
	client.SetRequestPolicy(client.RequestPolicy{Retries: 1})
	t.Cleanup(client.ResetRequestPolicy)
	_, _, err := runTableSuggestion(t, NewStandardFieldSuggestionsCommand(), "42", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestGetStandardFieldSuggestionsRequiresTable(t *testing.T) {
	newTableSuggestionEnv(t)
	_, _, err := runTableSuggestion(t, NewStandardFieldSuggestionsCommand())
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("missing table exits %d: %v", qerrors.GetExitCode(err), err)
	}
}

// TestStandardFieldSuggestionsAliasWorksWithConceptSuggestions verifies that
// the "concept-suggestions" alias works the same as "standard-field-suggestions".
func TestStandardFieldSuggestionsAliasWorksWithConceptSuggestions(t *testing.T) {
	mock := newTableSuggestionEnv(t)
	mock.RegisterHandler("GET", standardFieldSuggestionsRoute, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"columns":[{"dataset_col":"policy_no","suggestions":[{"concept":{"id":"550e8400-e29b-41d4-a716-446655440000","slug":"policy-number","name":"Policy Number","description":null},"source":"org_name","matched_text":"Policy Number","similarity":null,"table_count":2}]}]}`))
	})

	// Test with standard-field-suggestions command
	cmd1 := setupTestCommandForTable(NewStandardFieldSuggestionsCommand())
	var out1, err1Out bytes.Buffer
	cmd1.SetOut(&out1)
	cmd1.SetErr(&err1Out)
	cmd1.SetArgs([]string{"get", "standard-field-suggestions", "42"})
	if err := cmd1.Execute(); err != nil {
		t.Fatalf("standard-field-suggestions: %v", err)
	}

	// Test with concept-suggestions alias
	cmd2 := setupTestCommandForTable(NewStandardFieldSuggestionsCommand())
	var out2, err2Out bytes.Buffer
	cmd2.SetOut(&out2)
	cmd2.SetErr(&err2Out)
	cmd2.SetArgs([]string{"get", "concept-suggestions", "42"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("concept-suggestions alias: %v", err)
	}

	// Both should produce the same output
	if out1.String() != out2.String() {
		t.Errorf("standard-field-suggestions and concept-suggestions produced different output:\nstandard-field-suggestions:\n%s\nconcept-suggestions:\n%s", out1.String(), out2.String())
	}
}
