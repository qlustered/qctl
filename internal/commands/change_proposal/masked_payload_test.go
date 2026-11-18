package change_proposal_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestMetadataOnlyEditOmitsThePayload(t *testing.T) {
	for _, operation := range []api.ChangeReviewOperation{
		api.ChangeReviewOperationAlertResolve,
		api.ChangeReviewOperationWarningResolve,
		api.ChangeReviewOperationDatasetRulePatch,
	} {
		t.Run(string(operation), func(t *testing.T) {
			for _, args := range [][]string{
				{"--submit"},
				{"--also-alert", "10"},
				{"--also-warning", "56"},
				{"--clear-also-alerts"},
				{"--clear-also-warnings"},
				{"--depends-on", holderID},
				{"--remove-depends-on"},
				{"--note", "Reviewed"},
				{"--remove-note"},
			} {
				t.Run(args[0], func(t *testing.T) {
					mock := newEnv(t)
					p := proposal(api.ChangeProposalStateDraft, 2)
					switch operation {
					case api.ChangeReviewOperationWarningResolve:
						p.Operation, p.AlertItemID, p.WarningItemID = operation, nil, intPtr(55)
					case api.ChangeReviewOperationDatasetRulePatch:
						p = ruleProposal(api.ChangeProposalStateDraft, 2)
					}
					current := asJSON(t, p)
					current["masked_fields"] = []string{"customer_email"}
					serveJSON(mock, "GET", proposalURL, http.StatusOK, current)
					got := captureJSON(mock, "PATCH", proposalURL, http.StatusOK, p)

					_, _, err := run(t, append([]string{"change-proposal", "edit", proposalID.String()}, args...)...)
					wantExit(t, err, qerrors.ExitSuccess)
					if _, sent := (*got)["payload"]; sent || (*got)["version_id"] != float64(2) {
						t.Errorf("edit body = %v, want the version and no payload", *got)
					}
				})
			}
		})
	}
}

func TestEditRefusesToMergeMaskedParams(t *testing.T) {
	for _, args := range [][]string{{"--set-param", "threshold=8"}, {"--unset-param", "mode"}} {
		t.Run(args[0], func(t *testing.T) {
			mock := newEnv(t)
			current := asJSON(t, ruleProposal(api.ChangeProposalStateDraft, 2))
			current["masked_fields"] = []string{"customer_email"}
			serveJSON(mock, "GET", proposalURL, http.StatusOK, current)

			_, _, err := run(t, append([]string{"change-proposal", "edit", proposalID.String()}, args...)...)
			wantExit(t, err, qerrors.ExitBadUsage)
			if countRequests(mock, "PATCH") != 0 {
				t.Errorf("sent a PATCH: %v", mock.Requests())
			}
		})
	}
}

func TestEditCanReplaceMaskedPayload(t *testing.T) {
	for _, operation := range []api.ChangeReviewOperation{api.ChangeReviewOperationAlertResolve, api.ChangeReviewOperationWarningResolve} {
		for _, useFile := range []bool{false, true} {
			t.Run(string(operation)+"/file="+map[bool]string{true: "yes", false: "no"}[useFile], func(t *testing.T) {
				mock := newEnv(t)
				p := proposal(api.ChangeProposalStateDraft, 2)
				p.Operation = operation
				doc := mapValueDoc
				if operation == api.ChangeReviewOperationWarningResolve {
					p.AlertItemID, p.WarningItemID = nil, intPtr(55)
					doc = strings.ReplaceAll(strings.ReplaceAll(doc, "AlertResolution", "WarningResolution"), `id: "9"`, `id: "55"`)
					doc = strings.ReplaceAll(doc, "  map_value_once: true\n", "")
					serveJSON(mock, "GET", warningURL, http.StatusOK, anomalyWarning(api.MapValue, api.NotAnomaly))
				} else {
					serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionMapValue, api.AlertActionNotAnomaly))
				}
				current := asJSON(t, p)
				current["masked_fields"] = []string{"customer_email"}
				current["payload"] = map[string]interface{}{"action": "map_value", "new_value": "MASKED_OLD_VALUE"}
				serveJSON(mock, "GET", proposalURL, http.StatusOK, current)
				got := captureJSON(mock, "PATCH", proposalURL, http.StatusOK, p)
				args := []string{"--action", "not_anomaly"}
				if useFile {
					args = []string{"-f", writeFile(t, doc)}
				}
				_, _, err := run(t, append([]string{"change-proposal", "edit", proposalID.String()}, args...)...)
				if err != nil {
					t.Fatal(err)
				}
				payload, _ := (*got)["payload"].(map[string]interface{})
				wantAction, wantValue := "not_anomaly", interface{}(nil)
				if useFile {
					wantAction, wantValue = "map_value", "1420"
				}
				if payload["action"] != wantAction || payload["new_value"] != wantValue || (*got)["version_id"] != float64(2) {
					t.Errorf("edit body = %v", *got)
				}
			})
		}
	}
}
