package get

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

const tableKindSuggestionsRoute = "/api/orgs/" + testOrgID + "/datasets/42/problem-domain-suggestions"

func TestGetTableKindSuggestions(t *testing.T) {
	mock := newTableSuggestionEnv(t)
	mock.RegisterHandler("GET", tableKindSuggestionsRoute, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"problem_domain":{"id":"550e8400-e29b-41d4-a716-446655440000","slug":"car-policy","name":"Car Policy","description":null},"matched_concept_count":1,"domain_concept_count":2,"matched_concepts":[{"id":"6f1c2d3e-4b5a-6789-0abc-def012345678","slug":"policy-number","name":"Policy Number","description":null}]}]}`))
	})
	out, _, err := runTableSuggestion(t, NewTableKindSuggestionsCommand(), "42")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"car-policy", "Car Policy", "policy-number", "1", "2"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q: %s", want, out)
		}
	}
	out, _, err = runTableSuggestion(t, NewTableKindSuggestionsCommand(), "42", "-o", "name")
	if err != nil || out != "table-kind/550e8400-e29b-41d4-a716-446655440000\n" {
		t.Errorf("name output = %q, error = %v", out, err)
	}
	out, _, err = runTableSuggestion(t, NewTableKindSuggestionsCommand(), "42", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["results"]; !ok {
		t.Errorf("raw response lost results: %s", out)
	}
}

func TestTableKindSuggestionsTableHeaders(t *testing.T) {
	mock := newTableSuggestionEnv(t)
	mock.RegisterHandler("GET", tableKindSuggestionsRoute, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"problem_domain":{"id":"550e8400-e29b-41d4-a716-446655440000","slug":"car-policy","name":"Car Policy","description":null},"matched_concept_count":1,"domain_concept_count":2,"matched_concepts":[{"id":"6f1c2d3e-4b5a-6789-0abc-def012345678","slug":"policy-number","name":"Policy Number","description":null}]}]}`))
	})
	out, _, err := runTableSuggestion(t, NewTableKindSuggestionsCommand(), "42")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SLUG", "NAME", "MATCHED-COUNT", "STANDARD-FIELD-COUNT", "MATCHED-STANDARD-FIELDS"} {
		if !strings.Contains(out, want) {
			t.Errorf("table headers missing %q: %s", want, out)
		}
	}
}
