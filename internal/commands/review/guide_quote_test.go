package review

import (
	"errors"
	"net/http"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/testutil"
)

// Pins the approve output quoted under review-rules "## 5. Decide — humans
// only".
func TestGuideQuoteApproveCompletesTheReview(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	detail := sampleReviewDetail(api.ReviewRequestStatusApproved)
	detail.CurrentApprovals = 2
	registerDecisionEndpoints(mock, &gotBody,
		sampleDecisionResult(api.ReviewDecisionEnumApprove, true, api.ReviewRequestStatusApproved), detail)

	out, err := runReviewCommand(t, "", "review", "approve", testReviewUUID.String(), "--comment", "LGTM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	guidetest.AssertQuoted(t, "review-rules", out)
}

// Pins the request output quoted under request-review "## 4. Request the
// review".
func TestGuideQuoteRequestWithDryRunJob(t *testing.T) {
	mock := newReviewTestEnv(t)
	registerRuleResolution(mock)
	registerUsers(mock)
	registerDryRunJob(mock, api.IngestionJobStateFinished)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{
				Results: []api.DataSetTinySchema{{ID: 42, Name: "orders"}},
			})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusCreated, sampleReview(api.ReviewRequestStatusOpen))
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusOpen, DryRunJobID: testutil.IntPtr(512),
			})
		})

	out, err := runReviewCommand(t, "", "review", "request", "dedupe-emails", "--release", "2.1.0",
		"--table", "orders", "--reviewer", "alice@corp.com", "--dry-run-job", "512")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	guidetest.AssertQuoted(t, "request-review", out)
}

// Pins the already-open message quoted under request-review "## 4. Request
// the review".
func TestGuideQuoteRequestAlreadyOpen(t *testing.T) {
	err := mapRequestError(&rule_reviews.APIError{
		StatusCode: http.StatusConflict,
		Code:       "rule_review_already_open",
		Context: map[string]interface{}{
			"review_id": testReviewUUID.String(),
			"status":    "open",
			"title":     "Review dedupe-emails@2.1.0",
		},
		Err: errors.New("An active review already exists"),
	})
	guidetest.AssertQuoted(t, "request-review", err.Error())
}

// Pins the re-request output quoted under request-review "## 5. Monitor and
// respond".
func TestGuideQuoteRerequestNamesTheNudged(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users",
		func(w http.ResponseWriter, _ *http.Request) {
			users := sampleUsersList()
			users.Results[0].LastName = "Doe"
			testutil.RespondJSON(w, http.StatusOK, users)
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/re-request",
		func(w http.ResponseWriter, _ *http.Request) {
			ids := []openapi_types.UUID{testReviewerUUID}
			testutil.RespondJSON(w, http.StatusOK, api.RerequestReviewersResponseSchema{
				ReviewID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
				RerequestedReviewerIds: &ids,
			})
		})

	out, err := runReviewCommand(t, "", "review", "re-request", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	guidetest.AssertQuoted(t, "request-review", out)
}
