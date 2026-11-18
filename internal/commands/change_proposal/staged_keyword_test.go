package change_proposal_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const keywordSuggestionID = "33333333-3333-3333-3333-333333333333"

func stagedKeywordProposal(noun string) api.ChangeProposalSchema {
	p := executed()
	p.AuthorIds = []uuid.UUID{}
	p.AuthorUsers = []api.UserInfoTinySchema{}
	p.ObsoleteReasons = []api.ChangeProposalReasonSchema{}
	p.PolicyKeys = [][]string{}
	p.Payload = map[string]interface{}{"action": "validation_add_a_new_keyword", "new_value": "replacement"}
	if noun == "warning" {
		p.Operation, p.AlertItemID, p.WarningItemID = api.ChangeReviewOperationWarningResolve, nil, intPtr(55)
	}
	p.ExecutionResult = &map[string]interface{}{
		"response": map[string]interface{}{"dataset_rule_suggestion_id": keywordSuggestionID},
		"partial":  false, "target_state": map[string]interface{}{"resolved": false},
	}
	return p
}

func TestApproveReportsStagedKeywordSuggestions(t *testing.T) {
	for _, noun := range []string{"alert", "warning"} {
		for _, kind := range []string{"migration", "candidate", "unreadable"} {
			for _, format := range []string{"table", "json", "yaml", "name"} {
				t.Run(noun+"/"+kind+"/"+format, func(t *testing.T) {
					mock := newEnv(t)
					p := stagedKeywordProposal(noun)
					read := p
					read.State = api.ChangeProposalStatePendingReview
					read.ExecutionResult = nil
					serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, read))
					serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, p)
					serveKeywordSuggestion(mock, kind)
					issueID := 9
					if noun == "alert" {
						serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert())
					} else {
						issueID = 55
						serveJSON(mock, "GET", warningURL, http.StatusOK, anomalyWarning())
					}

					stdout, stderr, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes", "-o", format)
					wantExit(t, err, qerrors.ExitSuccess)
					human := stdout
					if format != "table" {
						human = stderr
						if strings.Contains(stdout, "keyword change staged") {
							t.Fatalf("human guidance leaked onto %s stdout: %s", format, stdout)
						}
					}
					for _, want := range []string{
						fmt.Sprintf("%s %d — keyword change staged as rule suggestion %s; the %s stays open", noun, issueID, keywordSuggestionID, noun),
						fmt.Sprintf("qctl describe %s %d", noun, issueID),
						fmt.Sprintf("qctl %s resolve -f <file.yaml> # the same PAYLOAD shown above", noun),
					} {
						if !strings.Contains(human, want) {
							t.Errorf("missing %q:\n%s", want, human)
						}
					}
					accept := "qctl suggestion accept " + keywordSuggestionID + " --table-id 42"
					if kind == "migration" {
						if !strings.Contains(human, accept) || !strings.Contains(human, "Accepting it also resolves the "+noun+" when the keyword is the "+noun+"'s own value") {
							t.Errorf("missing migration guidance:\n%s", human)
						}
					} else {
						if strings.Contains(human, accept) {
							t.Errorf("offered accept for %s:\n%s", kind, human)
						}
						if !strings.Contains(human, "qctl describe rule-suggestion "+keywordSuggestionID+" --table-id 42") {
							t.Errorf("missing suggestion follow command:\n%s", human)
						}
						if kind == "candidate" && !strings.Contains(human, "It cannot be accepted") {
							t.Errorf("missing candidate explanation:\n%s", human)
						}
						if kind == "unreadable" && !strings.Contains(human, "could not read the suggestion") {
							t.Errorf("missing failed read explanation:\n%s", human)
						}
					}
					if format == "json" {
						var got map[string]interface{}
						if err := json.Unmarshal([]byte(stdout), &got); err != nil {
							t.Fatal(err)
						}
						if want := asJSON(t, p); !reflect.DeepEqual(got, want) {
							t.Errorf("%s changed the raw proposal:\ngot %v\nwant %v", format, got, want)
						}
					}
					if format == "yaml" {
						want, err := json.Marshal(p)
						if err != nil {
							t.Fatal(err)
						}
						testutil.AssertYAMLReadsAsJSON(t, stdout, string(want))
					}
					if format == "name" && stdout != "change-proposal/"+proposalID.String()+"\n" {
						t.Errorf("name output = %q", stdout)
					}
				})
			}
		}
	}
}

