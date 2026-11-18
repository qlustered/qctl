package apierror

import (
	"fmt"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

const (
	tablePolicyPointer = "`qctl get change-review-policy --table <table>` shows the roles the policy in force lists for each key."
	orgPolicyPointer   = "`qctl get change-review-policy` shows the roles the organization's policy lists for each key."
	anyPolicyPointer   = "`qctl get change-review-policy --table <table>` shows the roles the policy in force lists for each key; " +
		"with no selector it shows the organization's policy."
	genericProposeHint = "Propose the change for review instead, with `qctl alert propose`, `qctl warning propose` or `qctl table-rule propose`."
)

// changeReviewBody is a change review 403 as atlas sends it.
func changeReviewBody(code, contextJSON string) []byte {
	ctx := ""
	if contextJSON != "" {
		ctx = `,"context":` + contextJSON
	}
	return []byte(fmt.Sprintf(`{"detail":{"msg":"This table's change review policy refuses this change.","title":"Error","severity":"error","error_code":%q,"error_id":"req-1"%s}}`,
		code, ctx))
}

func changeReviewHint(t *testing.T, code, contextJSON string) string {
	t.Helper()
	opErr, err := ParseErrorResponse(changeReviewBody(code, contextJSON), 403)
	if err != nil {
		t.Fatalf("ParseErrorResponse: %v", err)
	}
	_, hint, found := strings.Cut(opErr.Format(), "\nHint: ")
	if !found {
		t.Fatalf("Format() has no Hint line:\n%s", opErr.Format())
	}
	return hint
}

func TestChangeReviewPolicyDeniedHint(t *testing.T) {
	for name, tc := range map[string]struct{ ctx, want string }{
		"table operation, one key": {
			`{"operation":"alert_resolve","verb":"resolve","denied_keys":[["invalid_keyword","trash_file"]]}`,
			"The policy does not let your role resolve alert_resolve key invalid_keyword/trash_file. " + tablePolicyPointer,
		},
		"table operation, several keys": {
			`{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["params"],["state"]]}`,
			"The policy does not let your role resolve dataset_rule_patch keys params, state. " + tablePolicyPointer,
		},
		"organization operation": {
			`{"operation":"rule_revision_submit","verb":"resolve","denied_keys":[["submit"]]}`,
			"The policy does not let your role resolve rule_revision_submit key submit. " + orgPolicyPointer,
		},
		"organization operation, no keys": {
			`{"operation":"rule_revision_patch","verb":"resolve","denied_keys":[]}`,
			"The change maps to no rule_revision_patch key the policy can list, so the policy refuses it for every role. " + orgPolicyPointer,
		},
		"resolve with no keys": {
			`{"operation":"history_undo","verb":"resolve","denied_keys":[]}`,
			"The change maps to no history_undo key the policy can list, so the policy refuses it for every role. " + tablePolicyPointer,
		},
		"propose verb": {
			`{"operation":"warning_resolve","verb":"propose","denied_keys":[["anomaly","not_anomaly"]]}`,
			"The policy does not let your role propose warning_resolve key anomaly/not_anomaly. " + tablePolicyPointer,
		},
		"approve verb": {
			`{"operation":"dataset_rule_patch","verb":"approve","denied_keys":[["params"]]}`,
			"The policy does not let your role approve dataset_rule_patch key params. " + tablePolicyPointer,
		},
		"approve verb, no keys": {
			`{"operation":"alert_resolve","verb":"approve","denied_keys":[]}`,
			tablePolicyPointer,
		},
		"edit verb, no keys": {
			`{"operation":"alert_resolve","verb":"edit","denied_keys":[]}`,
			tablePolicyPointer,
		},
		"no verb": {
			`{"operation":"settings_patch","denied_keys":[["anomaly_threshold"]]}`,
			"The policy does not let your role change settings_patch key anomaly_threshold. " + tablePolicyPointer,
		},
		"no denied_keys": {
			`{"operation":"settings_patch","verb":"resolve"}`,
			tablePolicyPointer,
		},
		"null denied_keys": {
			`{"operation":"settings_patch","verb":"resolve","denied_keys":null}`,
			tablePolicyPointer,
		},
		"denied_keys not a list of lists": {
			`{"operation":"settings_patch","verb":"resolve","denied_keys":["anomaly_threshold"]}`,
			tablePolicyPointer,
		},
		"an empty key": {
			`{"operation":"settings_patch","verb":"resolve","denied_keys":[[]]}`,
			tablePolicyPointer,
		},
		"a key part unsafe to print": {
			`{"operation":"settings_patch","verb":"resolve","denied_keys":[["anomaly\u001b[31m"]]}`,
			tablePolicyPointer,
		},
		"an operation unsafe to print": {
			`{"operation":"alert resolve\n","verb":"resolve","denied_keys":[["a"]]}`,
			anyPolicyPointer,
		},
		"operation not a string": {
			`{"operation":7,"verb":"resolve","denied_keys":[["a"]]}`,
			anyPolicyPointer,
		},
		"no context":       {"", anyPolicyPointer},
		"null context":     {"null", anyPolicyPointer},
		"empty context":    {"{}", anyPolicyPointer},
		"context a list":   {`["alert_resolve"]`, anyPolicyPointer},
		"context a string": {`"alert_resolve"`, anyPolicyPointer},
	} {
		t.Run(name, func(t *testing.T) {
			if got := changeReviewHint(t, CodeChangeReviewPolicyDenied, tc.ctx); got != tc.want {
				t.Errorf("hint =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestChangeProposalRequiredHint(t *testing.T) {
	for name, tc := range map[string]struct{ ctx, want string }{
		"alert resolve": {
			`{"operation":"alert_resolve","verb":"resolve","denied_keys":[["series_file_routing","assign_reporting_period"]]}`,
			"Propose the resolution for review instead: `qctl alert propose <alert-id> --action <action>`, " +
				"or `qctl alert propose -f <file>` with the AlertResolution manifest.",
		},
		"warning resolve": {
			`{"operation":"warning_resolve","verb":"resolve","denied_keys":[["anomaly","not_anomaly"]]}`,
			"Propose the resolution for review instead: `qctl warning propose <warning-id> --action <action>`, " +
				"or `qctl warning propose -f <file>` with the WarningResolution manifest.",
		},
		"dataset rule patch": {
			`{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["params"]]}`,
			"Propose the params change for review instead: " +
				"`qctl table-rule propose <rule> --table-id <id> --set-param <key>=<value>`.",
		},
		"data review approve, which has no proposal path": {
			`{"operation":"data_review_approve","verb":"resolve","denied_keys":[["rows"]]}`,
			"This change has no proposal path: someone whose role the policy lists must make it. " +
				"`qctl get change-review-policy --table <table>` shows the roles the policy in force lists for each key.",
		},
		"an operation with no propose command": {
			`{"operation":"settings_patch","verb":"resolve","denied_keys":[["anomaly_threshold"]]}`,
			genericProposeHint,
		},
		"malformed denied_keys": {
			`{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":"params"}`,
			"Propose the params change for review instead: " +
				"`qctl table-rule propose <rule> --table-id <id> --set-param <key>=<value>`.",
		},
		"operation not a string": {`{"operation":["alert_resolve"]}`, genericProposeHint},
		"no context":             {"", genericProposeHint},
		"null context":           {"null", genericProposeHint},
		"context not an object":  {`[1,2]`, genericProposeHint},
	} {
		t.Run(name, func(t *testing.T) {
			if got := changeReviewHint(t, CodeChangeProposalRequired, tc.ctx); got != tc.want {
				t.Errorf("hint =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

// A change review 403 keeps the exit code of a 403, hint and all.
func TestAChangeReviewRefusalStillExitsFour(t *testing.T) {
	for _, code := range []string{CodeChangeProposalRequired, CodeChangeReviewPolicyDenied} {
		t.Run(code, func(t *testing.T) {
			err := HandleHTTPErrorFromBytes(403,
				changeReviewBody(code, `{"operation":"alert_resolve","verb":"resolve","denied_keys":[["a","b"]]}`),
				"resolve alert failed")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
			}
			if !strings.Contains(err.Error(), "\nHint: ") {
				t.Errorf("error lacks the hint line:\n%s", err.Error())
			}
		})
	}
}

func TestChangeReviewRefusalOf(t *testing.T) {
	err := fmt.Errorf("resolve failed: %w", HandleHTTPErrorFromBytes(403,
		changeReviewBody(CodeChangeReviewPolicyDenied,
			`{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["params"],["state"]]}`),
		"patch failed"))
	got, ok := ChangeReviewRefusalOf(err)
	if !ok {
		t.Fatal("ChangeReviewRefusalOf found no refusal")
	}
	if got.Code != CodeChangeReviewPolicyDenied || got.Operation != OperationDatasetRulePatch || got.Verb != "resolve" ||
		!got.KeysKnown || strings.Join(got.DeniedKeys, ",") != "params,state" {
		t.Errorf("refusal = %+v", got)
	}
	if !got.DeniesKey("params") || got.DeniesKey("severity") {
		t.Errorf("DeniesKey: params %v, severity %v", got.DeniesKey("params"), got.DeniesKey("severity"))
	}
	if unknown := (ChangeReviewRefusal{}); !unknown.DeniesKey("params") {
		t.Error("DeniesKey with unknown keys = false, want true")
	}

	other := HandleHTTPErrorFromBytes(409, seriesConflictBody(CodePeriodLocked, "locked", ""), "patch failed")
	if _, ok := ChangeReviewRefusalOf(other); ok {
		t.Error("ChangeReviewRefusalOf found a refusal in a PERIOD_LOCKED error")
	}
	if _, ok := ChangeReviewRefusalOf(fmt.Errorf("plain")); ok {
		t.Error("ChangeReviewRefusalOf found a refusal in a plain error")
	}
}

// A change review 403 is raised after the server found the target, so the
// organization-scope note would send the reader after a scope failure that
// did not happen.
func TestChangeReviewRefusalCarriesNoScopeNote(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	for _, code := range []string{"CHANGE_PROPOSAL_REQUIRED", "CHANGE_REVIEW_POLICY_DENIED"} {
		t.Run(code, func(t *testing.T) {
			body := changeReviewBody(code, `{"operation":"alert_resolve","verb":"resolve","denied_keys":[["invalid_keyword","trash_file"]]}`)
			err := HandleHTTPErrorFromBytes(403, body, "failed to resolve alert")
			if strings.Contains(err.Error(), "Scoped to organization") {
				t.Errorf("error = %q, want NO organization-scope note on a change review 403", err)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
			}
		})
	}
}

// The control for the test above: the same 403 shape with another code keeps
// the note, so the absence above is the codes' doing.
func TestOtherForbiddenCodeKeepsTheScopeNote(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	err := HandleHTTPErrorFromBytes(403, changeReviewBody("PERMISSION_DENIED", ""), "failed to resolve alert")
	if !strings.Contains(err.Error(), "Scoped to organization Qluster-dev") {
		t.Errorf("error = %q, want the organization-scope note", err)
	}
}
