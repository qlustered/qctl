package change_proposal_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

const approveURL = "/approve"

// detail is the GET response for p: the proposal with a current review whose
// digest is "d1g35t".
func detail(t *testing.T, p api.ChangeProposalSchema) map[string]interface{} {
	t.Helper()
	out := asJSON(t, p)
	out["review"] = map[string]interface{}{
		"validity": "current", "reasons": []interface{}{}, "blockers": []interface{}{},
		"impact": map[string]interface{}{
			"bound": map[string]interface{}{
				"issue_type": "anomaly",
				"keys": []interface{}{map[string]interface{}{"key": "anomaly/not_anomaly", "scope": []interface{}{"issue"},
					"modifies_data": false, "follow_on": []interface{}{}, "reversible": true}},
				"target": map[string]interface{}{"kind": "alert", "id": 9},
			},
			"context": map[string]interface{}{"files": []interface{}{}, "same_issue_sweep_ids": []interface{}{}},
		},
		"impact_digest": "d1g35t",
	}
	out["impact_at_submit"] = map[string]interface{}{"bound": map[string]interface{}{}, "context": map[string]interface{}{}}
	out["impact_diff"] = map[string]interface{}{}
	return out
}

func pending(action string) api.ChangeProposalSchema {
	p := proposal(api.ChangeProposalStatePendingReview, 4)
	p.Payload = map[string]interface{}{"action": action}
	p.AllowedActions = []api.ChangeProposalAction{api.ChangeProposalActionApprove, api.ChangeProposalActionReject}
	return p
}

func executed() api.ChangeProposalSchema {
	p := proposal(api.ChangeProposalStateExecuted, 6)
	p.ExecutionResult = &map[string]interface{}{
		"response":     map[string]interface{}{"redirect_post_submit_to": "", "should_refresh_post_submit": true},
		"partial":      false,
		"target_state": map[string]interface{}{"resolved": true, "issue_type": "anomaly"},
	}
	return p
}

func requireNonTTY(t *testing.T) {
	t.Helper()
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test')")
	}
}

func TestApproveSendsTheVersionAndDigestItRead(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("not_anomaly")))
	got := captureJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, executed())

	stdout, stderr, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitSuccess)
	if (*got)["version_id"] != float64(4) || (*got)["impact_digest"] != "d1g35t" {
		t.Errorf("approve body = %v, want the version and digest the GET returned", *got)
	}
	id := proposalID.String()
	for _, want := range []string{
		"# change-proposal/" + id + " — approving runs this as you\n",
		"## PAYLOAD\n",
		"## REVIEW\nValidity: current — it applies as submitted\n",
		"Impact digest: d1g35t",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the preview belongs on stderr, next to the prompt; missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "## PAYLOAD") {
		t.Errorf("preview leaked onto stdout:\n%s", stdout)
	}
	for _, want := range []string{
		"change-proposal/" + id + " approved (state: executed, version 6)\n",
		"Response: should_refresh_post_submit: true\n",
		"Target alert/9 now: resolved\n",
		"Next: qctl describe change-proposal " + id + "\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestApproveJSONKeepsStdoutForTheProposal(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("not_anomaly")))
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, executed())

	stdout, stderr, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes", "-o", "json")
	wantExit(t, err, qerrors.ExitSuccess)
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || got["state"] != "executed" {
		t.Fatalf("stdout is not the executed proposal (%v):\n%s", err, stdout)
	}
	if !strings.Contains(stderr, "## REVIEW") || !strings.Contains(stderr, "approved (state: executed") {
		t.Errorf("preview and result lines belong on stderr:\n%s", stderr)
	}
}

func TestApproveWithoutATerminalNeedsYes(t *testing.T) {
	requireNonTTY(t)
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("not_anomaly")))
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, executed())

	_, _, err := run(t, "change-proposal", "approve", proposalID.String())
	wantExit(t, err, qerrors.ExitBadUsage)
	if n := countRequests(mock, "POST"); n != 0 {
		t.Errorf("%d POSTs without --yes, want none", n)
	}
}

