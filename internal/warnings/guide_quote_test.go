package warnings

import (
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
)

// Pins the NEXT ACTIONS footer quoted under triage-alerts "## Act on a warning".
// The topic quotes it as a terminal renders it, with no --yes on the
// ignore_column_for_future_anomalies line.
func TestGuideQuoteWarningNextActions(t *testing.T) {
	resolved := false
	actions := []api.WarningAction{api.NotAnomaly, api.IgnoreColumnForFutureAnomalies}
	guidetest.AssertQuoted(t, "triage-alerts", RenderNextActions(&WarningFull{
		ID:                 55,
		DatasetID:          42,
		IssueType:          api.IssueTypeEnum("anomaly"),
		Resolved:           &resolved,
		WarningActionsList: &actions,
	}, atTerminal))
}

// Pins the footer quoted under triage-alerts "## When the policy says
// propose": the same warning for a caller whose table's change review policy
// lets them propose not_anomaly and do neither with the other action.
func TestGuideQuoteWarningNextActionsUnderAPolicy(t *testing.T) {
	resolved := false
	actions := []api.WarningAction{api.NotAnomaly, api.IgnoreColumnForFutureAnomalies}
	modes := map[string]api.ActionMode{
		string(api.NotAnomaly):                     api.ActionModePropose,
		string(api.IgnoreColumnForFutureAnomalies): api.ActionModeNone,
	}
	guidetest.AssertQuoted(t, "triage-alerts", RenderNextActions(&WarningFull{
		ID:                 55,
		DatasetID:          42,
		IssueType:          api.IssueTypeEnum("anomaly"),
		Resolved:           &resolved,
		WarningActionsList: &actions,
		ActionModes:        &modes,
	}, atTerminal))
}
