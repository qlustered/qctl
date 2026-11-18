package review

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/testutil"
)

var testCommentUUID = uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")

func sampleComment() api.CommentResponseSchema {
	return api.CommentResponseSchema{
		ID:        testCommentUUID,
		ThreadID:  testReviewUUID,
		AuthorID:  testReviewerUUID,
		Body:      "Null emails slip through",
		CreatedAt: time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
	}
}

// registerAddComment mocks POST …/comments, capturing the wire body.
func registerAddComment(mock *testutil.MockAPIServer, gotBody *map[string]interface{}) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(gotBody)
			testutil.RespondJSON(w, http.StatusCreated, sampleComment())
		})
}

func runReviewCommand(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if stdin != "" {
		cmd.SetIn(strings.NewReader(stdin))
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestCommentCommand(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)

	out, err := runReviewCommand(t, "",
		"review", "comment", testReviewUUID.String(), "--body", "Null emails slip through")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["body"] != "Null emails slip through" {
		t.Errorf("wire body = %v", gotBody["body"])
	}
	if gotBody["parent_id"] != nil {
		t.Errorf("parent_id = %v, want null", gotBody["parent_id"])
	}
	want := "rule-review/" + testReviewUUID.String() + " comment added [c: " + testCommentUUID.String() + "]"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
}

func TestCommentReplyTo(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)
	parent := "0190aaaa-1111-2222-3333-444444444444"

	if _, err := runReviewCommand(t, "",
		"review", "comment", testReviewUUID.String(), "--body", "PTAL", "--reply-to", parent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["parent_id"] != parent {
		t.Errorf("parent_id = %v, want %s", gotBody["parent_id"], parent)
	}
}

func TestCommentMentionAppendsCcLine(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleUsersList())
		})

	if _, err := runReviewCommand(t, "",
		"review", "comment", testReviewUUID.String(), "--body", "please look", "--mention", "alice@corp.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantBody := "please look\n\ncc [@Alice Chen](mention:" + testReviewerUUID.String() + ")"
	if gotBody["body"] != wantBody {
		t.Errorf("wire body = %q, want %q", gotBody["body"], wantBody)
	}
}

func TestCommentBodyFileStdin(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)

	if _, err := runReviewCommand(t, "piped from a heredoc\n",
		"review", "comment", testReviewUUID.String(), "--body-file", "-"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["body"] != "piped from a heredoc\n" {
		t.Errorf("wire body = %q", gotBody["body"])
	}
}

func TestCommentBodyFlagsValidation(t *testing.T) {
	_ = newReviewTestEnv(t)

	if _, err := runReviewCommand(t, "",
		"review", "comment", testReviewUUID.String(), "--body", "x", "--body-file", "notes.md"); err == nil ||
		!strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("want mutual-exclusion error, got %v", err)
	}
	if _, err := runReviewCommand(t, "",
		"review", "comment", testReviewUUID.String()); err == nil ||
		!strings.Contains(err.Error(), "one of --body or --body-file is required") {
		t.Errorf("want missing-body error, got %v", err)
	}
}

func TestCommentDepthExceeded(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "Maximum comment depth exceeded",
					"error_code": "COMMENT_DEPTH_EXCEEDED",
					"context":    map[string]interface{}{"code": "comment_depth_exceeded"},
				},
			})
		})

	_, err := runReviewCommand(t, "",
		"review", "comment", testReviewUUID.String(), "--body", "too deep",
		"--reply-to", "0190dddd-1111-2222-3333-444444444444")
	if err == nil || !strings.Contains(err.Error(), "max thread depth is 3") {
		t.Errorf("want depth-exceeded mapping, got %v", err)
	}
}

func TestCommentDryRunJob(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)
	registerDryRunJob(mock, api.IngestionJobStateFinished)

	if _, err := runReviewCommand(t, "",
		"review", "comment", testReviewUUID.String(), "--body", "see run", "--dry-run-job", "512"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["linked_dry_run_job_id"] != float64(512) {
		t.Errorf("linked_dry_run_job_id = %v, want 512", gotBody["linked_dry_run_job_id"])
	}
}