func TestApprovePromptsAndHonoursTheAnswer(t *testing.T) {
	for answer, want := range map[string]int{"y\n": 1, "n\n": 0} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("not_anomaly")))
			serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, executed())
			prompt.SetReader(strings.NewReader(answer))
			t.Cleanup(prompt.ResetReader)

			_, stderr, err := run(t, "change-proposal", "approve", proposalID.String())
			if want == 0 {
				if !errors.Is(err, cmdutil.ErrDeclined) {
					t.Fatalf("err = %v, want ErrDeclined", err)
				}
				wantExit(t, err, qerrors.ExitGenericError)
			} else {
				wantExit(t, err, qerrors.ExitSuccess)
			}
			if !strings.Contains(stderr, "Approve change-proposal/"+proposalID.String()+" and run action not_anomaly on alert/9 as you?") {
				t.Errorf("prompt missing:\n%s", stderr)
			}
			if n := countRequests(mock, "POST"); n != want {
				t.Errorf("%d POSTs, want %d", n, want)
			}
		})
	}
}

func TestApproveExecutionFailedExitsOne(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("not_anomaly")))
	failed := proposal(api.ChangeProposalStateExecutionFailed, 6)
	failed.ExecutionResult = &map[string]interface{}{
		"error":        map[string]interface{}{"status_code": 409, "error_code": "PERIOD_LOCKED", "message": "The period is locked."},
		"partial":      true,
		"target_state": map[string]interface{}{"resolved": false, "issue_type": "anomaly"},
	}
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, failed)

	stdout, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitGenericError)
	for _, want := range []string{
		"approved (state: execution_failed, version 6)\n",
		"Execution failed: 409 PERIOD_LOCKED: The period is locked.\n",
		"Partial: true — part of the change was committed and stays; see what moved: qctl get history --table-id 42\n",
		"Target alert/9 now: still open\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// TestApproveRelaysTheResolutionsOwnError: a resolve refused before it
// changed anything keeps its exit code, reads as alert resolve's would, and
// says the proposal is still pending.
func TestApproveRelaysTheResolutionsOwnError(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("not_anomaly")))
	serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionNotAnomaly))
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusConflict,
		apiError("PERIOD_LOCKED", "The reporting period is locked.", nil))

	_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	msg := err.Error()
	for _, want := range []string{
		"Note: the locked period is the alert's own",
		"change-proposal/" + proposalID.String() + " is still pending_review (version 4): nothing was applied.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
}

func TestApproveRelayedErrorKeepsItsExitCode(t *testing.T) {
	for name, status := range map[string]int{"403": http.StatusForbidden, "404": http.StatusNotFound, "422": http.StatusUnprocessableEntity} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			p := pending("not_anomaly")
			p.Operation, p.AlertItemID, p.WarningItemID = api.ChangeReviewOperationWarningResolve, nil, intPtr(55)
			serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, p))
			serveJSON(mock, "POST", proposalURL+approveURL, status, apiError("", "refused", nil))

			_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
			want := map[int]int{403: qerrors.ExitUnauthorized, 404: qerrors.ExitNotFound, 422: qerrors.ExitBadUsage}[status]
			wantExit(t, err, want)
			if !strings.Contains(err.Error(), "still pending_review") {
				t.Errorf("error does not say the proposal is still pending:\n%v", err)
			}
		})
	}
}

func TestApproveReviewConflictsExplainAndDoNotRetry(t *testing.T) {
	review := map[string]interface{}{
		"validity": "current", "reasons": []interface{}{},
		"blockers": []interface{}{map[string]interface{}{
			"blocker": "period_locked", "status_code": 409, "error_code": "PERIOD_LOCKED", "message": "The period is locked.",
		}},
	}
	for code, want := range map[string]string{
		"CHANGE_PROPOSAL_IMPACT_CHANGED":   "Now: state pending_review, version 4; your next steps: approve, reject",
		"CHANGE_PROPOSAL_BLOCKED":          "- period_locked (409 PERIOD_LOCKED): The period is locked.",
		"CHANGE_PROPOSAL_STALE":            "no longer applies as submitted",
		"CHANGE_PROPOSAL_VERSION_MISMATCH": "the proposal changed since it was read",
	} {
		t.Run(code, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("not_anomaly")))
			serveJSON(mock, "POST", proposalURL+approveURL, http.StatusConflict, apiError(code, "refused", map[string]interface{}{
				"change_proposal": asJSON(t, pending("not_anomaly")), "review": review,
			}))

			_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
			wantExit(t, err, qerrors.ExitBadUsage)
			for _, line := range []string{want, "Now: state pending_review, version 4; your next steps: approve, reject",
				"qctl describe change-proposal " + proposalID.String()} {
				if !strings.Contains(err.Error(), line) {
					t.Errorf("error missing %q:\n%v", line, err)
				}
			}
			if n := countRequests(mock, "POST"); n != 1 {
				t.Errorf("%d POSTs, want exactly 1 (no retry)", n)
			}
		})
	}
}

