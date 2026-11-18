package change_proposal_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
)

var ruleID = uuid.MustParse("0190dddd-0000-4000-8000-000000000042")

// ruleProposal is a dataset_rule_patch proposal on ruleID of table 42.
func ruleProposal(state api.ChangeProposalState, version int) api.ChangeProposalSchema {
	p := proposal(state, version)
	p.Operation = api.ChangeReviewOperationDatasetRulePatch
	p.AlertItemID = nil
	p.DatasetRuleID = &ruleID
	p.Payload = map[string]interface{}{"params": map[string]interface{}{"threshold": 5, "mode": "strict"}}
	base := map[string]interface{}{"threshold": 3, "mode": "strict"}
	p.BaseParams = &base
	p.DryRunJobID = intPtr(77)
	return p
}

// TestRerunDryRunPostsAndPrintsTheNextSteps: rerun-dry-run POSTs the
// dry-run route with no body and prints the lead and Next lines.
func TestRerunDryRunPostsAndPrintsTheNextSteps(t *testing.T) {
	mock := newEnv(t)
	p := ruleProposal(api.ChangeProposalStatePendingReview, 3)
	p.AllowedActions = []api.ChangeProposalAction{api.ChangeProposalActionApprove}
	serveJSON(mock, "POST", proposalURL+"/dry-run", http.StatusOK, p)
	stdout, _, err := run(t, "change-proposal", "rerun-dry-run", proposalID.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"change-proposal/" + proposalID.String() + " dry run re-staged (state: pending_review, version 3)",
		"Next: qctl change-proposal approve " + proposalID.String(),
		"Next: qctl describe change-proposal " + proposalID.String(),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if n := countRequests(mock, "POST"); n != 1 {
		t.Errorf("POSTs = %d, want 1", n)
	}
}

func TestRerunDryRunErrorsKeepTheirExitCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   interface{}
		exit   int
		want   string
	}{
		"run still computing": {http.StatusConflict, apiError("CHANGE_PROPOSAL_STATE_CONFLICT",
			"Only a missing dry run, one that ended killed, error or stopped, or one that no longer matches the rule can be re-run.",
			map[string]interface{}{"change_proposal": asJSON(t, ruleProposal(api.ChangeProposalStatePendingReview, 3))}),
			qerrors.ExitBadUsage, "Re-read it:"},
		"not a rule proposal": {http.StatusUnprocessableEntity,
			map[string]interface{}{"detail": map[string]interface{}{"msg": "A alert_resolve proposal has no dry run."}},
			qerrors.ExitBadUsage, "has no dry run"},
		"over budget": {http.StatusTooManyRequests,
			map[string]interface{}{"detail": map[string]interface{}{"msg": "Dry-run limit reached.", "error_code": "DRY_RUN_RATE_LIMITED"}},
			qerrors.ExitGenericError, "Dry-run limit"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "POST", proposalURL+"/dry-run", tc.status, tc.body)
			_, _, err := run(t, "change-proposal", "rerun-dry-run", proposalID.String())
			wantExit(t, err, tc.exit)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err, tc.want)
			}
		})
	}
}

func TestRerunDryRunJSON(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "POST", proposalURL+"/dry-run", http.StatusOK, ruleProposal(api.ChangeProposalStatePendingReview, 3))
	stdout, _, err := run(t, "change-proposal", "rerun-dry-run", proposalID.String(), "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc["dry_run_job_id"] != float64(77) {
		t.Errorf("stdout = %q (%v)", stdout, err)
	}
}

// ruleDetail is the GET response for a pending_review rule proposal whose
// review finds the series rule approval current; reapproves is the server's
// approve_reapproves_series_rules.
func ruleDetail(t *testing.T, reapproves interface{}) map[string]interface{} {
	t.Helper()
	p := ruleProposal(api.ChangeProposalStatePendingReview, 4)
	p.AllowedActions = []api.ChangeProposalAction{api.ChangeProposalActionApprove}
	out := asJSON(t, p)
	out["review"] = map[string]interface{}{
		"validity": "current", "reasons": []interface{}{}, "blockers": []interface{}{},
		"impact": map[string]interface{}{
			"bound": map[string]interface{}{
				"operation":            "dataset_rule_patch",
				"base_params":          map[string]interface{}{"threshold": 3, "mode": "strict"},
				"params_diff":          map[string]interface{}{"threshold": map[string]interface{}{"before": 3, "after": 5}},
				"series_rule_approval": map[string]interface{}{"series_id": "0190ffff-0000-4000-8000-000000000001", "status": "current"},
				"rule_errors":          map[string]interface{}{},
			},
			"context": map[string]interface{}{"dry_run": map[string]interface{}{"id": 77, "status": "finished"}},
		},
		"impact_digest": "rul3",
	}
	out["impact_at_submit"] = map[string]interface{}{"bound": map[string]interface{}{}, "context": map[string]interface{}{}}
	out["impact_diff"] = map[string]interface{}{}
	out["approve_reapproves_series_rules"] = reapproves
	return out
}

