package comment_threads //nolint:revive // Match the established package under test.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var testThreadUUID = uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")

// ByRows must chunk at the endpoint's 100-row cap and concatenate, rather than
// handing the backend a request it will reject.
func TestByRowsChunksAtOneHundred(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var batches [][]int
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads/by-rows",
		func(w http.ResponseWriter, r *http.Request) {
			var body api.ThreadsByRowsRequestSchema
			_ = json.NewDecoder(r.Body).Decode(&body)
			batches = append(batches, body.RowIds)
			testutil.RespondJSON(w, http.StatusOK, api.ThreadsByRowsResponseSchema{
				Threads: []api.RowThreadOverlaySchema{{ThreadID: testThreadUUID, RowID: body.RowIds[0]}},
			})
		})

	rowIDs := make([]int, 250)
	for i := range rowIDs {
		rowIDs[i] = i + 1
	}
	client := NewClient(mock.Server.URL, testOrgID, 0)
	got, err := client.ByRows("test-token", 42, rowIDs)
	if err != nil {
		t.Fatalf("ByRows: %v", err)
	}
	if len(batches) != 3 {
		t.Fatalf("sent %d requests, want 3 (100+100+50)", len(batches))
	}
	for i, want := range []int{100, 100, 50} {
		if len(batches[i]) != want {
			t.Errorf("batch %d had %d row ids, want %d", i, len(batches[i]), want)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d overlay entries, want 3 (one per batch)", len(got))
	}
}

// An empty row set must not reach the wire: the endpoint requires min_length 1.
func TestByRowsWithNoRowsMakesNoRequest(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	called := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads/by-rows",
		func(w http.ResponseWriter, r *http.Request) { called = true })

	got, err := NewClient(mock.Server.URL, testOrgID, 0).ByRows("test-token", 42, nil)
	if err != nil {
		t.Fatalf("ByRows: %v", err)
	}
	if called {
		t.Error("ByRows made a request for an empty row set")
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

// The all-targets-failed 422 is object-shaped, which the generated parser
// rejects before the body is inspectable. wrapAPIError must recover it.
func TestCreateSurfacesObjectShaped422(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "No valid targets: row 7 (row_not_found)"},
			})
		})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).Create("test-token", 42, CreateOptions{
		Targets: []Target{{RowID: 7}},
		Body:    "why?",
	})
	if err == nil {
		t.Fatal("expected an error for the all-targets-failed 422")
	}
	if !strings.Contains(err.Error(), "No valid targets") {
		t.Errorf("the backend's message was lost: %v", err)
	}
	// The message alone does not prove wrapAPIError ran: client.HandleError
	// also reads the captured body and would produce the same text. Only
	// comments.WrapAPIError yields an *APIError carrying the status code.
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError from wrapAPIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("StatusCode = %d, want 422", apiErr.StatusCode)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// --- Coverage for the methods the brief specified in prose -----------------

const testCommentIDStr = "0190cccc-5555-6666-7777-888888888888"

var testCommentUUID = uuid.MustParse(testCommentIDStr)

func threadPath() string {
	return "/api/orgs/" + testOrgID + "/datasets/42/comment-threads/" + testThreadUUID.String()
}

func newThreadClient(mock *testutil.MockAPIServer) *Client {
	return NewClient(mock.Server.URL, testOrgID, 0)
}

// Get returns the thread with its transcript.
func TestGetReturnsThreadWithTranscript(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	count := 2
	mock.RegisterHandler("GET", threadPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.CommentThreadResponseSchema{
			ID:           testThreadUUID,
			CommentCount: &count,
			Comments: &[]api.CommentResponseSchema{
				{ID: testCommentUUID, ThreadID: testThreadUUID, Body: "why is this null?"},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		})
	})

	got, err := newThreadClient(mock).Get("test-token", 42, testThreadUUID.String())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != testThreadUUID {
		t.Errorf("ID = %s, want %s", got.ID, testThreadUUID)
	}
	if got.Comments == nil || len(*got.Comments) != 1 {
		t.Fatalf("transcript not carried through: %+v", got.Comments)
	}
}

// A rule-revision-review thread answers 404 on these routes by design; that
// must survive as an *APIError with the not-found exit code.
func TestGetNotFoundIsAPIErrorWithNotFoundExitCode(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", threadPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
			"detail": map[string]interface{}{"msg": "Comment thread not found"},
		})
	})

	_, err := newThreadClient(mock).Get("test-token", 42, testThreadUUID.String())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
}

