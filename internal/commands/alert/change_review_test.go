package alert

import (
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// registerChangeReview403 answers the resolve of alert 9 with a change review
// 403 carrying contextJSON.
func registerChangeReview403(mock *testutil.MockAPIServer, code, contextJSON string) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":{"msg":"This table's change review policy refuses this change.","error_code":"` +
				code + `","context":` + contextJSON + `}}`))
		})
}

func TestAlertResolveChangeReviewRefusals(t *testing.T) {
	const (
		routingCtx = `{"operation":"alert_resolve","verb":"resolve","denied_keys":[["series_file_routing","try_again"]]}`
		ruleCtx    = `{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["params"]]}`
	)
	cases := []struct {
		name    string
		code    string
		ctx     string
		args    []string
		want    []string
		notWant []string
	}{
		{
			name: "proposal required for a parameterless action",
			code: "CHANGE_PROPOSAL_REQUIRED", ctx: routingCtx,
			args: []string{"alert", "resolve", "9", "--action", "try_again"},
			want: []string{
				"\nHint: Propose the resolution for review instead: `qctl alert propose <alert-id> --action <action>`",
				"\n\nInstead: propose it for review with `qctl alert propose 9 --action try_again`; it runs when an approver approves it",
			},
		},
		{
			name: "proposal required for an action that needs a manifest",
			code: "CHANGE_PROPOSAL_REQUIRED", ctx: `{"operation":"alert_resolve","verb":"resolve","denied_keys":[["series_file_routing","assign_reporting_period"]]}`,
			args: []string{"alert", "resolve", "-f", ""},
			want: []string{
				"\n\nInstead: propose it for review with `qctl alert propose -f <file>`, which takes the same AlertResolution manifest; " +
					"it runs when an approver approves it. `qctl alert propose 9 --action assign_reporting_period --dry-run` writes one",
			},
		},
		{
			name: "policy denied",
			code: "CHANGE_REVIEW_POLICY_DENIED", ctx: routingCtx,
			args: []string{"alert", "resolve", "9", "--action", "try_again"},
			want: []string{
				"\nHint: The policy does not let your role resolve alert_resolve key series_file_routing/try_again. ",
				"\n\nNext: `qctl get change-review-policy --table-id 42` shows the roles this table's policy lists for each alert_resolve key",
			},
			notWant: []string{"Instead:"},
		},
		{
			// An implied key: the refusal is about the rule's params, not the
			// alert, so proposing the alert is not the command-level answer.
			name: "proposal required under another operation",
			code: "CHANGE_PROPOSAL_REQUIRED", ctx: ruleCtx,
			args:    []string{"alert", "resolve", "9", "--action", "try_again"},
			want:    []string{"\nHint: Propose the params change for review instead: `qctl table-rule propose <rule> --table-id <id> --set-param <key>=<value>`."},
			notWant: []string{"Instead:", "qctl alert propose"},
		},
		{
			name: "policy denied under another operation",
			code: "CHANGE_REVIEW_POLICY_DENIED", ctx: ruleCtx,
			args:    []string{"alert", "resolve", "9", "--action", "try_again"},
			want:    []string{"\nHint: The policy does not let your role resolve dataset_rule_patch key params. "},
			notWant: []string{"Next:", "--table-id 42"},
		},
		{
			name: "malformed context",
			code: "CHANGE_PROPOSAL_REQUIRED", ctx: `"alert_resolve"`,
			args:    []string{"alert", "resolve", "9", "--action", "try_again"},
			want:    []string{"\nHint: Propose the change for review instead"},
			notWant: []string{"Instead:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerGetAlert(mock, routingAlert())
			registerChangeReview403(mock, tc.code, tc.ctx)
			args := tc.args
			if args[len(args)-1] == "" {
				args = append(args[:len(args)-1:len(args)-1], writeManifest(t, assignPeriodManifest))
			}
			_, err := runAlertCommand(t, args...)
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