// TestApproveRuleProposalPredictsAndReportsItsOutcome: the preview predicts
// the series re-approval, the prompt names the params patch, and the result
// is the rule outcome, not the rule detail the response carries.
func TestApproveRuleProposalPredictsAndReportsItsOutcome(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, ruleDetail(t, true))
	done := ruleProposal(api.ChangeProposalStateExecuted, 5)
	done.ExecutionResult = &map[string]interface{}{
		"response":             map[string]interface{}{"id": ruleID.String(), "instance_name": "vin_check", "code": "def check(): ..."},
		"partial":              false,
		"target_state":         map[string]interface{}{"state": "enabled", "params": map[string]interface{}{"threshold": 5, "mode": "strict"}},
		"outcome":              "applied",
		"series_rule_approval": map[string]interface{}{"id": 12},
	}
	got := captureJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, done)
	prompt.SetReader(strings.NewReader("y\n"))
	t.Cleanup(prompt.ResetReader)

	stdout, stderr, err := run(t, "change-proposal", "approve", proposalID.String())
	wantExit(t, err, qerrors.ExitSuccess)
	if (*got)["impact_digest"] != "rul3" {
		t.Errorf("approve body = %v", *got)
	}
	rule := "rule/" + ruleID.String()
	for _, want := range []string{
		"Operation: dataset_rule_patch on " + rule + "\n",
		"Approving by you also re-approves the series rule set with the new params\n",
		"Approve change-proposal/" + proposalID.String() + " and run its params patch on " + rule + " as you?",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	for _, want := range []string{
		"approved (state: executed, version 5)\n",
		"Outcome: applied — the rule now has the proposed params\n",
		"Series rule approval: re-recorded as approval 12, for the series rule set with the new params\n",
		"Target " + rule + ` now: enabled, params {"mode":"strict","threshold":5}` + "\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"Response:", "instance_name", "def check"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("stdout printed %q:\n%s", unwanted, stdout)
		}
	}
}

func TestApproveRuleProposalStagedNamesTheSuggestion(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, ruleDetail(t, false))
	done := ruleProposal(api.ChangeProposalStateExecuted, 5)
	suggestion := "0190aaaa-0000-4000-8000-0000000000aa"
	done.ExecutionResult = &map[string]interface{}{
		"response": map[string]interface{}{"id": suggestion}, "partial": false,
		"target_state":               map[string]interface{}{"state": "enabled", "params": map[string]interface{}{"threshold": 3}},
		"outcome":                    "staged",
		"dataset_rule_suggestion_id": suggestion,
		"application":                "dismissed",
		"series_rule_approval":       map[string]interface{}{"id": nil, "reason": "approver_role"},
	}
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, done)

	stdout, stderr, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitSuccess)
	if !strings.Contains(stderr, "Approving by you does not re-approve the series rule set\n") {
		t.Errorf("preview missing the prediction:\n%s", stderr)
	}
	for _, want := range []string{
		"Outcome: staged — the patch became a rule suggestion instead of changing the rule",
		"Staged suggestion: " + suggestion + " — see it: qctl describe rule-suggestion " + suggestion + " --table-id 42\n",
		"Suggestion application: dismissed — the suggestion was dismissed or failed, so the patch was not applied\n",
		"Series rule approval: not re-recorded — approver_role (",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// TestApproveRuleProposalErrorSkipsTheResolveExplainers: a refused rule
// PATCH keeps its exit code and reads no alert or warning.
func TestApproveRuleProposalErrorSkipsTheResolveExplainers(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, ruleDetail(t, nil))
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusUnprocessableEntity,
		apiError("", "threshold must be positive", nil))

	_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	if strings.Contains(err.Error(), "note for action") {
		t.Errorf("a rule patch error carries an action note:\n%v", err)
	}
	for _, r := range mock.Requests() {
		if strings.Contains(r, "/alerts/") || strings.Contains(r, "/warnings/") {
			t.Errorf("a rule patch error read an issue: %s", r)
		}
	}
}

