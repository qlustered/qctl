package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPollForTokenRejectsMalformedErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":`))
	}))
	t.Cleanup(server.Close)

	client := NewKindeClient(server.URL, "test-client")
	_, err := client.PollForToken(context.Background(), "device-code", 5, 30, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to parse token error response") {
		t.Fatalf("PollForToken() error = %v, want malformed error-response failure", err)
	}
}
