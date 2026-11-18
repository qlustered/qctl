package warning

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestWarningResolveChangeReviewRefusals(t *testing.T) {
	const (
		anomalyCtx = `{"operation":"warning_resolve","verb":"resolve","denied_keys":[["anomaly","not_anomaly"]]}`
		mapCtx     = `{"operation":"warning_resolve","verb":"resolve","denied_keys":[["anomaly","map_value"]]}`
		ruleCtx    = `{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["params"]]}`
		mapValue   = "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  new_value: \"1420.00\"\n"
	)
	cases := []struct {
		name     string
		code     string
		ctx      string
		manifest string
		want     []string
		notWant  []string
	}{
		{
			name: "proposal required for a parameterless action",
			code: "CHANGE_PROPOSAL_REQUIRED", ctx: anomalyCtx,
			want: []string{
				"\nHint: Propose the resolution for review instead: `qctl warning propose <warning-id> --action <action>`",
				"\n\nInstead: propose it for review with `qctl warning propose 55 --action not_anomaly`; it runs when an approver approves it",
			},
		},
		{
			name: "proposal required for an action that needs a manifest",
			code: "CHANGE_PROPOSAL_REQUIRED", ctx: mapCtx, manifest: mapValue,
			want: []string{
				"\n\nInstead: propose it for review with `qctl warning propose -f <file>`, which takes the same WarningResolution manifest; " +
					"it runs when an approver approves it. `qctl warning propose 55 --action map_value --dry-run` writes one",
			},
		},
		{
			name: "policy denied",
			code: "CHANGE_REVIEW_POLICY_DENIED", ctx: anomalyCtx,
			want: []string{
				"\nHint: The policy does not let your role resolve warning_resolve key anomaly/not_anomaly. ",
				"\n\nNext: `qctl get change-review-policy --table-id 42` shows the roles this table's policy lists for each warning_resolve key",
			},
			notWant: []string{"Instead:"},
		},
		{
			name: "proposal required under another operation",
			code: "CHANGE_PROPOSAL_REQUIRED", ctx: ruleCtx,
			want:    []string{"\nHint: Propose the params change for review instead"},
			notWant: []string{"Instead:", "qctl warning propose"},
		},
		{
			name: "policy denied with no context",
			code: "CHANGE_REVIEW_POLICY_DENIED", ctx: "null",
			want:    []string{"\nHint: `qctl get change-review-policy --table <table>`"},
			notWant: []string{"Next:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newWarningTestEnv(t)
			registerGetWarning(mock, anomalyWarning(api.NotAnomaly, api.MapValue))
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"detail":{"msg":"This table's change review policy refuses this change.","error_code":"` +
						tc.code + `","context":` + tc.ctx + `}}`))
				})
			args := []string{"warning", "resolve", "55", "--action", "not_anomaly", "--yes"}
			if tc.manifest != "" {
				args = []string{"warning", "resolve", "-f", writeWarningManifest(t, tc.manifest), "--yes"}
			}
			_, _, err := runWarningCommandSplit(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
				t.Fatalf("exit code = %d, want %d: %v", code, qerrors.ExitUnauthorized, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%v", want, err)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(err.Error(), notWant) {
					t.Errorf("error has %q:\n%v", notWant, err)
				}
			}
		})
	}
}
