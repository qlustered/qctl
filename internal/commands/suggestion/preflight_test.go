package suggestion

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_suggestions"
)

// fakeGetter answers from a fixed table, so these tests exercise the
// classification without a server.
type fakeGetter struct {
	byID map[string]*rule_suggestions.Suggestion
	// errByID forces a transport-shaped failure for one id — an expired token,
	// a 5xx, a dead socket — which is NOT a classification and must never be
	// reported as a bad id.
	errByID     map[string]error
	calls       int
	lastToken   string
	lastDataset int
}

func (f *fakeGetter) Get(token string, datasetID int, id string) (*rule_suggestions.Suggestion, error) {
	f.calls++
	f.lastToken, f.lastDataset = token, datasetID
	if err, ok := f.errByID[id]; ok {
		return nil, err
	}
	if s, ok := f.byID[id]; ok {
		return s, nil
	}
	return nil, qerrors.NewNotFound("suggestion not found")
}

func sug(id string, state api.SuggestionState, candidate bool) *rule_suggestions.Suggestion {
	s := &rule_suggestions.Suggestion{
		ID:           uuid.MustParse(id),
		State:        state,
		Action:       api.SuggestionActionUpdate,
		Source:       api.User,
		InstanceName: "dedupe",
	}
	if candidate {
		s.IsRevalidationCandidate = &candidate
	}
	return s
}

const (
	idOK        = "3f2a0000-1111-2222-3333-000000000001"
	idDismissed = "3f2a0000-1111-2222-3333-000000000002"
	idCandidate = "3f2a0000-1111-2222-3333-000000000003"
	idMissing   = "3f2a0000-1111-2222-3333-000000000004"
)

func newFakeGetter() *fakeGetter {
	return &fakeGetter{byID: map[string]*rule_suggestions.Suggestion{
		idOK:        sug(idOK, api.SuggestionStatePending, false),
		idDismissed: sug(idDismissed, api.SuggestionStateDismissed, false),
		idCandidate: sug(idCandidate, api.SuggestionStatePending, true),
	}}
}

func TestPreflightPassesAllPending(t *testing.T) {
	g := newFakeGetter()
	if err := preflight(g, "tok", 8, []string{idOK}, "accept"); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if g.calls != 1 {
		t.Errorf("calls = %d, want 1 (one request per id)", g.calls)
	}
}

// TestPreflightForwardsTokenAndDataset pins that the credential and the table
// reach the getter unchanged. A pre-flight that read a different dataset than
// the one about to be written would classify the wrong suggestions and clear a
// command that should have been refused.
func TestPreflightForwardsTokenAndDataset(t *testing.T) {
	g := newFakeGetter()
	if err := preflight(g, "bearer-xyz", 42, []string{idOK}, "accept"); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if g.lastToken != "bearer-xyz" {
		t.Errorf("token = %q, want %q", g.lastToken, "bearer-xyz")
	}
	if g.lastDataset != 42 {
		t.Errorf("datasetID = %d, want 42", g.lastDataset)
	}
}

func TestPreflightRefusesNonPending(t *testing.T) {
	err := preflight(newFakeGetter(), "tok", 8, []string{idOK, idDismissed}, "accept")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "not pending (state: dismissed)") {
		t.Errorf("message must name the state, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Nothing was accepted") {
		t.Errorf("message must say nothing was written, got %q", err.Error())
	}
}

// TestPreflightPropagatesNonClassificationErrors guards the branch with the
// highest cost of silent regression. A 401, a 5xx or a dead socket says nothing
// about whether the suggestion is usable, so it must NOT join `problems`:
// folding it in would turn an expired token into "1 of 2 suggestions cannot be
// accepted: ... not found" at exit 2, sending the operator to fix an id that is
// perfectly fine. Per the repo's error contract the assertion is on the exit
// CODE, which a substring check on the message would not have caught.
func TestPreflightPropagatesNonClassificationErrors(t *testing.T) {
	g := newFakeGetter()
	g.errByID = map[string]error{idOK: qerrors.NewUnauthorized("token expired")}

	// idOK fails first and idDismissed (a genuine problem) follows it, so an
	// implementation that appended instead of returning would still produce an
	// error — just the wrong one. That is the mutation this test exists to fail.
	err := preflight(g, "tok", 8, []string{idOK, idDismissed}, "accept")
	if err == nil {
		t.Fatal("expected the transport failure to propagate")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d — the underlying error's own code, not bad usage", got, qerrors.ExitUnauthorized)
	}
	if strings.Contains(err.Error(), "cannot be") {
		t.Errorf("a transport failure must not be reported as a bad id, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), idOK) {
		t.Errorf("message must name the id it was checking, got %q", err.Error())
	}
	// Returning rather than collecting also means it stops at the first failure:
	// idDismissed is never read.
	if g.calls != 1 {
		t.Errorf("calls = %d, want 1 — preflight must abandon the check, not go on", g.calls)
	}
}

func TestPreflightReportsEveryBadIDAtOnce(t *testing.T) {
	err := preflight(newFakeGetter(), "tok", 8, []string{idOK, idDismissed, idMissing}, "accept")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	// Fixing them one round-trip at a time is the failure mode this exists to
	// prevent, so both bad ids must appear in the same message.
	if !strings.Contains(err.Error(), idDismissed) || !strings.Contains(err.Error(), idMissing) {
		t.Errorf("message must list every bad id, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("message must classify the missing id, got %q", err.Error())
	}
}

func TestPreflightRefusesCandidateOnAcceptOnly(t *testing.T) {
	err := preflight(newFakeGetter(), "tok", 8, []string{idCandidate}, "accept")
	if err == nil {
		t.Fatal("accept must refuse a revalidation candidate, mirroring the backend 409")
	}
	if !strings.Contains(err.Error(), "revalidation candidate") {
		t.Errorf("message must name the reason, got %q", err.Error())
	}

	// dismiss is the verb that DOES apply to a candidate, so the same id passes.
	if err := preflight(newFakeGetter(), "tok", 8, []string{idCandidate}, "dismiss"); err != nil {
		t.Fatalf("dismiss must accept a candidate: %v", err)
	}
}
