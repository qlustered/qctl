package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestTransportDiagnosticNamesServerAndPreservesCause(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	SetServerSource(server.URL, `from context "staging"`)
	t.Cleanup(func() { SetServerSource("", "") })
	c, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, transportErr := c.API.GetOrganizationsListWithResponse(context.Background(), uuid.New(), nil)
	if transportErr == nil {
		t.Fatal("expected transport failure")
	}
	err = c.HandleError(transportErr, "request failed")
	for _, want := range []string{server.URL, `from context "staging"`, "not an authentication or organization problem", "--retries N"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
	var urlErr *url.Error
	if !errors.Is(err, transportErr) || !errors.As(err, &urlErr) || qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Errorf("transport cause or exit lost: %v", err)
	}
}

func TestParserFailureDoesNotClaimServerIsUnreachable(t *testing.T) {
	c := &Client{config: Config{BaseURL: "https://api.example.com"}}
	err := c.HandleError(errors.New("invalid successful response"), "request failed")
	if strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("parser failure = %v", err)
	}
}
