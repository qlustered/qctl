package alerts

import (
	"errors"
	"net/http"
	"testing"
)

func TestHandleErrorExtractsCodeAndContext(t *testing.T) {
	body := []byte(`{"detail":{"msg":"2 pending suggestions must be resolved","error_code":"PENDING_SUGGESTIONS_BLOCK_MIGRATION","context":{"table":"bookings"}}}`)

	err := handleError(http.StatusConflict, body, "resolve alert failed")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError in chain, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusConflict {
		t.Errorf("StatusCode = %d, want 409", apiErr.StatusCode)
	}
	if apiErr.Code != "PENDING_SUGGESTIONS_BLOCK_MIGRATION" {
		t.Errorf("Code = %q, want PENDING_SUGGESTIONS_BLOCK_MIGRATION", apiErr.Code)
	}
	if got := apiErr.ContextString("table"); got != "bookings" {
		t.Errorf("ContextString(table) = %q, want bookings", got)
	}
}

// FastAPI's validation 422 sends detail as a list, which carries no
// error_code. It must not panic and must leave Code empty.
func TestHandleErrorSurvivesListDetail(t *testing.T) {
	body := []byte(`{"detail":[{"loc":["body","action"],"msg":"field required"}]}`)

	err := handleError(http.StatusUnprocessableEntity, body, "resolve alert failed")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError in chain, got %T: %v", err, err)
	}
	if apiErr.Code != "" {
		t.Errorf("Code = %q, want empty for list-shaped detail", apiErr.Code)
	}
}

func TestHandleErrorFallsBackToContextCode(t *testing.T) {
	body := []byte(`{"detail":{"msg":"nope","context":{"code":"SOME_CODE"}}}`)

	err := handleError(http.StatusBadRequest, body, "resolve alert failed")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "SOME_CODE" {
		t.Errorf("Code = %q, want SOME_CODE", apiErr.Code)
	}
}

// A number in detail.context is the integer the server sent.
func TestHandleErrorKeepsContextIntegersExact(t *testing.T) {
	body := []byte(`{"detail":{"msg":"too many","context":{"rows":1234567,"big":9007199254740993}}}`)

	var apiErr *APIError
	if !errors.As(handleError(http.StatusConflict, body, "resolve alert failed"), &apiErr) {
		t.Fatal("expected *APIError in chain")
	}
	for key, want := range map[string]int64{"rows": 1234567, "big": 9007199254740993} {
		if got, ok := apiErr.Context[key].(int64); !ok || got != want {
			t.Errorf("Context[%q] = %T(%v), want int64(%d)", key, apiErr.Context[key], apiErr.Context[key], want)
		}
	}
}
