package apply

import (
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestApplyTableRulePatchChangeReviewRefusals(t *testing.T) {
	const (
		paramsOnly = "  params:\n    threshold: 0.95\n"
		roundTrip  = "  state: enabled\n  severity: as_declared\n  params:\n    threshold: 0.95\n"
		stateOnly  = "  state: disabled\n"
		policy     = "`qctl get change-review-policy --table-id 1` shows the roles this table's policy lists for each dataset_rule_patch key"
		propose    = "`qctl table-rule propose " + testTableRuleID + " --table-id 1 --set-param <key>=<value>`"
		sentNote   = "The policy checks every field the patch sends, unchanged ones included: this one sent severity, state. " +
			"Leave out of the manifest a field you do not mean to change. "
	)
	cases := []struct {
		name    string
		code    string
		ctx     string
		spec    string
		want    string
		notWant []string
	}{
		{
			name: "proposal required",
			code: "CHANGE_PROPOSAL_REQUIRED",
			ctx:  `{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["params"]]}`,
			spec: paramsOnly,
			want: "\n\nInstead: propose the params change for review with " + propose +
				", one --set-param per changed key; it runs when an approver approves it",
		},
		{
			name: "params denied in a patch that sent other fields",
			code: "CHANGE_REVIEW_POLICY_DENIED",
			ctx:  `{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["params"]]}`,
			spec: roundTrip,
			want: "\n\nInstead: only params can be proposed, with " + propose +
				"; apply the other fields directly, from a manifest without spec.params. " + sentNote + policy,
		},
		{
			name: "unknown denied keys in a patch that sent other fields",
			code: "CHANGE_REVIEW_POLICY_DENIED",
			ctx:  `{"operation":"dataset_rule_patch","verb":"resolve"}`,
			spec: roundTrip,
			want: "\n\nInstead: only params can be proposed, with " + propose,
		},
		{
			name:    "another field denied",
			code:    "CHANGE_REVIEW_POLICY_DENIED",
			ctx:     `{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["state"]]}`,
			spec:    roundTrip,
			want:    "\n\nNext: " + sentNote + policy,
			notWant: []string{"Instead:", "table-rule propose " + testTableRuleID},
		},
		{
			name:    "params denied in a params-only patch",
			code:    "CHANGE_REVIEW_POLICY_DENIED",
			ctx:     `{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["params"]]}`,
			spec:    paramsOnly,
			want:    "\n\nNext: " + policy,
			notWant: []string{"Instead:", "every field the patch sends"},
		},
		{
			name:    "a field that cannot be proposed",
			code:    "CHANGE_REVIEW_POLICY_DENIED",
			ctx:     `{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["state"]]}`,
			spec:    stateOnly,
			want:    "\n\nNext: The policy checks every field the patch sends, unchanged ones included: this one sent state. ",
			notWant: []string{"Instead:"},
		},
		{
			name:    "a refusal about another operation",
			code:    "CHANGE_REVIEW_POLICY_DENIED",
			ctx:     `{"operation":"settings_patch","verb":"resolve","denied_keys":[["anomaly_threshold"]]}`,
			spec:    roundTrip,
			want:    "\nHint: The policy does not let your role resolve settings_patch key anomaly_threshold. ",
			notWant: []string{"Instead:", "Next:"},
		},
		{
			name:    "malformed context",
			code:    "CHANGE_PROPOSAL_REQUIRED",
			ctx:     `[]`,
			spec:    paramsOnly,
			want:    "\nHint: Propose the change for review instead",
			notWant: []string{"Instead:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()

			registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{"threshold": map[string]interface{}{"type": "number"}},
					}))
				})
			mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"detail":{"msg":"This table's change review policy refuses this change.","error_code":"` +
						tc.code + `","context":` + tc.ctx + `}}`))
				})

			manifest := "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: " + testTableRuleID +
				"\nspec:\n  dataset_id: 1\n" + tc.spec
			_, err := execApply(t, env.CreateFile("table-rule.yaml", manifest), "--yes")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
				t.Fatalf("exit code = %d, want %d: %v", code, qerrors.ExitUnauthorized, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error lacks %q:\n%v", tc.want, err)
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(err.Error(), notWant) {
					t.Errorf("error has %q:\n%v", notWant, err)
				}
			}
		})
	}
}