// Every id-taking method rejects a non-UUID before touching the wire.
//
// Registering handlers only on the valid-UUID path would not prove this: the
// mock 404s every unregistered route, so an implementation that swallowed the
// parse error and requested the zero UUID would still hand back "an error" and
// still never hit a registered handler. So the assertion is on the parse
// message specifically, and "before any request" is witnessed by a transport
// wrapper that sees EVERY outbound request, registered or not.
func TestIDParsingRejectsNonUUIDsBeforeAnyRequest(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	// Both restores are defers in this body, so they unwind LIFO: the wrapper
	// comes off first, then mock.Close() puts the real transport back. A
	// t.Cleanup here would be WRONG — a test body's defers run BEFORE its
	// cleanups, so the cleanup would re-install the closed mock transport over
	// the real one and leak it into the rest of the package's test binary.
	requested, restoreTransport := recordEveryRequest()
	defer restoreTransport()
	for _, m := range []string{"GET", "POST", "PATCH", "DELETE"} {
		mock.RegisterHandler(m, threadPath(), func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.CommentThreadResponseSchema{ID: testThreadUUID})
		})
	}
	c := newThreadClient(mock)
	good := testThreadUUID.String()

	cases := map[string]error{
		"Get":                   secondErr(c.Get("test-token", 42, "nope")),
		"AddComment":            secondErr(c.AddComment("test-token", 42, "nope", "hi", "")),
		"AddComment/parent":     secondErr(c.AddComment("test-token", 42, good, "hi", "nope")),
		"EditComment/thread":    secondErr(c.EditComment("test-token", 42, "nope", testCommentIDStr, "hi")),
		"EditComment/comment":   secondErr(c.EditComment("test-token", 42, good, "nope", "hi")),
		"DeleteComment/thread":  c.DeleteComment("test-token", 42, "nope", testCommentIDStr),
		"DeleteComment/comment": c.DeleteComment("test-token", 42, good, "nope"),
		"Resolve":               secondErr(c.Resolve("test-token", 42, "nope")),
		"Unresolve":             secondErr(c.Unresolve("test-token", 42, "nope")),
	}
	for name, err := range cases {
		if err == nil {
			t.Errorf("%s: expected an error for a non-UUID id", name)
			continue
		}
		// A 404 from the wire also reads as "an error" — only the parse
		// message proves the id was rejected locally.
		if !strings.Contains(err.Error(), "expected a full UUID") {
			t.Errorf("%s: want an id-parse failure, got %v", name, err)
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%s: exit code = %d, want %d", name, code, qerrors.ExitBadUsage)
		}
	}
	if n := *requested; n != 0 {
		t.Errorf("a malformed id reached the wire: %d request(s) made", n)
	}
}

// recordEveryRequest counts EVERY request leaving through the default
// transport, including ones the mock has no handler for. The mock installs
// itself as http.DefaultTransport, so this wraps whatever it installed. It
// returns the restore func rather than registering a t.Cleanup: the caller
// must unwind it as a defer, ahead of mock.Close() in LIFO order, so the
// closed mock transport is never left installed for later tests.
func recordEveryRequest() (*int, func()) {
	count := 0
	inner := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count++
		return inner.RoundTrip(r)
	})
	return &count, func() { http.DefaultTransport = inner }
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// AddComment must send parent_id when threading a reply and must never put a
// VALUE in linked_dry_run_job_id — the key itself is always on the wire (the
// generated field has no omitempty), so null is the contract here.
func TestAddCommentSendsParentAndNeverLinksADryRunJob(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var sent map[string]interface{}
	mock.RegisterHandler("POST", threadPath()+"/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		testutil.RespondJSON(w, http.StatusCreated, api.CommentResponseSchema{
			ID: testCommentUUID, ThreadID: testThreadUUID, Body: "agreed", CreatedAt: time.Now(),
		})
	})

	got, err := newThreadClient(mock).AddComment(
		"test-token", 42, testThreadUUID.String(), "agreed", testCommentIDStr)
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if got.ID != testCommentUUID {
		t.Errorf("ID = %s, want %s", got.ID, testCommentUUID)
	}
	if sent["parent_id"] != testCommentIDStr {
		t.Errorf("parent_id = %v, want %s", sent["parent_id"], testCommentIDStr)
	}
	if job, ok := sent["linked_dry_run_job_id"]; ok && job != nil {
		t.Errorf("linked_dry_run_job_id was sent (%v); the backend discards it on this arc", job)
	}
}