// TestEditSetParamMergesIntoTheStoredParams: --set-param and --unset-param
// edit the params the proposal stores, re-send them whole with the version
// read, and read no rule.
func TestEditSetParamMergesIntoTheStoredParams(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, ruleProposal(api.ChangeProposalStatePendingReview, 3))
	got := captureJSON(mock, "PATCH", proposalURL, http.StatusOK, ruleProposal(api.ChangeProposalStatePendingReview, 4))

	stdout, _, err := run(t, "change-proposal", "edit", proposalID.String(),
		"--set-param", "threshold=8", "--set-param", "window=7d", "--unset-param", "mode")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	want := map[string]interface{}{"params": map[string]interface{}{"threshold": float64(8), "window": "7d"}}
	if !reflect.DeepEqual((*got)["payload"], interface{}(want)) || (*got)["version_id"] != float64(3) {
		t.Errorf("edit body = %v", *got)
	}
	for _, line := range []string{
		"Params change (the proposal's stored value -> the edited one):",
		`- mode: "strict" -> (unset)`,
		"- threshold: 5 -> 8",
		`- window: (unset) -> "7d"`,
		"edited (state: pending_review, version 4)",
	} {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout lacks %q:\n%s", line, stdout)
		}
	}
	if strings.Contains(stdout, "Note:") {
		t.Errorf("a proposal without rule_params_changed got the note:\n%s", stdout)
	}
	if n := countRequests(mock, "GET"); n != 1 {
		t.Errorf("%d GETs, want only the proposal read", n)
	}
}

// TestEditSetParamSendsAnEditThatChangesNothing: atlas accepts an edit whose
// params equal the stored ones, so it is sent, and the result says so.
func TestEditSetParamSendsAnEditThatChangesNothing(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, ruleProposal(api.ChangeProposalStateDraft, 2))
	serveJSON(mock, "PATCH", proposalURL, http.StatusOK, ruleProposal(api.ChangeProposalStateDraft, 3))
	stdout, _, err := run(t, "change-proposal", "edit", proposalID.String(), "--set-param", "threshold=5")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if n := countRequests(mock, "PATCH"); n != 1 {
		t.Errorf("%d PATCHes, want 1", n)
	}
	if !strings.Contains(stdout, "Params change: (none — the edited params equal the stored ones)") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// TestEditSetParamNotesParamsThatMovedUnderneath: a proposal whose review
// says the rule's params changed gets the note that the edit did not merge
// them.
func TestEditSetParamNotesParamsThatMovedUnderneath(t *testing.T) {
	mock := newEnv(t)
	detail := asJSON(t, ruleProposal(api.ChangeProposalStatePendingReview, 3))
	detail["review"] = map[string]interface{}{
		"validity": "stale", "impact_digest": "d",
		"reasons":  []interface{}{map[string]interface{}{"reason": "rule_params_changed", "message": "changed"}},
		"blockers": []interface{}{}, "resource_keys": []interface{}{},
		"impact": map[string]interface{}{"bound": map[string]interface{}{}, "context": map[string]interface{}{}},
	}
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail)
	serveJSON(mock, "PATCH", proposalURL, http.StatusOK, ruleProposal(api.ChangeProposalStatePendingReview, 4))

	stdout, _, err := run(t, "change-proposal", "edit", proposalID.String(), "--set-param", "threshold=8")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(stdout, "Note: the rule's params changed after this proposal went to review") {
		t.Errorf("stdout lacks the note:\n%s", stdout)
	}
}

// TestEditParamRefusalsSendNoPatch: each refusal of a param edit, before or
// after the proposal read, exits 2 with no PATCH.
func TestEditParamRefusalsSendNoPatch(t *testing.T) {
	for name, tc := range map[string]struct {
		current api.ChangeProposalSchema
		args    []string
		want    string
	}{
		"unknown unset":     {ruleProposal(api.ChangeProposalStateDraft, 2), []string{"--unset-param", "nope"}, `has no param "nope" (it has: mode, threshold)`},
		"alert proposal":    {proposal(api.ChangeProposalStateDraft, 2), []string{"--set-param", "threshold=8"}, "apply only to a dataset_rule_patch proposal"},
		"-f on a rule":      {ruleProposal(api.ChangeProposalStateDraft, 2), []string{"-f", writeFile(t, mapValueDoc)}, "-f and --action do not apply"},
		"action on a rule":  {ruleProposal(api.ChangeProposalStateDraft, 2), []string{"--action", "not_anomaly"}, "-f and --action do not apply"},
		"set with action":   {ruleProposal(api.ChangeProposalStateDraft, 2), []string{"--set-param", "a=1", "--action", "not_anomaly"}, "-f and --action replace"},
		"set without equal": {ruleProposal(api.ChangeProposalStateDraft, 2), []string{"--set-param", "a"}, "expected key=value"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", proposalURL, http.StatusOK, tc.current)
			_, _, err := run(t, append([]string{"change-proposal", "edit", proposalID.String()}, tc.args...)...)
			wantExit(t, err, qerrors.ExitBadUsage)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
			if n := countRequests(mock, "PATCH"); n != 0 {
				t.Errorf("%d PATCHes, want none", n)
			}
		})
	}
}
