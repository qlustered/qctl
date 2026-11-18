package review

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

func sampleDecisionResult(decision api.ReviewDecisionEnum, completed bool, status api.ReviewRequestStatus) api.SubmitDecisionResponseSchema {
	return api.SubmitDecisionResponseSchema{
		Decision: api.ReviewDecisionResponseSchema{
			ID:         uuid.MustParse("0190dddd-1111-2222-3333-444444444444"),
			ReviewID:   testReviewUUID,
			ReviewerID: testReviewerUUID,
			Decision:   decision,
			CreatedAt:  time.Now(),
		},
		ReviewCompleted: completed,
		ReviewStatus:    status,
	}
}

// registerDecisionEndpoints mocks POST …/decisions (capturing the wire body)
// and GET …/rule-reviews/<id> (the approvals enrichment read).
func registerDecisionEndpoints(mock *testutil.MockAPIServer, gotBody *map[string]interface{},
	result api.SubmitDecisionResponseSchema, detail api.ReviewDetailResponseSchema) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/decisions",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(gotBody)
			testutil.RespondJSON(w, http.StatusCreated, result)
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})
}

func TestDecisionSubcommandsSendTheRightEnum(t *testing.T) {
	cases := []struct {
		subcommand string
		wireEnum   string
		label      string
	}{
		{"approve", "approve", "approved"},
		{"reject", "reject", "rejected"},
		{"request-changes", "request_changes", "changes requested"},
	}
	for _, tc := range cases {
		t.Run(tc.subcommand, func(t *testing.T) {
			mock := newReviewTestEnv(t)
			var gotBody map[string]interface{}
			registerDecisionEndpoints(mock, &gotBody,
				sampleDecisionResult(api.ReviewDecisionEnum(tc.wireEnum), false, api.ReviewRequestStatusOpen),
				sampleReviewDetail(api.ReviewRequestStatusOpen))

			out, err := runReviewCommand(t, "",
				"review", tc.subcommand, testReviewUUID.String(), "--comment", "noted")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotBody["decision"] != tc.wireEnum {
				t.Errorf("wire decision = %v, want %s", gotBody["decision"], tc.wireEnum)
			}
			if gotBody["comment"] != "noted" {
				t.Errorf("wire comment = %v, want noted", gotBody["comment"])
			}
			want := "rule-review/" + testReviewUUID.String() + " " + tc.label + " (status: open, approvals: 0/2)"
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q\n%s", want, out)
			}
			if !strings.Contains(out, "Next: qctl describe rule-review "+testReviewUUID.String()) {
				t.Errorf("missing next hint\n%s", out)
			}
		})
	}
}

func TestApproveCompletedOutputAndEnableHint(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	detail := sampleReviewDetail(api.ReviewRequestStatusApproved)
	detail.CurrentApprovals = 2
	registerDecisionEndpoints(mock, &gotBody,
		sampleDecisionResult(api.ReviewDecisionEnumApprove, true, api.ReviewRequestStatusApproved), detail)

	out, err := runReviewCommand(t, "", "review", "approve", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "rule-review/" + testReviewUUID.String() + " approved (status: approved, approvals: 2/2, review completed)"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
	hint := "If auto-enable is off or the dry-run gate failed, enable manually: qctl enable rule dedupe-emails --release 2.1.0"
	if !strings.Contains(out, hint) {
		t.Errorf("output missing auto-enable caveat\n%s", out)
	}
	// The empty --comment must be omitted from the wire, not sent as "".
	if gotBody["comment"] != nil {
		t.Errorf("wire comment = %v, want null", gotBody["comment"])
	}
}

func TestRejectCompletedHasNoEnableHint(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	registerDecisionEndpoints(mock, &gotBody,
		sampleDecisionResult(api.ReviewDecisionEnumReject, true, api.ReviewRequestStatusRejected),
		sampleReviewDetail(api.ReviewRequestStatusRejected))

	out, err := runReviewCommand(t, "", "review", "reject", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "rejected (status: rejected, approvals: 0/2, review completed)") {
		t.Errorf("missing rejected line\n%s", out)
	}
	if strings.Contains(out, "auto-enable") {
		t.Errorf("reject must not print the enable hint\n%s", out)
	}
}

func TestDecisionDegradesWithoutApprovalsWhenGetFails(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/decisions",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated,
				sampleDecisionResult(api.ReviewDecisionEnumApprove, false, api.ReviewRequestStatusOpen))
		})
	// The enrichment read fails deterministically → the line degrades.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "boom"},
			})
		})

	out, err := runReviewCommand(t, "", "review", "approve", testReviewUUID.String())
	if err != nil {
		t.Fatalf("a decision must not fail over the enrichment read: %v", err)
	}
	if !strings.Contains(out, "rule-review/"+testReviewUUID.String()+" approved (status: open)") {
		t.Errorf("missing degraded line\n%s", out)
	}
	if strings.Contains(out, "approvals:") {
		t.Errorf("degraded line must omit the approvals segment\n%s", out)
	}
}

func TestDecisionForbiddenExplainsAgentRole(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/decisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Permission denied"},
			})
		})

	_, err := runReviewCommand(t, "", "review", "approve", testReviewUUID.String())
	if err == nil || !strings.Contains(err.Error(), "a human renders the verdict") {
		t.Errorf("want agent-role explanation on 403, got %v", err)
	}
}

func TestApproverNotDesignatedMapping(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/decisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "Reviewer is not designated",
					"error_code": "APPROVER_NOT_DESIGNATED",
				},
			})
		})

	_, err := runReviewCommand(t, "", "review", "approve", testReviewUUID.String())
	if err == nil || !strings.Contains(err.Error(), "designates specific approvers") {
		t.Errorf("want designated-approver mapping, got %v", err)
	}
}

// The enable hint's rule name and release are the server's text, echoed to
// stdout.
func TestApproveHintSanitizesTheRuleName(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	detail := sampleReviewDetail(api.ReviewRequestStatusApproved)
	detail.RuleRevision.Name = "dedupe\x1b]0;pwned\x07"
	detail.RuleRevision.Release = "2.1.0\x00"
	result := sampleDecisionResult(api.ReviewDecisionEnumApprove, true, api.ReviewRequestStatusApproved)
	registerDecisionEndpoints(mock, &gotBody, result, detail)

	out, err := runReviewCommand(t, "", "review", "approve", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.ContainsAny(out, "\x1b\x00\x07") {
		t.Fatalf("server text reached the terminal with a live control byte: %q", out)
	}
	if want := `qctl enable rule dedupe\x1b]0;pwned\x07 --release 2.1.0\x00`; !strings.Contains(out, want) {
		t.Errorf("missing the visible escape %q: %q", want, out)
	}
}

// The decision line's status is the server's text, echoed to stdout.
func TestDecisionSanitizesTheServerStatus(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	registerDecisionEndpoints(mock, &gotBody,
		sampleDecisionResult(api.ReviewDecisionEnumReject, false, "open\x1b[31m"), sampleReviewDetail(api.ReviewRequestStatusOpen))
	out, err := runReviewCommand(t, "", "review", "reject", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.ContainsRune(out, '\x1b') || !strings.Contains(out, `(status: open\x1b[31m`) {
		t.Errorf("status not sanitized: %q", out)
	}
}