// A top-level comment sends a null parent_id, not a zero UUID.
func TestAddCommentTopLevelSendsNoParent(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var sent map[string]interface{}
	mock.RegisterHandler("POST", threadPath()+"/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		testutil.RespondJSON(w, http.StatusCreated, api.CommentResponseSchema{
			ID: testCommentUUID, ThreadID: testThreadUUID, Body: "first", CreatedAt: time.Now(),
		})
	})

	if _, err := newThreadClient(mock).AddComment(
		"test-token", 42, testThreadUUID.String(), "first", ""); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if parent, ok := sent["parent_id"]; ok && parent != nil {
		t.Errorf("parent_id = %v, want null for a top-level comment", parent)
	}
}

// The depth cap is a 422 with an object-shaped detail: error_code and
// context.code must both survive onto the *APIError.
func TestAddCommentDepthExceededKeepsErrorCode(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", threadPath()+"/comments", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"detail": map[string]interface{}{
				"msg":        "Maximum comment depth of 3 exceeded",
				"error_code": "COMMENT_DEPTH_EXCEEDED",
				"context":    map[string]interface{}{"code": "comment_depth_exceeded"},
			},
		})
	})

	_, err := newThreadClient(mock).AddComment(
		"test-token", 42, testThreadUUID.String(), "too deep", testCommentIDStr)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "COMMENT_DEPTH_EXCEEDED" {
		t.Errorf("Code = %q, want COMMENT_DEPTH_EXCEEDED", apiErr.Code)
	}
	if got := apiErr.ContextString("code"); got != "comment_depth_exceeded" {
		t.Errorf("context.code = %q, want comment_depth_exceeded", got)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestEditCommentSendsTheNewBody(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var sent api.EditCommentSchema
	mock.RegisterHandler("PATCH", threadPath()+"/comments/"+testCommentIDStr,
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			testutil.RespondJSON(w, http.StatusOK, api.CommentResponseSchema{
				ID: testCommentUUID, ThreadID: testThreadUUID, Body: sent.Body,
				IsEdited: true, CreatedAt: time.Now(),
			})
		})

	got, err := newThreadClient(mock).EditComment(
		"test-token", 42, testThreadUUID.String(), testCommentIDStr, "corrected")
	if err != nil {
		t.Fatalf("EditComment: %v", err)
	}
	if sent.Body != "corrected" {
		t.Errorf("sent body = %q, want %q", sent.Body, "corrected")
	}
	if !got.IsEdited {
		t.Error("IsEdited = false, want true")
	}
}

// Only the author may edit; a 403 must keep the unauthorized exit code.
func TestEditCommentForbiddenKeepsUnauthorizedExitCode(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("PATCH", threadPath()+"/comments/"+testCommentIDStr,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Only the author can edit this comment"},
			})
		})

	_, err := newThreadClient(mock).EditComment(
		"test-token", 42, testThreadUUID.String(), testCommentIDStr, "corrected")
	if err == nil {
		t.Fatal("expected an error for a 403")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
}

func TestDeleteCommentReturnsNilOn200(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	hit := false
	mock.RegisterHandler("DELETE", threadPath()+"/comments/"+testCommentIDStr,
		func(w http.ResponseWriter, _ *http.Request) {
			hit = true
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
		})

	if err := newThreadClient(mock).DeleteComment(
		"test-token", 42, testThreadUUID.String(), testCommentIDStr); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if !hit {
		t.Error("DeleteComment made no request")
	}
}

func TestDeleteCommentSurfacesNotFound(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("DELETE", threadPath()+"/comments/"+testCommentIDStr,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Comment not found"},
			})
		})

	err := newThreadClient(mock).DeleteComment(
		"test-token", 42, testThreadUUID.String(), testCommentIDStr)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitNotFound, err)
	}
}

