package apply

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestApplyTableRulePeriodLockedRefusals(t *testing.T) {
	for _, mode := range []string{"instantiate", "patch"} {
		for _, format := range []string{"table", "json"} {
			t.Run(mode+"/"+format, func(t *testing.T) {
				env, mock := setupTableRuleApplyEnv(t)
				t.Cleanup(env.Cleanup)
				t.Cleanup(mock.Close)
				registerRuleRevisionDetails(mock)
				method, path := "POST", waitInstantiate
				doc := tableRuleInstantiateDoc("email_check")
				if mode == "patch" {
					registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())
					method = "PATCH"
					path = "/api/orgs/" + testOrgID + "/datasets/1/dataset-rules/" + testTableRuleID
					doc = tableRulePatchDoc("renamed")
				}
				const message = "Period '2026-09' is locked. Unlock it before changing its data."
				mock.RegisterHandler(method, path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusConflict, map[string]any{
						"detail": map[string]any{"msg": message, "error_code": apierror.CodePeriodLocked},
					})
				})

				stdout, _, err := execApplyRoot(t, "apply", "-f", env.CreateFile("rule.yaml", doc), "--yes", "-o", format)
				requireExit(t, err, qerrors.ExitBadUsage)
				if code := apierror.ErrorCodeOf(err); code != apierror.CodePeriodLocked {
					t.Errorf("error code = %q, want PERIOD_LOCKED", code)
				}
				for _, want := range []string{
					message,
					"Hint: An account with deliverable:admin can run",
					"qctl table-series unlock <series> --period <key> --yes",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error lacks %q:\n%v", want, err)
					}
				}
				if format == "json" && stdout != "" {
					t.Errorf("refused apply wrote to JSON stdout: %s", stdout)
				}
			})
		}
	}
}
