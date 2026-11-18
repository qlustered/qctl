package testutil

import (
	"net/http"
	"testing"
	"time"
)

// WaitForRequestCancellation waits for r's context to end, failing after 5s.
func WaitForRequestCancellation(t *testing.T, r *http.Request) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
	case <-timer.C:
		t.Error("HTTP request did not inherit the wait's cancellation")
	}
}
