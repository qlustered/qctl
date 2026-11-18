package alerts

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// lateFileAlertFull is an unresolved late_file alert on cloud source 5 for
// period 2026-09, offering what a non-ops caller is offered: nothing.
func lateFileAlertFull() AlertFull {
	a := sampleAlertFull()
	resolved := false
	source := 5
	period := "2026-09"
	a.ID = 31
	a.IssueType = api.AlertTypeLateFile
	a.Resolved = &resolved
	a.DataSourceModelID = &source
	a.AlertActionsList = &[]api.AlertAction{}
	a.Body = &api.AlertBodySchema{PeriodKey: &period}
	return a
}

// The spec's issue-type map is what offers try_again on a late_file alert; a
// regen that changes it must fail here before it reaches a page.
func TestLateFileOffersOnlyTryAgain(t *testing.T) {
	got := api.IssueTypeAlertActions[string(api.AlertTypeLateFile)]
	if strings.Join(got, ",") != "try_again" {
		t.Errorf("IssueTypeAlertActions[late_file] = %v, want [try_again]", got)
	}
}

// A late_file alert is settled on the intake's board, not by an action, so
// the footer names the board and the waiver whether or not try_again is
// offered.
func TestFooterRoutesALateFileAlertToTheBoardAndTheWaiver(t *testing.T) {
	want := []string{
		"the backend resolves this alert once the period has a loaded file, a nil\n",
		"the intake's submissions: qctl get submission-statuses --cloud-source-id 5\n",
		"waive the period: qctl submission-status waive --cloud-source-id 5 --period 2026-09 --reason <text>\n",
	}
	offering := lateFileAlertFull()
	offering.AlertActionsList = &[]api.AlertAction{api.AlertActionTryAgain}
	for name, a := range map[string]AlertFull{"no actions": lateFileAlertFull(), "try_again": offering} {
		for mode, canPrompt := range terminalModes {
			out := RenderNextActionsPlain(&a, canPrompt)
			for _, line := range want {
				if !strings.Contains(out, line) {
					t.Errorf("%s, %s: footer lacks %q:\n%s", name, mode, line, out)
				}
			}
		}
	}
}

// A period key the shell would split is quoted in the waive line.
func TestFooterQuotesALateFilePeriodKey(t *testing.T) {
	a := lateFileAlertFull()
	period := "FY 2026"
	a.Body.PeriodKey = &period
	if out := RenderNextActionsPlain(&a, atTerminal); !strings.Contains(out, "--period 'FY 2026' --reason") {
		t.Errorf("footer does not quote the period key:\n%s", out)
	}
}

// Without a cloud source or a period there is no board or waiver to name, and
// no other alert type gets the lines.
func TestFooterNamesNoBoardWithoutTheSourceAndPeriod(t *testing.T) {
	noSource := lateFileAlertFull()
	noSource.DataSourceModelID = nil
	noPeriod := lateFileAlertFull()
	noPeriod.Body = nil
	other := lateFileAlertFull()
	other.IssueType = api.AlertTypeSeriesFileRouting
	for name, a := range map[string]AlertFull{"no source": noSource, "no period": noPeriod, "other type": other} {
		if out := RenderNextActionsPlain(&a, atTerminal); strings.Contains(out, "submission") {
			t.Errorf("%s: footer names the board:\n%s", name, out)
		}
	}
}

// period_key says which period is late, so describe shows it at every
// verbosity.
func TestLateFilePeriodKeyIsShownAtDefaultVerbosity(t *testing.T) {
	a := lateFileAlertFull()
	for _, verbosity := range []int{0, 1} {
		if body := APIResponseToManifest(&a, nil, verbosity).Spec.Body; body == nil || body.PeriodKey != "2026-09" {
			t.Errorf("verbosity %d: body = %#v, want period_key 2026-09", verbosity, body)
		}
	}
}

// inbound_email_held offers the two held-email actions in place of try_again.
func TestInboundEmailHeldOffersTheHeldEmailActions(t *testing.T) {
	got := api.IssueTypeAlertActions[string(api.AlertTypeInboundEmailHeld)]
	if strings.Join(got, ",") != "use_held_email_file,reject_held_email_file" {
		t.Errorf("IssueTypeAlertActions[inbound_email_held] = %v, want [use_held_email_file reject_held_email_file]", got)
	}
}

// use_held_email_file and reject_held_email_file say what they settle and
// when they are refused, so NEXT ACTIONS prints the backend's note under each.
func TestFooterPrintsTheHeldEmailActionNotes(t *testing.T) {
	a := sampleAlertFull()
	resolved := false
	a.IssueType = api.AlertTypeInboundEmailHeld
	a.Resolved = &resolved
	a.AlertActionsList = &[]api.AlertAction{api.AlertActionUseHeldEmailFile, api.AlertActionRejectHeldEmailFile}
	for mode, canPrompt := range terminalModes {
		out := RenderNextActionsPlain(&a, canPrompt)
		for _, action := range []string{"use_held_email_file", "reject_held_email_file"} {
			if note := api.AlertActionFields[action].Note; note == "" || !strings.Contains(out, "    "+note+"\n") {
				t.Errorf("%s: footer lacks the %s note:\n%s", mode, action, out)
			}
		}
	}
}
