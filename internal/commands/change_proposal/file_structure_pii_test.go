package change_proposal_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestFileStructurePiiRefusalsExplainAccessWorkflow(t *testing.T) {
	for _, issueType := range []api.AlertType{api.AlertTypeCantSniffHeader, api.AlertTypeMultipleSheetsFound} {
		for _, verb := range []string{"resolve", "resolve-file", "propose", "propose-file", "edit", "submit"} {
			t.Run(string(issueType)+"/"+verb, func(t *testing.T) {
				mock := newEnv(t)
				config.SetContextOverride("default")
				t.Cleanup(func() { config.SetContextOverride("") })
				a := anomalyAlert(api.AlertActionTrashFile)
				a.IssueType = issueType
				serveJSON(mock, "GET", alertURL, http.StatusOK, a)
				p := proposal(api.ChangeProposalStateDraft, 1)
				p.Payload = map[string]interface{}{"action": "trash_file"}
				serveJSON(mock, "GET", proposalURL, http.StatusOK, change_proposals.Detail{Proposal: p})

				method, path := "POST", proposalsURL
				var args []string
				switch verb {
				case "resolve", "propose":
					args = []string{"alert", verb, "9", "--action", "trash_file", "--comment", "unreadable"}
				case "resolve-file", "propose-file":
					file := writeFile(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: \"9\"\nspec:\n  action: trash_file\n  comment: unreadable\n")
					args = []string{"alert", strings.TrimSuffix(verb, "-file"), "-f", file}
				case "edit":
					method, path = "PATCH", proposalURL
					args = []string{"change-proposal", "edit", proposalID.String(), "--note", "Inspect file structure"}
				case "submit":
					path = proposalURL + "/submit"
					args = []string{"change-proposal", "submit", proposalID.String()}
				}
				if strings.HasPrefix(verb, "resolve") {
					path = "/api/orgs/" + testOrgID + "/alerts/resolve/9"
					args = append(args, "--yes")
				}
				serveJSON(mock, method, path, http.StatusForbidden, apiError("PII_ACCESS_REQUIRED", "An unmasked preview is required.", nil))

				stdout, _, err := run(t, args...)
				if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
					t.Fatalf("exit code = %d, want 4: %v", code, err)
				}
				for _, want := range []string{"PII_ACCESS_REQUIRED", "qctl pii-access-request request --table-id", "qctl get pii-access-requests --table-id", "wait for approval"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error lacks %q: %v", want, err)
					}
				}
				if stdout != "" || strings.Contains(err.Error(), "Scoped to organization") {
					t.Errorf("unexpected output: stdout=%q, error=%v", stdout, err)
				}
				writes := 0
				for _, request := range mock.Requests() {
					if !strings.HasPrefix(request, "GET ") {
						writes++
					}
				}
				if writes != 1 {
					t.Errorf("sent %d writes, want one without retry: %v", writes, mock.Requests())
				}
			})
		}
	}
}
