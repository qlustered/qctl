package alerts

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// routingAlertFull is an unresolved series_file_routing alert offering the
// actions the issue type declares, in the backend's order.
func routingAlertFull() AlertFull {
	a := sampleAlertFull()
	resolved := false
	list := []api.AlertAction{api.AlertActionAssignReportingPeriod, api.AlertActionTryAgain}
	a.ID = 9
	a.IssueType = api.AlertTypeSeriesFileRouting
	a.Resolved = &resolved
	a.AlertActionsList = &list
	return a
}

// The spec's issue-type map is what puts the three actions on a routing alert;
// a regen that drops any must fail here before it reaches a page.
func TestSeriesFileRoutingOffersAssignReportingPeriodTryAgainAndTrashFile(t *testing.T) {
	got := api.IssueTypeAlertActions[string(api.AlertTypeSeriesFileRouting)]
	want := []string{"assign_reporting_period", "try_again", "trash_file"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("IssueTypeAlertActions[series_file_routing] = %v, want %v", got, want)
	}
	fields := api.AlertActionFields["assign_reporting_period"]
	if strings.Join(fields.Required, ",") != "new_value" || len(fields.Optional) != 0 || len(fields.OneOf) != 0 {
		t.Errorf("assign_reporting_period fields = %+v, want exactly required new_value", fields)
	}
}

// assign_reporting_period needs new_value, so its footer line is the --dry-run
// generator naming that field; try_again is parameterless and runs as is.
func TestFooterOffersAssignReportingPeriodAsAGeneratorAndTryAgainAsIs(t *testing.T) {
	a := routingAlertFull()
	for mode, canPrompt := range terminalModes {
		out := RenderNextActionsPlain(&a, canPrompt)
		for _, want := range []string{
			"  qctl alert resolve 9 --action assign_reporting_period --dry-run\n",
			"    needs extra input (new_value)",
			"  qctl alert resolve 9 --action try_again\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: footer lacks %q:\n%s", mode, want, out)
			}
		}
		if strings.Contains(out, "--action assign_reporting_period\n") {
			t.Errorf("%s: footer offers assign_reporting_period without --dry-run:\n%s", mode, out)
		}
	}
}

// reason and period_key say why the file waits and for which period, so
// describe shows them at the default tier, and -v keeps them.
func TestRoutingReasonAndPeriodKeyAreShownAtDefaultVerbosity(t *testing.T) {
	a := routingAlertFull()
	var reason api.AlertBodySchema_Reason
	if err := reason.FromSeriesFileRoutingReason(api.PeriodLocked); err != nil {
		t.Fatal(err)
	}
	period := "2026-06"
	a.Body = &api.AlertBodySchema{Reason: &reason, PeriodKey: &period, FileName: alertPtr("june.csv")}

	for _, verbosity := range []int{0, 1} {
		body := APIResponseToManifest(&a, nil, verbosity).Spec.Body
		if body == nil || body.Reason != "period_locked" || body.PeriodKey != "2026-06" {
			t.Errorf("verbosity %d: body = %#v, want reason period_locked and period_key 2026-06", verbosity, body)
		}
	}
	if body := APIResponseToManifest(&a, nil, 0).Spec.Body; body != nil && body.FileName != "" {
		t.Errorf("default tier leaked a -v body field: %#v", body)
	}

	// A reason with no known period shows the reason alone.
	a.Body = &api.AlertBodySchema{Reason: &reason}
	body := APIResponseToManifest(&a, nil, 0).Spec.Body
	if body == nil || body.Reason != "period_locked" || body.PeriodKey != "" {
		t.Errorf("reason-only body = %#v", body)
	}
}

// The emailed file's sender asked for its period, the periods offered, and why
// nobody was asked say what the question did, so describe shows them at the
// default tier, and -v keeps them. An empty offer is left out.
func TestRoutingQuestionIsShownAtDefaultVerbosity(t *testing.T) {
	a := routingAlertFull()
	var reason api.AlertBodySchema_Reason
	if err := reason.FromSeriesFileRoutingReason(api.NoMatchingPeriod); err != nil {
		t.Fatal(err)
	}
	asked := uuid.MustParse("6f1c2a4e-0d1b-4c8e-9a51-3b7d2e8f4a10")
	offered := []string{"2026-05", "2026-06"}
	a.Body = &api.AlertBodySchema{Reason: &reason, AskedUserID: &asked, OfferedPeriodKeys: &offered}

	for _, verbosity := range []int{0, 1} {
		body := APIResponseToManifest(&a, nil, verbosity).Spec.Body
		if body == nil || body.AskedUserID != asked.String() || !slices.Equal(body.OfferedPeriodKeys, offered) {
			t.Errorf("verbosity %d: body = %#v, want asked_user_id %s and offered_period_keys %v",
				verbosity, body, asked, offered)
		}
	}

	notAsked := api.OutboundEmailSkipReasonNotAMember
	empty := []string{}
	a.Body = &api.AlertBodySchema{Reason: &reason, NotAskedReason: &notAsked, OfferedPeriodKeys: &empty}
	body := APIResponseToManifest(&a, nil, 0).Spec.Body
	if body == nil || body.NotAskedReason != "not_a_member" || body.OfferedPeriodKeys != nil {
		t.Errorf("not-asked body = %#v, want not_asked_reason not_a_member and no offered_period_keys", body)
	}
}