func TestCommentDryRunJobMissing(t *testing.T) {
	mock := newReviewTestEnv(t)
	posted := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments",
		func(w http.ResponseWriter, r *http.Request) {
			posted = true
			testutil.RespondJSON(w, http.StatusCreated, sampleComment())
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/999",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Not found"},
			})
		})

	_, err := runReviewCommand(t, "",
		"review", "comment", testReviewUUID.String(), "--body", "x", "--dry-run-job", "999")
	if err == nil || !strings.Contains(err.Error(), "dry-run job 999 not found") {
		t.Errorf("want pre-validation not-found, got %v", err)
	}
	if posted {
		t.Error("comment must not be posted when the dry-run job is missing")
	}
}

func TestEditCommentCommand(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	mock.RegisterHandler("PATCH",
		"/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments/"+testCommentUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			c := sampleComment()
			c.Body, c.IsEdited = "fixed", true
			testutil.RespondJSON(w, http.StatusOK, c)
		})

	out, err := runReviewCommand(t, "",
		"review", "edit-comment", testReviewUUID.String(), testCommentUUID.String(), "--body", "fixed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["body"] != "fixed" {
		t.Errorf("wire body = %v", gotBody["body"])
	}
	want := "rule-review/" + testReviewUUID.String() + " comment " + testCommentUUID.String() + " edited"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
}

func TestEditCommentForbiddenPassesThrough(t *testing.T) {
	mock := newReviewTestEnv(t)
	mock.RegisterHandler("PATCH",
		"/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments/"+testCommentUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Only the author can edit a comment"},
			})
		})

	_, err := runReviewCommand(t, "",
		"review", "edit-comment", testReviewUUID.String(), testCommentUUID.String(), "--body", "x")
	if err == nil || !strings.Contains(err.Error(), "author") {
		t.Errorf("want author-only 403 passthrough, got %v", err)
	}
}

func TestDeleteCommentYes(t *testing.T) {
	mock := newReviewTestEnv(t)
	called := false
	mock.RegisterHandler("DELETE",
		"/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments/"+testCommentUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "ok"})
		})

	out, err := runReviewCommand(t, "",
		"review", "delete-comment", testReviewUUID.String(), testCommentUUID.String(), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("DELETE endpoint was never hit")
	}
	want := "rule-review/" + testReviewUUID.String() + " comment " + testCommentUUID.String() + " deleted"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q\n%s", want, out)
	}
}

func TestDeleteCommentDeclined(t *testing.T) {
	mock := newReviewTestEnv(t)
	called := false
	mock.RegisterHandler("DELETE",
		"/api/orgs/"+testOrgID+"/rule-reviews/"+testReviewUUID.String()+"/comments/"+testCommentUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "ok"})
		})
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	out, err := runReviewCommand(t, "",
		"review", "delete-comment", testReviewUUID.String(), testCommentUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("declined confirmation must not delete")
	}
	if !strings.Contains(out, "Aborted") {
		t.Errorf("output missing Aborted\n%s", out)
	}
}

// Commenting on a rule review needs REVIEW_LIST, which the editor role holds
// without org:users:list. A --mention given as a UUID must not be blocked by
// the directory it does not need.
func TestCommentUUIDMentionPostsWithoutTheDirectory(t *testing.T) {
	mock := newReviewTestEnv(t)
	var gotBody map[string]interface{}
	registerAddComment(mock, &gotBody)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden,
				map[string]any{"detail": map[string]any{"msg": "no access to the user list"}})
		})

	out, err := runReviewCommand(t, "", "review", "comment", testReviewUUID.String(),
		"--body", "please look", "--mention", testReviewerUUID.String())
	if err != nil {
		t.Fatalf("the caller may comment: %v", err)
	}
	wantBody := "please look\n\ncc [@" + testReviewerUUID.String() + "](mention:" + testReviewerUUID.String() + ")"
	if gotBody["body"] != wantBody {
		t.Errorf("wire body = %q, want %q", gotBody["body"], wantBody)
	}
	if !strings.Contains(out, "Warning: user directory unavailable") {
		t.Errorf("output = %q, want the directory warning", out)
	}
}
