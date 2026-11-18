package describe

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

var (
	cpRuleID   = uuid.MustParse("0190eeee-0000-4000-8000-000000000042")
	cpRulePath = "/api/orgs/" + cpOrgID + "/dataset-rules/" + cpRuleID.String()
)

// cpRuleProposal is a pending_review dataset_rule_patch proposal on
// cpRuleID of table 7.
func cpRuleProposal() api.ChangeProposalSchema {
	p := cpPendingAlertProposal()
	p.Operation = api.ChangeReviewOperationDatasetRulePatch
	p.AlertItemID = nil
	p.DataSourceModelID = nil
	p.DatasetRuleID = &cpRuleID
	p.AddressedAlertItemIds = []int{}
	p.Payload = map[string]interface{}{"params": map[string]interface{}{"threshold": 5, "mode": "strict"}}
	p.PolicyKeys = [][]string{{"params"}}
	p.DryRunJobID = cpIntPtr(31)
	p.AllowedActions = []api.ChangeProposalAction{
		api.ChangeProposalActionEdit, api.ChangeProposalActionApprove, api.ChangeProposalActionRerunDryRun,
	}
	return p
}

// cpRuleReview is a rule proposal's review: its params diff, base params,
// a current series approval and a finished dry run with a rule error.
func cpRuleReview(reapproves interface{}) map[string]interface{} {
	return map[string]interface{}{
		"review": map[string]interface{}{
			"validity": "current", "reasons": []interface{}{},
			"blockers": []interface{}{map[string]interface{}{
				"blocker": "dry_run_rule_errors", "status_code": 409, "error_code": nil,
				"message": "The proposal's dry run recorded rule errors: 1 rule_bug.",
			}},
			"impact": map[string]interface{}{
				"bound": map[string]interface{}{
					"operation":   "dataset_rule_patch",
					"target":      map[string]interface{}{"kind": "dataset_rule", "id": cpRuleID.String()},
					"payload":     map[string]interface{}{"params": map[string]interface{}{"threshold": 5}},
					"base_params": map[string]interface{}{"threshold": 3, "legacy": true},
					"params_diff": map[string]interface{}{
						"threshold": map[string]interface{}{"before": 3, "after": 5},
						"legacy":    map[string]interface{}{"before": true},
					},
					"series_rule_approval":  map[string]interface{}{"series_id": "0190ffff-0000-4000-8000-000000000001", "status": "current"},
					"dry_run_job_id":        31,
					"rule_errors":           map[string]interface{}{"rule_bug": 1},
					"addressed_alert_ids":   []interface{}{},
					"addressed_warning_ids": []interface{}{},
				},
				"context": map[string]interface{}{"dry_run": map[string]interface{}{
					"id": 31, "state": "finished", "status": "finished", "sample_size": 500,
					"counts": map[string]interface{}{"blocker_left_only": 0, "blocker_right_only": 4,
						"warning_left_only": 1, "warning_right_only": 0},
					"rates": map[string]interface{}{"blocker": 0.008},
				}},
			},
			"resource_keys": []interface{}{"dataset_rule:" + cpRuleID.String() + "/params"},
			"impact_digest": "rul3d1g",
		},
		"impact_at_submit":                map[string]interface{}{"bound": map[string]interface{}{}, "context": map[string]interface{}{}},
		"impact_diff":                     map[string]interface{}{},
		"overlaps":                        []interface{}{},
		"approve_reapproves_series_rules": reapproves,
	}
}

func serveRule(mock *testutil.MockAPIServer, status int, state string) {
	mock.RegisterHandler("GET", cpRulePath, func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			testutil.RespondJSON(w, status, map[string]interface{}{"detail": "Not found"})
			return
		}
		testutil.RespondJSON(w, status, map[string]interface{}{
			"id": cpRuleID.String(), "instance_name": "vin_check", "state": state, "dataset_id": 7,
			"params": map[string]interface{}{"threshold": 3, "legacy": true},
		})
	})
}

