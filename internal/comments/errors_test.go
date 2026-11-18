package comments

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qctlerrors "github.com/qlustered/qctl/internal/errors"
)

func TestHandleErrorParsesErrorCode(t *testing.T) {
	body := []byte(`{"detail": {"msg": "not designated", "error_code": "REVIEWER_NOT_DESIGNATED"}}`)
	err := HandleError(422, body, "assign failed")

	var apiErr *APIError
	if !stderrors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != 422 {
		t.Errorf("StatusCode = %d, want 422", apiErr.StatusCode)
	}
	if apiErr.Code != "REVIEWER_NOT_DESIGNATED" {
		t.Errorf("Code = %q, want REVIEWER_NOT_DESIGNATED", apiErr.Code)
	}
	if apiErr.Error() == "" {
		t.Error("Error() must be non-empty")
	}
}

// The unified Comment model's own 422: thread depth is capped at 3 for every
// arc that hangs off it, and the backend reports it with both an error_code
// and a context code.
func TestHandleErrorExtractsCodeAndContext(t *testing.T) {
	body := []byte(`{"detail":{"msg":"too deep","error_code":"COMMENT_DEPTH_EXCEEDED","context":{"code":"comment_depth_exceeded"}}}`)
	err := HandleError(http.StatusUnprocessableEntity, body, "add comment failed")

	var apiErr *APIError
	if !stderrors.As(err, &apiErr) {
		t.Fatalf("HandleError did not produce an *APIError: %T", err)
	}
	if apiErr.Code != "COMMENT_DEPTH_EXCEEDED" {
		t.Errorf("Code = %q, want COMMENT_DEPTH_EXCEEDED", apiErr.Code)
	}
	if got := apiErr.ContextString("code"); got != "comment_depth_exceeded" {
		t.Errorf("ContextString(code) = %q", got)
	}
}

// A number in detail.context is the integer the server sent.
func TestHandleErrorKeepsContextIntegersExact(t *testing.T) {
	body := []byte(`{"detail":{"msg":"too many","context":{"rows":1234567,"big":9007199254740993}}}`)

	var apiErr *APIError
	if !stderrors.As(HandleError(http.StatusConflict, body, "add comment failed"), &apiErr) {
		t.Fatal("HandleError did not produce an *APIError")
	}
	for key, want := range map[string]int64{"rows": 1234567, "big": 9007199254740993} {
		if got, ok := apiErr.Context[key].(int64); !ok || got != want {
			t.Errorf("Context[%q] = %T(%v), want int64(%d)", key, apiErr.Context[key], apiErr.Context[key], want)
		}
	}
}

func TestHandleErrorCodeFallsBackToContext(t *testing.T) {
	body := []byte(`{"detail": {"msg": "already open", "context": {"code": "rule_review_already_open", "review_id": "018f3c2e-4d5a-7b89-a1c2-3d4e5f607182", "status": "open", "title": "Review x"}}}`)
	err := HandleError(409, body, "request failed")

	var apiErr *APIError
	if !stderrors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "rule_review_already_open" {
		t.Errorf("Code = %q, want rule_review_already_open", apiErr.Code)
	}
	if got := apiErr.ContextString("review_id"); got != "018f3c2e-4d5a-7b89-a1c2-3d4e5f607182" {
		t.Errorf("ContextString(review_id) = %q", got)
	}
	if got := apiErr.ContextString("missing_key"); got != "" {
		t.Errorf("ContextString(missing_key) = %q, want empty", got)
	}
}

func TestHandleErrorNonJSONBody(t *testing.T) {
	err := HandleError(500, []byte("gateway exploded"), "request failed")

	var apiErr *APIError
	if !stderrors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "" {
		t.Errorf("Code = %q, want empty for non-JSON body", apiErr.Code)
	}
	if apiErr.Error() == "" {
		t.Error("Error() must be non-empty")
	}
}

func TestAPIErrorPreservesExitCode(t *testing.T) {
	// Unwrap must reach the internal/errors.Error so 404 still exits 3.
	err := HandleError(404, []byte(`{"detail": {"msg": "not found"}}`), "get failed")
	if got := qctlerrors.GetExitCode(err); got != qctlerrors.ExitNotFound {
		t.Errorf("GetExitCode = %d, want %d", got, qctlerrors.ExitNotFound)
	}
}

func TestFinalScopeNotePreservesAPIError(t *testing.T) {
	apierror.SetOrgScope("Qluster-dev", "from the current context")
	t.Cleanup(func() { apierror.SetOrgScope("", "") })

	apiErr := HandleError(http.StatusNotFound,
		[]byte(`{"detail":{"msg":"thread not found","error_code":"THREAD_NOT_FOUND"}}`),
		"get thread failed")
	withHint := fmt.Errorf("%w\n(look up the review thread instead)", apiErr)
	err := apierror.FinalizeScopeNote(withHint)

	var got *APIError
	if !stderrors.As(err, &got) {
		t.Fatalf("finalized error no longer unwraps to *APIError: %T", err)
	}
	if got.Code != "THREAD_NOT_FOUND" {
		t.Errorf("APIError.Code = %q, want THREAD_NOT_FOUND", got.Code)
	}
	if code := qctlerrors.GetExitCode(err); code != qctlerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qctlerrors.ExitNotFound)
	}
	if !strings.HasSuffix(err.Error(), "\n\n"+apierror.ScopeNote()) {
		t.Errorf("error = %q, want the scope note last", err)
	}
}