// resolve/unresolve answer with STATE, not the transcript: comments is [] with
// a real comment_count. The client must pass that through untouched.
func TestResolveReturnsStateNotTranscript(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	count := 4
	resolvedAt := time.Now()
	reason := "manual"
	mock.RegisterHandler("POST", threadPath()+"/resolve", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.CommentThreadResponseSchema{
			ID:             testThreadUUID,
			CommentCount:   &count,
			Comments:       &[]api.CommentResponseSchema{},
			ResolvedAt:     &resolvedAt,
			ResolvedReason: &reason,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		})
	})

	got, err := newThreadClient(mock).Resolve("test-token", 42, testThreadUUID.String())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ResolvedAt == nil || got.ResolvedReason == nil || *got.ResolvedReason != "manual" {
		t.Errorf("resolution state lost: %+v", got)
	}
	if got.CommentCount == nil || *got.CommentCount != 4 {
		t.Errorf("comment_count lost: %+v", got.CommentCount)
	}
	if got.Comments == nil || len(*got.Comments) != 0 {
		t.Errorf("comments = %+v, want the empty transcript the backend sends", got.Comments)
	}
}

func TestUnresolveClearsResolution(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", threadPath()+"/unresolve", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.CommentThreadResponseSchema{
			ID: testThreadUUID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
	})

	got, err := newThreadClient(mock).Unresolve("test-token", 42, testThreadUUID.String())
	if err != nil {
		t.Fatalf("Unresolve: %v", err)
	}
	if got.ResolvedAt != nil || got.ResolvedReason != nil {
		t.Errorf("thread still reads as resolved: %+v", got)
	}
}

// Create sends the title only when set, and hands back the partial-success
// dropped_targets from the 201 body.
func TestCreateSendsTargetsAndReportsDroppedTargets(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var sent map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			testutil.RespondJSON(w, http.StatusCreated, api.CommentThreadCreateResponseSchema{
				Thread: api.CommentThreadResponseSchema{
					ID: testThreadUUID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
				},
				DroppedTargets: &[]api.DroppedTargetSchema{{RowID: 9, Reason: "row_not_found"}},
			})
		})

	fields := []string{"amount", "currency"}
	got, err := newThreadClient(mock).Create("test-token", 42, CreateOptions{
		Targets: []Target{{RowID: 7, FieldNames: &fields}, {RowID: 9}},
		Body:    "these two look wrong",
		Title:   "FX mismatch",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sent["title"] != "FX mismatch" {
		t.Errorf("title = %v, want %q", sent["title"], "FX mismatch")
	}
	targets, _ := sent["targets"].([]interface{})
	if len(targets) != 2 {
		t.Fatalf("sent %d targets, want 2", len(targets))
	}
	if got.DroppedTargets == nil || len(*got.DroppedTargets) != 1 {
		t.Fatalf("dropped_targets lost: %+v", got.DroppedTargets)
	}
	if (*got.DroppedTargets)[0].Reason != "row_not_found" {
		t.Errorf("reason = %q, want row_not_found", (*got.DroppedTargets)[0].Reason)
	}
}

// An absent title must not be sent as an empty string.
func TestCreateOmitsAnEmptyTitle(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var sent map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			testutil.RespondJSON(w, http.StatusCreated, api.CommentThreadCreateResponseSchema{
				Thread: api.CommentThreadResponseSchema{
					ID: testThreadUUID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
				},
			})
		})

	if _, err := newThreadClient(mock).Create("test-token", 42, CreateOptions{
		Targets: []Target{{RowID: 7}}, Body: "why?",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if title, ok := sent["title"]; ok && title != nil {
		t.Errorf("title = %v, want null when unset", title)
	}
}

// A non-2xx mid-chunk aborts ByRows rather than returning a partial overlay.
func TestByRowsStopsOnAnErrorMidChunk(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads/by-rows",
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				testutil.RespondJSON(w, http.StatusOK, api.ThreadsByRowsResponseSchema{
					Threads: []api.RowThreadOverlaySchema{{ThreadID: testThreadUUID, RowID: 1}},
				})
				return
			}
			testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "comment:read required"},
			})
		})

	rowIDs := make([]int, 150)
	for i := range rowIDs {
		rowIDs[i] = i + 1
	}
	got, err := newThreadClient(mock).ByRows("test-token", 42, rowIDs)
	if err == nil {
		t.Fatal("expected an error from the second chunk")
	}
	if got != nil {
		t.Errorf("got %d overlay entries, want none on failure", len(got))
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
}

// secondErr keeps the id-parsing table readable for methods returning (T, error).
func secondErr[T any](_ T, err error) error { return err }