func serveKeywordSuggestion(mock *testutil.MockAPIServer, kind string) {
	url := "/api/orgs/" + testOrgID + "/datasets/42/rule-suggestions/" + keywordSuggestionID
	if kind == "unreadable" {
		serveJSON(mock, "GET", url, http.StatusNotFound, map[string]interface{}{"detail": "suggestion disappeared"})
		return
	}
	serveJSON(mock, "GET", url, http.StatusOK, map[string]interface{}{
		"id": keywordSuggestionID, "is_revalidation_candidate": kind == "candidate",
	})
}

func TestApproveStagedKeywordIssueRead(t *testing.T) {
	for _, noun := range []string{"alert", "warning"} {
		for _, outcome := range []string{"resolved", "unreadable"} {
			t.Run(noun+"/"+outcome, func(t *testing.T) {
				mock := newEnv(t)
				p := stagedKeywordProposal(noun)
				read := p
				read.State = api.ChangeProposalStatePendingReview
				serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, read))
				serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, p)
				serveKeywordSuggestion(mock, "candidate")
				url := alertURL
				if noun == "warning" {
					url = warningURL
				}
				if outcome == "resolved" {
					serveJSON(mock, "GET", url, http.StatusOK, map[string]interface{}{"resolved": true})
				} else {
					serveJSON(mock, "GET", url, http.StatusForbidden, map[string]interface{}{"detail": "read refused"})
				}
				stdout, _, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes")
				wantExit(t, err, qerrors.ExitSuccess)
				if strings.Contains(stdout, "the "+noun+" stays open") {
					t.Errorf("claimed the issue is still open:\n%s", stdout)
				}
				if outcome == "resolved" {
					if !strings.Contains(stdout, "resolved; its keyword change was staged") || strings.Contains(stdout, "resolve -f") {
						t.Errorf("resolved issue should not need another resolve:\n%s", stdout)
					}
				} else if !strings.Contains(stdout, "could not re-read the "+noun+" to confirm the outcome") {
					t.Errorf("missing uncertain outcome:\n%s", stdout)
				}
			})
		}
	}
}

func TestApproveUsesRecordedStagingKindWithoutLookup(t *testing.T) {
	for _, noun := range []string{"alert", "warning", "rule"} {
		for _, kind := range []string{"draft_migration", "revalidation_candidate"} {
			for _, format := range []string{"table", "json", "yaml", "name"} {
				t.Run(noun+"/"+kind+"/"+format, func(t *testing.T) {
					mock := newEnv(t)
					p := stagedKeywordProposal(noun)
					if noun == "rule" {
						p = ruleProposal(api.ChangeProposalStateExecuted, 5)
					}
					p.AuthorIds = []uuid.UUID{}
					p.AuthorUsers = []api.UserInfoTinySchema{}
					p.ObsoleteReasons = []api.ChangeProposalReasonSchema{}
					p.PolicyKeys = [][]string{}
					p.ExecutionResult = &map[string]interface{}{
						"outcome": "staged", "application": "pending", "staged_as": kind,
						"dataset_rule_suggestion_id": keywordSuggestionID,
					}
					read := p
					read.State, read.ExecutionResult = api.ChangeProposalStatePendingReview, nil
					serveJSON(mock, "GET", proposalURL, http.StatusOK, detail(t, read))
					serveJSON(mock, "POST", proposalURL+approveURL, http.StatusOK, p)
					stdout, stderr, err := run(t, "change-proposal", "approve", proposalID.String(), "--yes", "-o", format)
					wantExit(t, err, qerrors.ExitSuccess)
					human := stdout
					if format != "table" {
						human = stderr
						if strings.Contains(stdout, "Staged as:") {
							t.Errorf("explanation leaked into %s stdout: %s", format, stdout)
						}
					}
					for _, want := range []string{"Staged as: " + kind, "qctl describe rule-suggestion " + keywordSuggestionID + " --table-id 42"} {
						if !strings.Contains(human, want) {
							t.Errorf("missing %q: %s", want, human)
						}
					}
					if kind == "revalidation_candidate" && strings.Contains(human, "it applies when the suggestion is accepted") {
						t.Errorf("candidate offered acceptance: %s", human)
					}
					if len(mock.Requests()) != 2 {
						t.Errorf("extra lookup: %v", mock.Requests())
					}
					if format == "json" {
						var got map[string]interface{}
						if err := json.Unmarshal([]byte(stdout), &got); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(got, asJSON(t, p)) {
							t.Errorf("raw proposal changed: %v", got)
						}
					}
				})
			}
		}
	}
}
