package change_proposal_test

import (
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// TestProposeNoteIsSent: --note reaches the create body trimmed; without it
// the body sends null (no note).
func TestProposeNoteIsSent(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want interface{}
	}{
		"given":   {[]string{"--note", "  Broker confirmed the code  "}, "Broker confirmed the code"},
		"omitted": {nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionNotAnomaly))
			got := captureJSON(mock, "POST", proposalsURL, http.StatusCreated, proposal(api.ChangeProposalStatePendingReview, 1))
			if _, _, err := run(t, append([]string{"alert", "propose", "9", "--action", "not_anomaly"}, tc.args...)...); err != nil {
				t.Fatalf("propose: %v", err)
			}
			if v, present := (*got)["note"]; !present || v != tc.want {
				t.Errorf("note = %v (present %v), want %v", v, present, tc.want)
			}
		})
	}
}

// TestNoteFlagsRefuseBeforeAnyRequest: a blank note, --note under --dry-run
// and both edit flags at once are bad usage with no request.
func TestNoteFlagsRefuseBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"propose blank":        {"warning", "propose", "55", "--action", "not_anomaly", "--note", "   "},
		"propose with dry-run": {"alert", "propose", "9", "--action", "map_value", "--dry-run", "--note", "why"},
		"edit blank":           {"change-proposal", "edit", proposalID.String(), "--note", ""},
		"edit both flags":      {"change-proposal", "edit", proposalID.String(), "--note", "why", "--remove-note"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			_, _, err := run(t, args...)
			wantExit(t, err, qerrors.ExitBadUsage)
			if n := len(mock.Requests()); n != 0 {
				t.Errorf("%d requests sent, want none", n)
			}
		})
	}
}

// TestEditNoteKeepsThePayloadAndDependency: --note alone re-sends the stored
// payload with the new note and omits depends_on_id; --remove-note sends
// null.
func TestEditNoteKeepsThePayloadAndDependency(t *testing.T) {
	for name, tc := range map[string]struct {
		flags []string
		want  interface{}
	}{
		"set":    {[]string{"--note", "values look right"}, "values look right"},
		"remove": {[]string{"--remove-note"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			current := proposal(api.ChangeProposalStateDraft, 2)
			serveJSON(mock, "GET", proposalURL, http.StatusOK, current)
			got := captureJSON(mock, "PATCH", proposalURL, http.StatusOK, current)

			if _, _, err := run(t, append([]string{"change-proposal", "edit", proposalID.String()}, tc.flags...)...); err != nil {
				t.Fatalf("edit: %v", err)
			}
			if v, present := (*got)["note"]; !present || v != tc.want {
				t.Errorf("note = %v (present %v), want %v", v, present, tc.want)
			}
			if v, present := (*got)["depends_on_id"]; present {
				t.Errorf("depends_on_id = %v sent, want it omitted to keep the stored dependency", v)
			}
			if (*got)["version_id"] != float64(2) {
				t.Errorf("version_id = %v, want 2", (*got)["version_id"])
			}
		})
	}
}
