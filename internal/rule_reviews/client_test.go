package rule_reviews //nolint:revive // Match the established package under test.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	testReviewUUID   = uuid.MustParse("018f3c2e-4d5a-7b89-a1c2-3d4e5f607182")
	testRevisionUUID = uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
)

// newClientTestEnv wires the mock transport + config; returns the mock.
func newClientTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func TestGetReview(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewDetailResponseSchema{
				ID:                testReviewUUID,
				RuleRevisionID:    testRevisionUUID,
				Title:             "Review dedupe-emails@2.1.0",
				Status:            api.ReviewRequestStatusOpen,
				CurrentApprovals:  1,
				RequiredApprovals: 2,
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	detail, err := client.Get("test-token", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.ID != testReviewUUID {
		t.Errorf("ID = %s, want %s", detail.ID, testReviewUUID)
	}
	if detail.Status != api.ReviewRequestStatusOpen {
		t.Errorf("Status = %s, want open", detail.Status)
	}
}

func TestGetReviewInvalidUUID(t *testing.T) {
	_ = newClientTestEnv(t)
	client := NewClient("http://mock.local", testOrgID, 0)
	_, err := client.Get("test-token", "not-a-uuid")
	if err == nil {
		t.Fatal("expected error for invalid review id")
	}
}

func TestMalformedIDsExitBadUsage(t *testing.T) {
	client := NewClient("http://mock.local", testOrgID, 0)
	reviewID := testReviewUUID.String()
	cases := []struct {
		name string
		call func() error
	}{
		{"review", func() error { _, err := client.Get("test-token", "not-a-uuid"); return err }},
		{"list revision", func() error { _, err := client.ListForRevision("test-token", "not-a-uuid", nil, ""); return err }},
		{"create revision", func() error {
			_, err := client.CreateReview("test-token", "not-a-uuid", CreateReviewOptions{})
			return err
		}},
		{"link comment", func() error { return client.LinkDryRunToComment("test-token", reviewID, "not-a-uuid", 1) }},
		{"parent comment", func() error {
			_, err := client.AddComment("test-token", reviewID, CommentOptions{ParentID: "not-a-uuid"})
			return err
		}},
		{"edit comment", func() error { _, err := client.EditComment("test-token", reviewID, "not-a-uuid", "body"); return err }},
		{"delete comment", func() error { return client.DeleteComment("test-token", reviewID, "not-a-uuid") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d; error: %v", code, qerrors.ExitBadUsage, err)
			}
		})
	}
}

func TestGetReviewNotFoundIsAPIError(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Not found"},
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.Get("test-token", testReviewUUID.String())
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestInboxPassesQueryParams(t *testing.T) {
	mock := newClientTestEnv(t)
	var gotQuery map[string]string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			gotQuery = map[string]string{
				"page":            q.Get("page"),
				"limit":           q.Get("limit"),
				"status":          q.Get("status"),
				"my_reviews_only": q.Get("my_reviews_only"),
				"reviewer_type":   q.Get("reviewer_type"),
			}
			testutil.RespondJSON(w, http.StatusOK, api.ReviewsInboxResponseSchema{
				Reviews: []api.ReviewListItemSchema{}, Page: 2, Limit: 5, Total: 12,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	page, err := client.Inbox("test-token", InboxOptions{
		Page: 2, Limit: 5, Status: "open", All: true, ReviewerType: "author",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Total != 12 {
		t.Errorf("Total = %d, want 12", page.Total)
	}
	want := map[string]string{
		"page": "2", "limit": "5", "status": "open",
		"my_reviews_only": "false", "reviewer_type": "author",
	}
	for k, v := range want {
		if gotQuery[k] != v {
			t.Errorf("query %s = %q, want %q", k, gotQuery[k], v)
		}
	}
}

func TestInboxDefaultsOmitOptionalParams(t *testing.T) {
	mock := newClientTestEnv(t)
	var rawQuery string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			rawQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, api.ReviewsInboxResponseSchema{
				Reviews: []api.ReviewListItemSchema{}, Page: 1, Limit: 20, Total: 0,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.Inbox("test-token", InboxOptions{Page: 1, Limit: 20}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, forbidden := range []string{"status=", "my_reviews_only=", "reviewer_type="} {
		if strings.Contains(rawQuery, forbidden) {
			t.Errorf("query %q must not contain %q", rawQuery, forbidden)
		}
	}
}

func TestListForRevision(t *testing.T) {
	mock := newClientTestEnv(t)
	var gotQuery map[string]string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			gotQuery = map[string]string{
				"dataset_id":    q.Get("dataset_id"),
				"status_filter": q.Get("status_filter"),
			}
			testutil.RespondJSON(w, http.StatusOK, api.ReviewListResponseSchema{
				Reviews: []api.ReviewResponseSchema{{
					ID: testReviewUUID, RuleRevisionID: testRevisionUUID,
					Title: "Review dedupe-emails@2.1.0", Status: api.ReviewRequestStatusOpen,
				}},
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	datasetID := 42
	reviews, err := client.ListForRevision("test-token", testRevisionUUID.String(), &datasetID, "open")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviews) != 1 || reviews[0].ID != testReviewUUID {
		t.Fatalf("unexpected reviews: %+v", reviews)
	}
	if gotQuery["dataset_id"] != "42" || gotQuery["status_filter"] != "open" {
		t.Errorf("query = %v", gotQuery)
	}
}

func TestAuditLog(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/audit-log",
		func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Get("page") != "3" || q.Get("limit") != "10" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			testutil.RespondJSON(w, http.StatusOK, api.ReviewAuditListSchema{
				AuditEntries: []api.ReviewAuditEntrySchema{{Action: "decision_withdrawn", CreatedAt: time.Now()}},
				Page:         3, Limit: 10, Total: 31,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	auditPage, err := client.AuditLog("test-token", testReviewUUID.String(), 3, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auditPage.Total != 31 || len(auditPage.AuditEntries) != 1 {
		t.Fatalf("unexpected page: %+v", auditPage)
	}
	if auditPage.AuditEntries[0].Action != "decision_withdrawn" {
		t.Errorf("Action = %q", auditPage.AuditEntries[0].Action)
	}
}

func TestCreateReview(t *testing.T) {
	mock := newClientTestEnv(t)
	reviewerUUID := uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444")
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, api.ReviewResponseSchema{
				ID: testReviewUUID, RuleRevisionID: testRevisionUUID,
				Title: "Review dedupe-emails@2.1.0", Status: api.ReviewRequestStatusOpen,
				CurrentApprovals: 0, RequiredApprovals: 2,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	datasetID := 42
	review, err := client.CreateReview("test-token", testRevisionUUID.String(), CreateReviewOptions{
		Title:       "Review dedupe-emails@2.1.0",
		Description: "please look",
		DatasetID:   &datasetID,
		ReviewerIDs: []openapi_types.UUID{reviewerUUID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if review.ID != testReviewUUID || review.Status != api.ReviewRequestStatusOpen {
		t.Fatalf("unexpected review: %+v", review)
	}
	if gotBody["title"] != "Review dedupe-emails@2.1.0" {
		t.Errorf("body title = %v", gotBody["title"])
	}
	if gotBody["dataset_id"] != float64(42) {
		t.Errorf("body dataset_id = %v, want 42", gotBody["dataset_id"])
	}
	if gotBody["description"] != "please look" {
		t.Errorf("body description = %v", gotBody["description"])
	}
	ids, _ := gotBody["assigned_reviewer_ids"].([]interface{})
	if len(ids) != 1 || ids[0] != reviewerUUID.String() {
		t.Errorf("body assigned_reviewer_ids = %v", gotBody["assigned_reviewer_ids"])
	}
}

func TestCreateReviewOrgWideSendsNullDataset(t *testing.T) {
	mock := newClientTestEnv(t)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, api.ReviewResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.CreateReview("test-token", testRevisionUUID.String(), CreateReviewOptions{Title: "t"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, present := gotBody["dataset_id"]; !present || v != nil {
		t.Errorf("dataset_id = %v (present=%t), want explicit null", v, present)
	}
	if _, present := gotBody["description"]; present {
		t.Errorf("description must be omitted when empty, got %v", gotBody["description"])
	}
}

func TestCreateReviewConflictSurfacesContext(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionUUID.String()+"/rule-reviews",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg": "An active review already exists for this revision and dataset",
					"context": map[string]interface{}{
						"code":      "rule_review_already_open",
						"review_id": testReviewUUID.String(),
						"status":    "open",
						"title":     "Review dedupe-emails@2.1.0",
					},
				},
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.CreateReview("test-token", testRevisionUUID.String(), CreateReviewOptions{Title: "t"})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "rule_review_already_open" {
		t.Errorf("Code = %q", apiErr.Code)
	}
	if apiErr.ContextString("review_id") != testReviewUUID.String() {
		t.Errorf("review_id = %q", apiErr.ContextString("review_id"))
	}
}

func TestCloseReview(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/close",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusClosed,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	review, err := client.Close("test-token", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if review.Status != api.ReviewRequestStatusClosed {
		t.Errorf("Status = %s, want closed", review.Status)
	}
}

func TestReopenReturnsBackendStatusVerbatim(t *testing.T) {
	// Reopen into an occupied slot lands in closed ("superseded") — the client
	// must hand back whatever the backend said.
	mock := newClientTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/reopen",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewDetailResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusClosed,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	detail, err := client.Reopen("test-token", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.Status != api.ReviewRequestStatusClosed {
		t.Errorf("Status = %s, want closed (superseded)", detail.Status)
	}
}

func TestAssignAndUnassignSendReviewerIDs(t *testing.T) {
	mock := newClientTestEnv(t)
	reviewerUUID := uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444")
	for _, action := range []string{"assign", "unassign"} {
		action := action
		var gotBody map[string]interface{}
		mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/"+action,
			func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&gotBody)
				testutil.RespondJSON(w, http.StatusOK, api.ReviewDetailResponseSchema{
					ID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
				})
			})

		client := NewClient(mock.Server.URL, testOrgID, 0)
		var (
			detail *ReviewDetail
			err    error
		)
		if action == "assign" {
			detail, err = client.Assign("test-token", testReviewUUID.String(), []openapi_types.UUID{reviewerUUID})
		} else {
			detail, err = client.Unassign("test-token", testReviewUUID.String(), []openapi_types.UUID{reviewerUUID})
		}
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", action, err)
		}
		if detail.ID != testReviewUUID {
			t.Errorf("%s: ID = %s", action, detail.ID)
		}
		ids, _ := gotBody["reviewer_ids"].([]interface{})
		if len(ids) != 1 || ids[0] != reviewerUUID.String() {
			t.Errorf("%s: body reviewer_ids = %v", action, gotBody["reviewer_ids"])
		}
	}
}

func TestLinkDryRun(t *testing.T) {
	mock := newClientTestEnv(t)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusOK, api.ReviewResponseSchema{
				ID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
				DryRunJobID: testutil.IntPtr(512),
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	review, err := client.LinkDryRun("test-token", testReviewUUID.String(), 512)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if review.DryRunJobID == nil || *review.DryRunJobID != 512 {
		t.Errorf("DryRunJobID = %v, want 512", review.DryRunJobID)
	}
	if gotBody["dry_run_job_id"] != float64(512) {
		t.Errorf("body dry_run_job_id = %v, want 512", gotBody["dry_run_job_id"])
	}
}

func TestLinkDryRunInvalidReviewID(t *testing.T) {
	_ = newClientTestEnv(t)
	client := NewClient("http://mock.local", testOrgID, 0)
	if _, err := client.LinkDryRun("test-token", "not-a-uuid", 1); err == nil {
		t.Fatal("expected error for invalid review id")
	}
}

func TestLinkDryRunToComment(t *testing.T) {
	mock := newClientTestEnv(t)
	commentUUID := uuid.MustParse("cccccccc-1111-2222-3333-444444444444")
	var gotBody map[string]interface{}
	called := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments/"+commentUUID.String()+"/link-dry-run",
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusOK, api.ReviewMessageSchema{Message: "linked", Success: true})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if err := client.LinkDryRunToComment("test-token", testReviewUUID.String(), commentUUID.String(), 512); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("comment link endpoint was not called")
	}
	if gotBody["dry_run_job_id"] != float64(512) {
		t.Errorf("body dry_run_job_id = %v, want 512", gotBody["dry_run_job_id"])
	}
}

func TestLinkDryRunToCommentInvalidCommentID(t *testing.T) {
	_ = newClientTestEnv(t)
	client := NewClient("http://mock.local", testOrgID, 0)
	if err := client.LinkDryRunToComment("test-token", testReviewUUID.String(), "not-a-uuid", 1); err == nil {
		t.Fatal("expected error for invalid comment id")
	}
}

func TestRerequestOmitsEmptyReviewerIDs(t *testing.T) {
	mock := newClientTestEnv(t)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/re-request",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			ids := []openapi_types.UUID{uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444")}
			testutil.RespondJSON(w, http.StatusOK, api.RerequestReviewersResponseSchema{
				ReviewID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
				RerequestedReviewerIds: &ids,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	result, err := client.Rerequest("test-token", testReviewUUID.String(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != api.ReviewRequestStatusOpen || result.RerequestedReviewerIds == nil {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, present := gotBody["reviewer_ids"]; present {
		t.Errorf("reviewer_ids must be omitted for an empty nudge, got %v", gotBody["reviewer_ids"])
	}
}

func TestAddComment(t *testing.T) {
	mock := newClientTestEnv(t)
	commentUUID := uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")
	parentUUID := uuid.MustParse("0190aaaa-1111-2222-3333-444444444444")
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, api.CommentResponseSchema{
				ID:        commentUUID,
				ThreadID:  testReviewUUID,
				AuthorID:  uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444"),
				Body:      "hello",
				CreatedAt: time.Now(),
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	job := 512
	created, err := client.AddComment("test-token", testReviewUUID.String(), CommentOptions{
		Body: "hello", ParentID: parentUUID.String(), DryRunJobID: &job,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.ID != commentUUID {
		t.Errorf("ID = %s, want %s", created.ID, commentUUID)
	}
	if gotBody["body"] != "hello" {
		t.Errorf("wire body = %v, want hello", gotBody["body"])
	}
	if gotBody["parent_id"] != parentUUID.String() {
		t.Errorf("wire parent_id = %v, want %s", gotBody["parent_id"], parentUUID)
	}
	if gotBody["linked_dry_run_job_id"] != float64(512) {
		t.Errorf("wire linked_dry_run_job_id = %v, want 512", gotBody["linked_dry_run_job_id"])
	}
}

func TestAddCommentTopLevelSendsNullParent(t *testing.T) {
	mock := newClientTestEnv(t)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, api.CommentResponseSchema{
				ID: testReviewUUID, ThreadID: testReviewUUID, Body: "hi", CreatedAt: time.Now(),
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.AddComment("test-token", testReviewUUID.String(), CommentOptions{Body: "hi"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["parent_id"] != nil {
		t.Errorf("wire parent_id = %v, want null", gotBody["parent_id"])
	}
	if gotBody["linked_dry_run_job_id"] != nil {
		t.Errorf("wire linked_dry_run_job_id = %v, want null", gotBody["linked_dry_run_job_id"])
	}
}

func TestAddCommentInvalidParentUUID(t *testing.T) {
	_ = newClientTestEnv(t)
	client := NewClient("http://mock.local", testOrgID, 0)
	_, err := client.AddComment("test-token", testReviewUUID.String(), CommentOptions{Body: "x", ParentID: "not-a-uuid"})
	if err == nil || !strings.Contains(err.Error(), "invalid comment id") {
		t.Fatalf("want invalid-comment-id error, got %v", err)
	}
}

func TestEditComment(t *testing.T) {
	mock := newClientTestEnv(t)
	commentUUID := uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")
	var gotBody map[string]interface{}
	mock.RegisterHandler("PATCH",
		"/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments/"+commentUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusOK, api.CommentResponseSchema{
				ID: commentUUID, ThreadID: testReviewUUID, Body: "fixed", IsEdited: true, CreatedAt: time.Now(),
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	edited, err := client.EditComment("test-token", testReviewUUID.String(), commentUUID.String(), "fixed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !edited.IsEdited {
		t.Error("IsEdited = false, want true")
	}
	if gotBody["body"] != "fixed" {
		t.Errorf("wire body = %v, want fixed", gotBody["body"])
	}
}

func TestDeleteComment(t *testing.T) {
	mock := newClientTestEnv(t)
	commentUUID := uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")
	called := false
	mock.RegisterHandler("DELETE",
		"/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments/"+commentUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "ok"})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if err := client.DeleteComment("test-token", testReviewUUID.String(), commentUUID.String()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("DELETE endpoint was never hit")
	}
}

func TestSubmitDecision(t *testing.T) {
	mock := newClientTestEnv(t)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/decisions",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, api.SubmitDecisionResponseSchema{
				Decision: api.ReviewDecisionResponseSchema{
					ID:         uuid.MustParse("0190dddd-1111-2222-3333-444444444444"),
					ReviewID:   testReviewUUID,
					ReviewerID: uuid.MustParse("bbbbbbbb-1111-2222-3333-444444444444"),
					Decision:   api.ReviewDecisionEnumApprove,
					CreatedAt:  time.Now(),
				},
				ReviewCompleted: true,
				ReviewStatus:    api.ReviewRequestStatusApproved,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	result, err := client.SubmitDecision("test-token", testReviewUUID.String(), api.ReviewDecisionEnumApprove, "LGTM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.ReviewCompleted || result.ReviewStatus != api.ReviewRequestStatusApproved {
		t.Errorf("result = %+v, want completed+approved", result)
	}
	if gotBody["decision"] != "approve" {
		t.Errorf("wire decision = %v, want approve", gotBody["decision"])
	}
	if gotBody["comment"] != "LGTM" {
		t.Errorf("wire comment = %v, want LGTM", gotBody["comment"])
	}
}

func TestSubmitDecisionOmitsEmptyComment(t *testing.T) {
	mock := newClientTestEnv(t)
	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/decisions",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			testutil.RespondJSON(w, http.StatusCreated, api.SubmitDecisionResponseSchema{
				Decision:     api.ReviewDecisionResponseSchema{ReviewID: testReviewUUID, Decision: api.ReviewDecisionEnumReject, CreatedAt: time.Now()},
				ReviewStatus: api.ReviewRequestStatusRejected,
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.SubmitDecision("test-token", testReviewUUID.String(), api.ReviewDecisionEnumReject, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// null == absent for the backend's Optional[str]; must not send "".
	if gotBody["comment"] != nil {
		t.Errorf("wire comment = %v, want null", gotBody["comment"])
	}
}

func TestWithdrawDecision(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/withdraw-decision",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.ReviewDetailResponseSchema{
				ID: testReviewUUID, RuleRevisionID: testRevisionUUID, Title: "t",
				Status: api.ReviewRequestStatusOpen, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	detail, err := client.WithdrawDecision("test-token", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.Status != api.ReviewRequestStatusOpen {
		t.Errorf("Status = %s, want open", detail.Status)
	}
}
