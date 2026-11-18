package alert

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestAnomalyColumnIgnoreConfirmation(t *testing.T) {
	for _, file := range []bool{false, true} {
		for _, tc := range []struct {
			name, answer string
			yes          bool
			code, posts  int
		}{
			{"no terminal", "", false, 2, 0},
			{"declined", "n\n", false, 1, 0},
			{"confirmed", "y\n", false, 0, 1},
			{"yes flag", "", true, 0, 1},
		} {
			t.Run(tc.name+map[bool]string{false: "/action", true: "/file"}[file], func(t *testing.T) {
				cmdutil.ResetReader()
				t.Cleanup(cmdutil.ResetReader)
				if tc.answer != "" {
					cmdutil.SetReader(strings.NewReader(tc.answer))
				}
				mock := newAlertTestEnv(t)
				a := migrationAlert()
				a.IssueType = api.AlertTypeAnomaly
				a.MigrationModelID = nil
				a.FieldName = testutil.StringPtr("amount")
				actions := []api.AlertAction{api.AlertActionIgnoreColumnForFutureAnomalies}
				a.AlertActionsList = &actions
				registerResolvingGetAlert(mock, a)
				posts := 0
				mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, r *http.Request) {
					posts++
					var body api.AlertResolveSchema
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Action == nil || *body.Action != actions[0] {
						t.Errorf("posted action = %+v, error = %v", body, err)
					}
					testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
				})
				args := []string{"alert", "resolve", "9", "--action", string(actions[0])}
				if file {
					path := writeManifest(t, alerts.Skeleton(&a, string(actions[0]), nil))
					args = []string{"alert", "resolve", "-f", path}
				}
				if tc.yes {
					args = append(args, "--yes")
				}
				out, err := runAlertCommand(t, args...)
				if code := qerrors.GetExitCode(err); code != tc.code || posts != tc.posts {
					t.Fatalf("exit=%d posts=%d, want exit=%d posts=%d: %v", code, posts, tc.code, tc.posts, err)
				}
				if tc.code == 2 && !strings.Contains(err.Error(), "--yes") {
					t.Errorf("refusal has no confirmation hint: %v", err)
				}
				if tc.posts == 1 && !strings.Contains(out, "qctl get alerts --table-id 42 --issue-type anomaly --resolved") {
					t.Errorf("missing bulk-resolution follow-up: %s", out)
				}
				if tc.answer != "" && (!strings.Contains(out, "Permanently ignore") || !strings.Contains(out, "same column and settings model")) {
					t.Errorf("missing consequence in prompt: %s", out)
				}
			})
		}
	}
}