func TestDescribeChangeProposalRuleProposal(t *testing.T) {
	f := newCPFixture(cpRuleProposal())
	f.detail = cpRuleReview(true)
	mock := f.serve(t)
	serveRule(mock, http.StatusOK, "enabled")
	out, stderr, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
	id := cpID.String()
	assertOutputInOrder(t, out,
		"Operation: dataset_rule_patch\n",
		"Target: rule/"+cpRuleID.String()+" (vin_check) — now: enabled\n",
		"Table: table-id 7\n",
		"## PAYLOAD\nThe params the rule would be patched with (they replace its params as a whole):\n```yaml\nparams:\n",
		"  threshold: 5\n",
		"## REVIEW\n",
		"- dry_run_rule_errors (409): The proposal's dry run recorded rule errors: 1 rule_bug.\n",
		"Params change (the rule's live value -> the proposed one):\n- legacy: true -> (unset)\n- threshold: 3 -> 5\n",
		`Base params (the rule's params when this version went to review): {"legacy":true,"threshold":3}`+"\n",
		"Series rule approval: series 0190ffff-0000-4000-8000-000000000001 is approved for its live rule set\n",
		"Approving by you also re-approves the series rule set with the new params\n",
		"Dry run: dry-run job 31 — finished; sample size 500\n",
		"  counts (left = live params, right = proposed): blocker_left_only: 0, blocker_right_only: 4, warning_left_only: 1, warning_right_only: 0\n",
		"  rates: blocker: 0.008\n",
		"  see it: qctl describe dry-run-job 31\n",
		"Rule errors in the dry run: rule_bug: 1\n",
		"Resource keys: dataset_rule:"+cpRuleID.String()+"/params\n",
		"Impact digest: rul3d1g",
		"## NEXT ACTIONS\n",
		"- edit:            qctl change-proposal edit "+id+" --set-param <key>=<value> (or --unset-param <key>), merged into the PAYLOAD params above\n",
		"- approve:         qctl change-proposal approve "+id,
		"- rerun dry run:   qctl change-proposal rerun-dry-run "+id+"\n",
	)
	for _, unwanted := range []string{"Changes (by policy key)", "Linked files", "Same-issue sweep", "Other impact fields", "Note:", "no command"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a rule proposal printed %q\n%s", unwanted, out)
		}
	}
}

func TestDescribeChangeProposalRuleProposalWithoutADryRun(t *testing.T) {
	f := newCPFixture(cpRuleProposal())
	review := cpRuleReview(false)
	impact := review["review"].(map[string]interface{})["impact"].(map[string]interface{})
	impact["context"] = map[string]interface{}{"dry_run": nil}
	impact["bound"].(map[string]interface{})["rule_errors"] = map[string]interface{}{}
	f.detail = review
	mock := f.serve(t)
	serveRule(mock, http.StatusOK, "enabled")
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out,
		"Approving by you does not re-approve the series rule set\n",
		"Dry run: (none — approve is blocked until one finishes; stage one with: qctl change-proposal rerun-dry-run "+cpID.String()+")\n",
	)
}

// A deleted rule, or one the server cleared, turns the next step obsolete.
func TestDescribeChangeProposalRuleDeletedOrGone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		state  string
		clear  bool
		target string
	}{
		{"deleted", http.StatusOK, "deleted", false, "Target: rule/" + cpRuleID.String() + " (vin_check) — now: deleted\n"},
		{"404", http.StatusNotFound, "", false, "Target: rule/" + cpRuleID.String() + " — now: gone\n"},
		{"cleared", 0, "", true, "Target: a deleted rule (the server cleared its id) — now: gone\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := cpRuleProposal()
			if tc.clear {
				p.DatasetRuleID = nil
			}
			mock := newCPFixture(p).serve(t)
			if !tc.clear {
				serveRule(mock, tc.status, tc.state)
			}
			out, stderr, err := execDescribeChangeProposal(cpID.String())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stderr != "" {
				t.Errorf("a deleted rule is an answer, not a warning; got %q", stderr)
			}
			assertOutputInOrder(t, out, tc.target,
				"Note: the target rule is deleted or gone, so the next step taken on this proposal turns it obsolete")
		})
	}
}

func TestDescribeChangeProposalRuleReadFailureWarns(t *testing.T) {
	mock := newCPFixture(cpRuleProposal()).serve(t)
	serveRule(mock, http.StatusInternalServerError, "")
	out, stderr, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out, "Target: rule/"+cpRuleID.String()+" — now: unknown\n")
	if !strings.Contains(stderr, "Warning: could not read rule/"+cpRuleID.String()) {
		t.Errorf("stderr = %q", stderr)
	}
	if strings.Contains(out, "Note:") {
		t.Errorf("an unread rule is not a deleted one\n%s", out)
	}
}

