package suggestion

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
	"github.com/qlustered/qctl/internal/testutil"
)

// guideSuggestionID is the suggestion id manage-suggestions quotes.
const guideSuggestionID = "550e8400-e29b-41d4-a716-446655440000"

// runGuideResolve runs `suggestion <verb> guideSuggestionID` against a server
// that answers with the resolved suggestion in state, and returns the output.
func runGuideResolve(t *testing.T, verb string, state api.SuggestionState) string {
	t.Helper()
	mock := newSuggestionTestEnv(t)
	s := sampleResolvedSuggestion(state)
	s.ID = uuid.MustParse(guideSuggestionID)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/8/rule-suggestions/"+guideSuggestionID+"/"+verb,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, s)
		})
	out, err := runSuggestionCommand(t, "suggestion", verb, guideSuggestionID, "--table-id", "8")
	if err != nil {
		t.Fatalf("suggestion %s: %v", verb, err)
	}
	return out
}

// Pins the result line quoted under manage-suggestions "## Take it".
func TestGuideQuoteSuggestionAccept(t *testing.T) {
	guidetest.AssertQuoted(t, "manage-suggestions", runGuideResolve(t, "accept", api.SuggestionStateApplied))
}

// Pins the result line quoted under manage-suggestions "## Drop it".
func TestGuideQuoteSuggestionDismiss(t *testing.T) {
	guidetest.AssertQuoted(t, "manage-suggestions", runGuideResolve(t, "dismiss", api.SuggestionStateDismissed))
}