// TestApproveOnlyPreviewsAPendingReviewProposal: any other state is sent
// unprompted and ungated, and the server's state refusal, with the fresh
// proposal, is the answer.
func TestApproveOnlyPreviewsAPendingReviewProposal(t *testing.T) {
	requireNonTTY(t)
	for _, state := range []api.ChangeProposalState{api.ChangeProposalStateExecuted, api.ChangeProposalStateDraft} {
		t.Run(string(state), func(t *testing.T) {
			mock := newEnv(t)
			p := pending("discard_migration")
			p.State, p.VersionID, p.AllowedActions = state, 6, []api.ChangeProposalAction{api.ChangeProposalActionSubmit}
			read := asJSON(t, p)
			if state == api.ChangeProposalStateDraft {
				read = detail(t, p)
			}
			serveJSON(mock, "GET", proposalURL, http.StatusOK, read)
			got := captureJSON(mock, "POST", proposalURL+approveURL, http.StatusConflict,
				apiError("CHANGE_PROPOSAL_STATE_CONFLICT", "A change proposal in state "+string(state)+" cannot be approved.",
					map[string]interface{}{"change_proposal": asJSON(t, p)}))
			protectCurrentContext(t)

			_, stderr, err := run(t, "change-proposal", "approve", proposalID.String())
			wantExit(t, err, qerrors.ExitBadUsage)
			for _, want := range []string{
				"the proposal is now " + string(state),
				"Now: state " + string(state) + ", version 6; your next steps: submit",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q:\n%v", want, err)
				}
			}
			if strings.Contains(stderr, "## PAYLOAD") {
				t.Errorf("a proposal that cannot be approved was previewed:\n%s", stderr)
			}
			if (*got)["version_id"] != float64(6) || (*got)["impact_digest"] != "" {
				t.Errorf("approve body = %v", *got)
			}
		})
	}
}

// TestApprovePendingReviewWithoutAReviewNeverPosts: a server that predates
// impact digests sends no review, and approving there would skip the
// preview, the prompt and the protected-context gate.
func TestApprovePendingReviewWithoutAReviewNeverPosts(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, pending("discard_migration"))
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, executed())

	_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	if !strings.Contains(err.Error(), "upgrade the server") {
		t.Errorf("error = %v", err)
	}
	if n := countRequests(mock, "POST"); n != 0 {
		t.Errorf("%d POSTs, want none", n)
	}
}

// TestApprovePolicyDenialSkipsTheResolveExplainers: the approver check's 403
// is not the resolution's failure, so no alert read and no action note.
func TestApprovePolicyDenialSkipsTheResolveExplainers(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("map_value")))
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusForbidden,
		apiError("CHANGE_REVIEW_POLICY_DENIED", "The editor role may not approve this change.", nil))

	_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitUnauthorized)
	if strings.Contains(err.Error(), "note for action") {
		t.Errorf("policy denial carries an action note:\n%v", err)
	}
	if !strings.Contains(err.Error(), "still pending_review") {
		t.Errorf("error does not report the re-read:\n%v", err)
	}
	for _, r := range mock.Requests() {
		if strings.Contains(r, "/alerts/") {
			t.Errorf("policy denial read the alert: %s", r)
		}
	}
}

// TestApproveRelayedErrorCarriesTheNoteWhenTheAlertReadFails: the action
// note does not depend on reading the alert.
func TestApproveRelayedErrorCarriesTheNoteWhenTheAlertReadFails(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("map_value")))
	serveJSON(mock, "GET", alertURL, http.StatusInternalServerError, map[string]interface{}{"detail": "boom"})
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusUnprocessableEntity, apiError("", "cannot map a blank value", nil))

	_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	if !strings.Contains(err.Error(), "note for action map_value (from the backend)") {
		t.Errorf("error missing the action note:\n%v", err)
	}
}

