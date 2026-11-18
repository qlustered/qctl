package rule_suggestions

import (
	"errors"
	"net/http"
	"testing"
)

// A number in detail.context is the integer the server sent.
func TestHandleErrorKeepsContextIntegersExact(t *testing.T) {
	body := []byte(`{"detail":{"msg":"too many","context":{"rows":1234567,"big":9007199254740993}}}`)

	var apiErr *APIError
	if !errors.As(handleError(http.StatusConflict, body, "accept failed"), &apiErr) {
		t.Fatal("expected *APIError in chain")
	}
	for key, want := range map[string]int64{"rows": 1234567, "big": 9007199254740993} {
		if got, ok := apiErr.Context[key].(int64); !ok || got != want {
			t.Errorf("Context[%q] = %T(%v), want int64(%d)", key, apiErr.Context[key], apiErr.Context[key], want)
		}
	}
}
