package prompt_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/qlustered/qctl/internal/warnings"
)

func TestResolutionAndPrivilegeSuggestionsUseThePromptReader(t *testing.T) {
	a := &alerts.AlertFull{ID: 9}
	w := &warnings.WarningFull{ID: 55}
	request := &privilege_requests.Detail{ID: uuid.New(), Status: api.PrivilegeRequestStatusPending}
	for _, interactive := range []bool{false, true} {
		t.Run(fmt.Sprint(interactive), func(t *testing.T) {
			cmdutil.ResetReader()
			if interactive {
				cmdutil.SetReader(strings.NewReader("y\n"))
			}
			t.Cleanup(cmdutil.ResetReader)
			suffix := " --yes"
			if interactive {
				suffix = ""
			}
			for _, tc := range []struct {
				text, command, wantSuffix string
			}{
				{alerts.Skeleton(a, "apply_migration", nil), "qctl alert resolve -f <file>", suffix},
				{alerts.Skeleton(a, "ignore_column_for_future_anomalies", nil), "qctl alert resolve -f <file>", suffix},
				{alerts.Skeleton(a, "map_value", nil), "qctl alert resolve -f <file>", ""},
				{warnings.Skeleton(w, "ignore_column_for_future_anomalies"), "qctl warning resolve -f <file>", suffix},
				{warnings.Skeleton(w, "map_value"), "qctl warning resolve -f <file>", ""},
				{privilege_requests.RenderDetail(request), "qctl privilege approve " + request.ID.String(), suffix},
				{privilege_requests.RenderDetail(request), "qctl privilege reject " + request.ID.String(), ""},
			} {
				if !strings.Contains(tc.text, tc.command+tc.wantSuffix+"\n") {
					t.Errorf("want complete suggestion %q in:\n%s", tc.command+tc.wantSuffix, tc.text)
				}
			}
			actions := []api.AlertAction{api.AlertActionIgnoreColumnForFutureAnomalies, api.AlertActionNotAnomaly}
			a.AlertActionsList = &actions
			footer := alerts.RenderNextActionsPlain(a, cmdutil.CanPrompt())
			for _, want := range []string{
				"qctl alert resolve 9 --action ignore_column_for_future_anomalies" + suffix + "\n",
				"qctl alert resolve 9 --action not_anomaly\n",
				"BULK", "same column and settings model",
			} {
				if !strings.Contains(footer, want) {
					t.Errorf("missing %q from footer:\n%s", want, footer)
				}
			}
		})
	}
}