func TestApproveGoneTargetPromptNamesTheIssue(t *testing.T) {
	mock := newEnv(t)
	p := pending("not_anomaly")
	p.AlertItemID = nil
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, p))
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, executed())
	prompt.SetReader(strings.NewReader("n\n"))
	t.Cleanup(prompt.ResetReader)

	_, stderr, err := run(t, "change-proposal", "approve", proposalID.String())
	wantExit(t, err, qerrors.ExitGenericError)
	if !strings.Contains(stderr, "run action not_anomaly on issue as you?") {
		t.Errorf("prompt does not name the issue:\n%s", stderr)
	}
}

func TestApproveDestructiveActionOnAProtectedContextNeedsTheContextNamed(t *testing.T) {
	requireNonTTY(t)
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("discard_migration")))
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, executed())
	name := protectCurrentContext(t)

	_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	if n := countRequests(mock, "POST"); n != 0 {
		t.Fatalf("refused approve still sent %d POSTs", n)
	}

	config.SetContextOverride(name)
	if _, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes"); err != nil {
		t.Fatalf("with --context %s: %v", name, err)
	}
	if n := countRequests(mock, "POST"); n != 1 {
		t.Errorf("POSTs = %d with the context named, want 1", n)
	}
}

func TestApproveNonDestructiveActionOnAProtectedContextRuns(t *testing.T) {
	requireNonTTY(t)
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, pending("not_anomaly")))
	serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, executed())
	protectCurrentContext(t)

	_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitSuccess)
}

// protectCurrentContext marks the stored current context protected and
// returns its name.
func protectCurrentContext(t *testing.T) string {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx, err := cfg.GetCurrentContext()
	if err != nil {
		t.Fatalf("GetCurrentContext: %v", err)
	}
	ctx.Protected = testutil.BoolPtr(true)
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { config.SetContextOverride("") })
	return cfg.CurrentContext
}

func TestDecisionsSendTheReasonAndPrintTheNextSteps(t *testing.T) {
	for verb, tc := range map[string]struct {
		path, done string
		state      api.ChangeProposalState
	}{
		"reject":          {"/reject", "rejected", api.ChangeProposalStateRejected},
		"request-changes": {"/request-changes", "sent back for changes", api.ChangeProposalStateChangesRequested},
	} {
		t.Run(verb, func(t *testing.T) {
			mock := newEnv(t)
			result := proposal(tc.state, 5)
			if tc.state == api.ChangeProposalStateChangesRequested {
				result.AllowedActions = []api.ChangeProposalAction{api.ChangeProposalActionReject}
			}
			got := captureJSON(mock, "POST", proposalURL+tc.path, http.StatusOK, result)

			stdout, _, err := run(t, "change-proposal", verb, proposalID.String(), "--reason", "wrong value")
			wantExit(t, err, qerrors.ExitSuccess)
			if (*got)["reason"] != "wrong value" {
				t.Errorf("body = %v", *got)
			}
			id := proposalID.String()
			want := "change-proposal/" + id + " " + tc.done + " (state: " + string(tc.state) + ", version 5)\n"
			if tc.state == api.ChangeProposalStateChangesRequested {
				want += "Next: qctl change-proposal reject " + id + " --reason \"...\"\n"
			}
			want += "Next: qctl describe change-proposal " + id + "\n"
			if stdout != want {
				t.Errorf("stdout = %q, want %q", stdout, want)
			}
		})
	}
}

func TestDecisionsNeedAReasonBeforeAnyRequest(t *testing.T) {
	for _, verb := range []string{"reject", "request-changes"} {
		for name, extra := range map[string][]string{"missing": nil, "blank": {"--reason", "  "}} {
			t.Run(verb+" "+name, func(t *testing.T) {
				mock := newEnv(t)
				_, _, err := run(t, append([]string{"change-proposal", verb, proposalID.String()}, extra...)...)
				wantExit(t, err, qerrors.ExitBadUsage)
				if n := len(mock.Requests()); n != 0 {
					t.Errorf("%d requests, want none", n)
				}
			})
		}
	}
}