// A payload carrying more than params is shown raw, with the reason.
func TestDescribeChangeProposalRulePayloadWithOtherKeysIsRaw(t *testing.T) {
	p := cpRuleProposal()
	p.Payload = map[string]interface{}{"params": map[string]interface{}{"threshold": 5}, "state": "disabled"}
	mock := newCPFixture(p).serve(t)
	serveRule(mock, http.StatusOK, "enabled")
	out, _, err := execDescribeChangeProposal(cpID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsAll(t, out, `The raw request body (it carries "state", and a rule-param proposal carries only params):`)
}

// cpExecutedRuleProposal is cpRuleProposal executed with result.
func cpExecutedRuleProposal(result map[string]interface{}) api.ChangeProposalSchema {
	p := cpRuleProposal()
	p.State = api.ChangeProposalStateExecuted
	p.ExecutionStartedAt = &cpApproved
	p.ExecutedAt = &cpApproved
	p.AllowedActions = []api.ChangeProposalAction{}
	p.ExecutionResult = &result
	return p
}

// An executed rule proposal renders its outcome, never the rule detail.
func TestDescribeChangeProposalRuleExecution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result map[string]interface{}
		want   []string
	}{
		{"applied and re-approved", map[string]interface{}{
			"response":             map[string]interface{}{"id": cpRuleID.String(), "instance_name": "vin_check", "code": "def check(): ..."},
			"partial":              false,
			"target_state":         map[string]interface{}{"state": "enabled", "params": map[string]interface{}{"threshold": 5}},
			"outcome":              "applied",
			"series_rule_approval": map[string]interface{}{"id": 12},
		}, []string{
			"Result: succeeded (-o yaml carries the response: the rule, or the staged suggestion)\n",
			"Partial: false\n",
			"Outcome: applied — the rule now has the proposed params\n",
			"Series rule approval: re-recorded as approval 12, for the series rule set with the new params\n",
			`Target at settlement: enabled, params {"threshold":5}` + "\n",
		}},
		{"staged and pending", map[string]interface{}{
			"response":                   map[string]interface{}{"id": "0190aaaa-0000-4000-8000-0000000000aa"},
			"partial":                    false,
			"target_state":               map[string]interface{}{"state": "enabled", "params": map[string]interface{}{"threshold": 3}},
			"outcome":                    "staged",
			"dataset_rule_suggestion_id": "0190aaaa-0000-4000-8000-0000000000aa",
			"application":                "pending",
			"series_rule_approval":       map[string]interface{}{"id": nil, "reason": "staged"},
		}, []string{
			"Outcome: staged — the patch became a rule suggestion instead of changing the rule; it applies when the suggestion is accepted\n",
			"Staged suggestion: 0190aaaa-0000-4000-8000-0000000000aa — see it: qctl describe rule-suggestion 0190aaaa-0000-4000-8000-0000000000aa --table-id 7\n",
			"Suggestion application: pending — accept or dismiss the suggestion",
			"Series rule approval: not re-recorded — staged (the patch was staged, not applied)\n",
			`Target at settlement: enabled, params {"threshold":3}` + "\n",
		}},
		{"refused series approval, rule gone", map[string]interface{}{
			"response": map[string]interface{}{"id": cpRuleID.String()}, "partial": false, "target_state": nil,
			"outcome": "applied", "series_rule_approval": map[string]interface{}{"id": nil, "reason": "rule_set_differs"},
		}, []string{
			"Series rule approval: not re-recorded — rule_set_differs (the live series rule set differs from the last approved one by more than this patch)\n",
			"Target at settlement: gone\n",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newCPFixture(cpExecutedRuleProposal(tc.result)).serve(t)
			serveRule(mock, http.StatusOK, "enabled")
			out, _, err := execDescribeChangeProposal(cpID.String())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertOutputInOrder(t, out, append([]string{"## EXECUTION\n"}, tc.want...)...)
			_, execution, _ := strings.Cut(out, "## EXECUTION")
			for _, unwanted := range []string{"Other result fields", "instance_name", "def check", "response:\n"} {
				if strings.Contains(execution, unwanted) {
					t.Errorf("EXECUTION printed %q\n%s", unwanted, out)
				}
			}
		})
	}
}
