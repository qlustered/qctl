package describe

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

var heldByProposal = &api.ChangeProposalSummarySchema{
	ID:    uuid.MustParse("6f1c2d3e-4b5a-4c6d-8e7f-90a1b2c3d4e5"),
	State: api.ChangeProposalStatePendingReview,
}

// A held alert names its proposal in the footer, ahead of NEXT ACTIONS, in
// the YAML and table views, and in status.change_proposal for -o json.
func TestDescribeAlertNamesTheHoldingProposal(t *testing.T) {
	a := sampleAlertFullWithActions()
	a.ChangeProposal = heldByProposal
	id := heldByProposal.ID.String()

	for _, view := range [][]string{nil, {"-o", "table"}} {
		out := runDescribeAlert(t, a, append([]string{"describe", "alert", "1"}, view...)...)
		held := strings.Index(out, "HELD BY CHANGE PROPOSAL")
		next := strings.Index(out, "NEXT ACTIONS")
		if held < 0 || next < held {
			t.Errorf("%v: want HELD BY CHANGE PROPOSAL before NEXT ACTIONS, got:\n%s", view, out)
		}
		for _, want := range []string{
			id + " (pending_review) — resolving this alert directly turns the proposal obsolete",
			"Next: qctl describe change-proposal " + id,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: output should contain %q, got:\n%s", view, want, out)
			}
		}
	}

	var doc struct {
		Status struct {
			ChangeProposal map[string]string `json:"change_proposal"`
		} `json:"status"`
	}
	out := runDescribeAlert(t, a, "describe", "alert", "1", "-o", "json")
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("-o json did not decode: %v\n%s", err, out)
	}
	if got := doc.Status.ChangeProposal; got["id"] != id || got["state"] != "pending_review" {
		t.Errorf("status.change_proposal = %v, want id %s and state pending_review", got, id)
	}
}

func TestDescribeAlertSaysNothingOfAnUnheldAlert(t *testing.T) {
	out := runDescribeAlert(t, sampleAlertFullWithActions(), "describe", "alert", "1")
	if strings.Contains(out, "HELD BY") || strings.Contains(out, "change_proposal") {
		t.Errorf("an unheld alert should name no proposal, got:\n%s", out)
	}
}

func TestDescribeWarningNamesTheHoldingProposal(t *testing.T) {
	w := describeWarningFixture(false, api.NotAnomaly)
	w.ChangeProposal = heldByProposal
	out := runDescribeWarning(t, w, "describe", "warning", "55")
	for _, want := range []string{
		"# HELD BY CHANGE PROPOSAL",
		"resolving this warning directly turns the proposal obsolete",
		"# Next: qctl describe change-proposal " + heldByProposal.ID.String(),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output should contain %q, got:\n%s", want, out)
		}
	}
}
