package review

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestWithdrawCommand(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/withdraw-decision",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleReviewDetail(api.ReviewRequestStatusOpen))
		})

	out, err := runReviewCommand(t, "", "review", "withdraw", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "rule-review/" + testReviewUUID.String() + " decision withdrawn (status: open)"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
	if !strings.Contains(out, "Next: qctl describe rule-review "+testReviewUUID.String()) {
		t.Errorf("missing next hint\n%s", out)
	}
}

func TestWithdrawReopenBlockedMapping(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/withdraw-decision",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "Cannot reopen: the rule revision is enabled",
					"error_code": "REVIEW_REOPEN_BLOCKED",
					"context":    map[string]interface{}{"code": "review_reopen_blocked"},
				},
			})
		})

	_, err := runReviewCommand(t, "", "review", "withdraw", testReviewUUID.String())
	if err == nil || !strings.Contains(err.Error(), "disable it first: qctl disable rule") {
		t.Errorf("want reopen-blocked mapping, got %v", err)
	}
}

func TestWithdrawReopenBlockedAliasOnly(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/withdraw-decision",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":     "Cannot reopen: the rule revision is enabled",
					"context": map[string]interface{}{"code": "review_reopen_blocked_rule_enabled"},
				},
			})
		})

	_, err := runReviewCommand(t, "", "review", "withdraw", testReviewUUID.String())
	if err == nil || !strings.Contains(err.Error(), "disable it first: qctl disable rule") {
		t.Errorf("want reopen-blocked alias mapping, got %v", err)
	}
}

func TestWithdrawNoStandingDecisionPassesThrough(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/withdraw-decision",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "No standing decision to withdraw"},
			})
		})

	_, err := runReviewCommand(t, "", "review", "withdraw", testReviewUUID.String())
	if err == nil || !strings.Contains(err.Error(), "No standing decision") {
		t.Errorf("want plain 422 passthrough, got %v", err)
	}
}