func TestDecisionErrorsKeepTheirExitCodes(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "POST", proposalURL+"/reject", http.StatusForbidden,
		apiError("CHANGE_REVIEW_POLICY_DENIED", "The editor role may not approve this change.", nil))
	serveJSON(mock, "POST", proposalURL+"/request-changes", http.StatusConflict,
		apiError("CHANGE_PROPOSAL_STATE_CONFLICT", "A change proposal in state changes_requested cannot be sent back for changes.",
			map[string]interface{}{"change_proposal": asJSON(t, proposal(api.ChangeProposalStateChangesRequested, 5))}))

	_, _, err := run(t, "change-proposal", "reject", proposalID.String(), "--reason", "no")
	wantExit(t, err, qerrors.ExitUnauthorized)
	_, _, err = run(t, "change-proposal", "request-changes", proposalID.String(), "--reason", "no")
	wantExit(t, err, qerrors.ExitBadUsage)
	if !strings.Contains(err.Error(), "the proposal is now changes_requested") {
		t.Errorf("error = %v", err)
	}
}

// TestProposeFileAndActionPrintTheSameNextLines: both propose paths print a
// Next line per allowed action, then the describe line.
func TestProposeFileAndActionPrintTheSameNextLines(t *testing.T) {
	created := proposal(api.ChangeProposalStateDraft, 1)
	created.AllowedActions = []api.ChangeProposalAction{api.ChangeProposalActionSubmit}
	id := proposalID.String()
	want := "Next: qctl change-proposal submit " + id + "\nNext: qctl describe change-proposal " + id + "\n"
	for name, args := range map[string][]string{
		"action": {"alert", "propose", "9", "--action", "not_anomaly", "--draft"},
		"file":   {"alert", "propose", "-f", writeFile(t, mapValueDoc), "--draft"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionNotAnomaly, api.AlertActionMapValue))
			serveJSON(mock, "POST", proposalsURL, http.StatusCreated, created)
			stdout, _, err := run(t, args...)
			wantExit(t, err, qerrors.ExitSuccess)
			if !strings.HasSuffix(stdout, want) {
				t.Errorf("stdout = %q, want it to end %q", stdout, want)
			}
		})
	}
}

// TestApproveDependencyConflictsNameTheStep: a refusal for a pending or a
// failed dependency names the step that clears it.
func TestApproveDependencyConflictsNameTheStep(t *testing.T) {
	dep := "0190cccc-0000-4000-8000-0000000000d0"
	for code, tc := range map[string]struct {
		review map[string]interface{}
		want   string
	}{
		"CHANGE_PROPOSAL_BLOCKED": {map[string]interface{}{"blockers": []interface{}{map[string]interface{}{
			"blocker": "dependency_not_executed", "status_code": 409, "error_code": "CHANGE_PROPOSAL_DEPENDENCY_PENDING",
			"message": "not executed yet.",
		}}}, "it clears once the dependency is approved and executed:\n  qctl describe change-proposal " + dep},
		"CHANGE_PROPOSAL_STALE": {map[string]interface{}{"reasons": []interface{}{map[string]interface{}{
			"reason": "dependency_failed", "message": "is rejected.",
		}}}, "qctl change-proposal edit " + proposalID.String() + " --remove-depends-on"},
	} {
		t.Run(code, func(t *testing.T) {
			mock := newEnv(t)
			p := pending("not_anomaly")
			serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, p))
			stored := asJSON(t, p)
			stored["depends_on_id"] = dep
			serveJSON(mock, "POST", proposalURL+approveURL, http.StatusConflict, apiError(code, "refused", map[string]interface{}{
				"change_proposal": stored, "review": tc.review,
			}))

			_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
			wantExit(t, err, qerrors.ExitBadUsage)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error missing %q:\n%v", tc.want, err)
			}
		})
	}
}

func TestApproveRefusesAWithheldDigestWithoutAPost(t *testing.T) {
	mock := newEnv(t)
	d := detail(t, pending("not_anomaly"))
	d["masked_fields"] = []string{"customer_email", "ssn"}
	d["review"].(map[string]interface{})["impact_digest"] = nil
	d["impact_diff"] = nil
	serveJSON(mock, "GET", proposalURL, http.StatusOK, d)

	_, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
	wantExit(t, err, qerrors.ExitUnauthorized)
	if err == nil || !strings.Contains(err.Error(), "masked_fields: customer_email, ssn") {
		t.Errorf("error = %v, want it to name the masked fields", err)
	}
	if countRequests(mock, "POST") != 0 {
		t.Errorf("sent an approve: %v", mock.Requests())
	}
}
