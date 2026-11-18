package alert

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// routingAlert is an unresolved series_file_routing alert 9 on the template
// table 42, offering the two actions the issue type declares.
func routingAlert() alerts.AlertFull {
	resolved := false
	var reason api.AlertBodySchema_Reason
	_ = reason.FromSeriesFileRoutingReason(api.PeriodLocked)
	period := "2026-06"
	list := []api.AlertAction{api.AlertActionAssignReportingPeriod, api.AlertActionTryAgain}
	return alerts.AlertFull{
		ID:               9,
		DatasetID:        42,
		DatasetName:      "monthly_template",
		IssueType:        api.AlertTypeSeriesFileRouting,
		Resolved:         &resolved,
		AlertActionsList: &list,
		Body:             &api.AlertBodySchema{Reason: &reason, PeriodKey: &period},
	}
}

const assignPeriodManifest = "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: \"9\"\n" +
	"spec:\n  action: assign_reporting_period\n  new_value: \"2026-07\"\n"

// The generator leaves new_value bare, so an unedited manifest is refused, and
// carries the action's note; the edited manifest then reaches the wire with
// the chosen period key. Dropping new_value from ToRequest, or a regen that
// stops requiring it, must make this fail.
func TestAssignReportingPeriodDryRunThenFile(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, routingAlert())
	posts := 0
	var sent api.AlertResolveSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			posts++
			_ = json.NewDecoder(r.Body).Decode(&sent)
			okResolve(w, r)
		})

	skeleton, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "assign_reporting_period", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if posts != 0 {
		t.Fatalf("--dry-run sent %d resolve request(s)", posts)
	}
	for _, want := range []string{"action: assign_reporting_period\n", "  new_value:\n", "# note from the backend: new_value is the key of the reporting period"} {
		if !strings.Contains(skeleton, want) {
			t.Errorf("skeleton lacks %q:\n%s", want, skeleton)
		}
	}
	_, err = runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, skeleton))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("unedited skeleton: exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
	}
	if posts != 0 {
		t.Fatalf("an unedited skeleton was sent")
	}

	if _, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, assignPeriodManifest)); err != nil {
		t.Fatalf("resolve -f: %v", err)
	}
	if posts != 1 {
		t.Fatalf("resolve requests = %d, want 1", posts)
	}
	if sent.Action == nil || *sent.Action != api.AlertActionAssignReportingPeriod {
		t.Errorf("action did not reach the wire: %+v", sent)
	}
	if sent.NewValue == nil || *sent.NewValue != "2026-07" {
		t.Errorf("new_value did not reach the wire as \"2026-07\": %+v", sent)
	}
}

// Each refusal POST .../alerts/resolve/{id} answers for assign_reporting_period
// keeps its status's exit code through the note and hint qctl appends.
func TestAssignReportingPeriodRefusalsKeepTheirExitCode(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		detail   map[string]any
		wantCode int
		want     []string
		oneHint  bool
	}{
		{
			name:   "400 at body.new_value",
			status: http.StatusBadRequest,
			detail: map[string]any{
				"msg": "The table series has no active period '2026-13'.",
				"loc": []string{"body", "new_value"},
			},
			wantCode: qerrors.ExitBadUsage,
			want:     []string{"Field: new_value", "note for action assign_reporting_period"},
		},
		{
			name:     "403 without a grant on the period's table",
			status:   http.StatusForbidden,
			detail:   map[string]any{"msg": "You do not have access to this table."},
			wantCode: qerrors.ExitUnauthorized,
			want:     []string{"note for action assign_reporting_period"},
		},
		{
			name:   "409 PERIOD_LOCKED",
			status: http.StatusConflict,
			detail: map[string]any{
				"msg":        "Period '2026-07' is locked. Unlock it before changing its data.",
				"error_code": "PERIOD_LOCKED",
			},
			wantCode: qerrors.ExitBadUsage,
			want: []string{
				testutil.PeriodLockedHintLead,
				"\n\nInstead: set spec.new_value to an unlocked period of the table series and resolve again. " +
					"The locked period is the one spec.new_value names, and it takes no data",
			},
			oneHint: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerGetAlert(mock, routingAlert())
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": tc.detail})
				})
			_, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, assignPeriodManifest))
			if err == nil {
				t.Fatal("want the backend's refusal")
			}
			if code := qerrors.GetExitCode(err); code != tc.wantCode {
				t.Errorf("exit code = %d, want %d: %v", code, tc.wantCode, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%v", want, err)
				}
			}
			if tc.oneHint {
				testutil.AssertOnePeriodUnlockHint(t, err)
			}
		})
	}
}

// send_held_file_to_period sends the file to the period spec.new_value names,
// so its PERIOD_LOCKED is about that period, not the alert's own.
func TestPeriodLockedOnSendHeldFileToPeriodNamesNewValue(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := routingAlert()
	alert.IssueType = api.AlertTypePreloadCheckHeld
	list := []api.AlertAction{api.AlertActionSendHeldFileToPeriod}
	alert.AlertActionsList = &list
	registerGetAlert(mock, alert)
	register409(mock, "PERIOD_LOCKED", "Period '2026-07' is locked. Unlock it before changing its data.")
	manifest := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: \"9\"\n" +
		"spec:\n  action: send_held_file_to_period\n  new_value: \"2026-07\"\n"
	_, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, manifest))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
	}
	if want := "\n\nInstead: set spec.new_value to an unlocked period of the table series and resolve again. " +
		"The locked period is the one spec.new_value names, and it takes no data"; !strings.Contains(err.Error(), want) {
		t.Errorf("error lacks %q:\n%v", want, err)
	}
	if strings.Contains(err.Error(), "the alert's own") {
		t.Errorf("a send_held_file_to_period refusal must not blame the alert's own period:\n%v", err)
	}
	testutil.AssertOnePeriodUnlockHint(t, err)
}

// A PERIOD_LOCKED refusal of any other action names the periods it can be
// about rather than spec.new_value, which that action does not send.
func TestPeriodLockedOnAnotherActionDoesNotNameNewValue(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, routingAlert())
	register409(mock, "PERIOD_LOCKED", "Period '2026-06' is locked. Unlock it before changing its data.")
	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "try_again")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
	}
	if want := "\n\nNote: the locked period is the alert's own, or that of a file or ingestion job the alert holds. " +
		"Resolving with try_again writes into that period, so it cannot succeed while the period stays locked"; !strings.Contains(err.Error(), want) {
		t.Errorf("error lacks %q:\n%v", want, err)
	}
	testutil.AssertOnePeriodUnlockHint(t, err)
	if strings.Contains(err.Error(), "spec.new_value") {
		t.Errorf("a try_again refusal must not point at spec.new_value:\n%v", err)
	}
}
