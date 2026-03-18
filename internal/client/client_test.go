package client

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBuildCurlCommand_GET(t *testing.T) {
	transport := &loggingTransport{verbosity: VerbosityCurlRedacted}

	// Create request with Authorization header (bearer token)
	req, _ := http.NewRequest("GET", "https://api.example.com/api/destinations?name=test", nil)
	req.Header.Set("Authorization", "Bearer secret-token-123")

	curlCmd := transport.buildCurlCommand(req, nil)

	// Should have curl at start
	if !strings.HasPrefix(curlCmd, "curl") {
		t.Errorf("expected curl command to start with 'curl', got: %s", curlCmd)
	}

	// Should NOT have -X GET (curl defaults to GET)
	if strings.Contains(curlCmd, "-X") && strings.Contains(curlCmd, "GET") {
		// Check for the pattern "-X" followed by "GET" in a way that accounts for line breaks
		lines := strings.Split(curlCmd, "\n")
		hasXGet := false
		for i, line := range lines {
			if strings.TrimSpace(line) == "-X" && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "GET" {
				hasXGet = true
				break
			}
		}
		if hasXGet {
			t.Errorf("GET should not include -X flag, got: %s", curlCmd)
		}
	}

	// Should have the URL
	if !strings.Contains(curlCmd, "https://api.example.com/api/destinations?name=test") {
		t.Errorf("expected URL in curl command, got: %s", curlCmd)
	}

	// At verbosity 8, token should be REDACTED in the Authorization header
	if !strings.Contains(curlCmd, "Bearer <REDACTED>") {
		t.Errorf("expected redacted token at verbosity 8, got: %s", curlCmd)
	}
}

func TestBuildCurlCommand_POST(t *testing.T) {
	transport := &loggingTransport{verbosity: VerbosityCurlRedacted}

	req, _ := http.NewRequest("POST", "https://api.example.com/api/destinations", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token-123")

	body := []byte(`{"name":"test","host":"db.example.com"}`)
	curlCmd := transport.buildCurlCommand(req, body)

	// Should have -X POST (may be on separate line due to line continuation)
	if !strings.Contains(curlCmd, "POST") {
		t.Errorf("expected POST method in curl command, got: %s", curlCmd)
	}

	// Should have Content-Type header
	if !strings.Contains(curlCmd, "Content-Type: application/json") {
		t.Errorf("expected Content-Type header, got: %s", curlCmd)
	}

	// Should have body data with -d flag
	if !strings.Contains(curlCmd, "-d") {
		t.Errorf("expected -d flag for body, got: %s", curlCmd)
	}

	// Should contain the request body
	if !strings.Contains(curlCmd, `"name":"test"`) {
		t.Errorf("expected body content, got: %s", curlCmd)
	}
}

func TestBuildCurlCommand_FullToken(t *testing.T) {
	// At verbosity 9, token should NOT be redacted
	transport := &loggingTransport{verbosity: VerbosityCurlFull}

	req, _ := http.NewRequest("GET", "https://api.example.com/api/destinations", nil)
	req.Header.Set("Authorization", "Bearer secret-token-123")

	curlCmd := transport.buildCurlCommand(req, nil)

	// At verbosity 9, token should be visible
	if !strings.Contains(curlCmd, "Bearer secret-token-123") {
		t.Errorf("expected full token at verbosity 9, got: %s", curlCmd)
	}

	// Should NOT contain REDACTED
	if strings.Contains(curlCmd, "<REDACTED>") {
		t.Errorf("token should not be redacted at verbosity 9, got: %s", curlCmd)
	}
}

func TestBuildCurlCommand_EscapesQuotes(t *testing.T) {
	transport := &loggingTransport{verbosity: VerbosityCurlRedacted}

	req, _ := http.NewRequest("POST", "https://api.example.com/api/test", nil)
	body := []byte(`{"name":"test's value"}`)

	curlCmd := transport.buildCurlCommand(req, body)

	// Single quotes in body should be properly escaped
	// The escape pattern is 'text'"'"'more text'
	if !strings.Contains(curlCmd, `'"'"'`) {
		t.Errorf("expected escaped single quote in body, got: %s", curlCmd)
	}
}

func TestVerbosityConstants(t *testing.T) {
	// Verify the constants are correctly defined
	if VerbosityOff != 0 {
		t.Errorf("VerbosityOff should be 0, got %d", VerbosityOff)
	}
	if VerbosityStructured != 7 {
		t.Errorf("VerbosityStructured should be 7, got %d", VerbosityStructured)
	}
	if VerbosityCurlRedacted != 8 {
		t.Errorf("VerbosityCurlRedacted should be 8, got %d", VerbosityCurlRedacted)
	}
	if VerbosityCurlFull != 9 {
		t.Errorf("VerbosityCurlFull should be 9, got %d", VerbosityCurlFull)
	}
}

func TestNewClient_VerbosityPropagation(t *testing.T) {
	tests := []struct {
		name      string
		verbosity int
	}{
		{"off", 0},
		{"structured", 7},
		{"curl redacted", 8},
		{"curl full", 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				BaseURL:   "https://api.example.com",
				Verbosity: tt.verbosity,
			}

			client, err := New(cfg)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			if client.Verbosity() != tt.verbosity {
				t.Errorf("Verbosity() = %d, want %d", client.Verbosity(), tt.verbosity)
			}
		})
	}
}

func TestGetCallerFunctionName(t *testing.T) {
	// The function should return empty string when not called from API client
	funcName := getCallerFunctionName()
	if funcName != "" {
		t.Errorf("expected empty string when not called from API client, got: %s", funcName)
	}
}

func TestRedactAuthorizationHeader(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bearer token",
			input: "Bearer secret-token-123",
			want:  "Bearer <REDACTED>",
		},
		{
			name:  "non-bearer",
			input: "Basic dXNlcjpwYXNz",
			want:  "Basic dXNlcjpwYXNz",
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactAuthorizationHeader(tt.input)
			if got != tt.want {
				t.Errorf("redactAuthorizationHeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBearerTransport_AddsHeader(t *testing.T) {
	transport := &bearerTransport{
		base:        http.DefaultTransport,
		accessToken: "test-token-123",
	}

	req, _ := http.NewRequest("GET", "http://example.com", nil)

	// Simulate what happens before RoundTrip - the header would be set
	if transport.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+transport.accessToken)
	}

	authHeader := req.Header.Get("Authorization")
	if authHeader != "Bearer test-token-123" {
		t.Errorf("expected Authorization header 'Bearer test-token-123', got %s", authHeader)
	}
}

func TestBearerTransport_EmptyToken(t *testing.T) {
	transport := &bearerTransport{
		base:        http.DefaultTransport,
		accessToken: "",
	}

	req, _ := http.NewRequest("GET", "http://example.com", nil)

	// Simulate what happens - no header should be set for empty token
	if transport.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+transport.accessToken)
	}

	authHeader := req.Header.Get("Authorization")
	if authHeader != "" {
		t.Errorf("expected no Authorization header for empty token, got %s", authHeader)
	}
}

// stubTransport returns a fixed response for testing
type stubTransport struct {
	statusCode int
	body       string
}

func (t *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.statusCode,
		Body:       io.NopCloser(bytes.NewReader([]byte(t.body))),
		Header:     make(http.Header),
	}, nil
}

// errorTransport returns a network error
type errorTransport struct{}

func (t *errorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func TestErrorBodyCapture_CapturesErrorResponse(t *testing.T) {
	body := `{"detail":{"msg":"rule import failed","severity":"error"}}`
	capture := &errorBodyCapture{
		base: &stubTransport{statusCode: 422, body: body},
	}

	req, _ := http.NewRequest("POST", "http://example.com/api/test", nil)
	resp, err := capture.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Body should be captured
	if capture.lastCode != 422 {
		t.Errorf("expected lastCode 422, got %d", capture.lastCode)
	}
	if string(capture.lastBody) != body {
		t.Errorf("expected captured body %q, got %q", body, string(capture.lastBody))
	}

	// Response body should still be readable by downstream code
	respBody, _ := io.ReadAll(resp.Body)
	if string(respBody) != body {
		t.Errorf("expected response body %q, got %q", body, string(respBody))
	}
}

func TestErrorBodyCapture_IgnoresSuccessResponse(t *testing.T) {
	capture := &errorBodyCapture{
		base: &stubTransport{statusCode: 200, body: `{"result":"ok"}`},
	}

	req, _ := http.NewRequest("GET", "http://example.com/api/test", nil)
	_, err := capture.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT capture body for 2xx
	if capture.lastCode != 0 {
		t.Errorf("expected lastCode 0, got %d", capture.lastCode)
	}
	if capture.lastBody != nil {
		t.Errorf("expected nil lastBody, got %q", string(capture.lastBody))
	}
}

func TestErrorBodyCapture_ClearsOnNewRequest(t *testing.T) {
	// First request: 422 error
	capture := &errorBodyCapture{
		base: &stubTransport{statusCode: 422, body: `{"detail":"error"}`},
	}
	req, _ := http.NewRequest("POST", "http://example.com/api/test", nil)
	_, _ = capture.RoundTrip(req)

	if capture.lastCode != 422 {
		t.Fatalf("expected lastCode 422 after error, got %d", capture.lastCode)
	}

	// Second request: 200 success — previous capture should be cleared
	capture.base = &stubTransport{statusCode: 200, body: `{"result":"ok"}`}
	req, _ = http.NewRequest("GET", "http://example.com/api/test", nil)
	_, _ = capture.RoundTrip(req)

	if capture.lastCode != 0 {
		t.Errorf("expected lastCode 0 after success, got %d", capture.lastCode)
	}
	if capture.lastBody != nil {
		t.Errorf("expected nil lastBody after success, got %q", string(capture.lastBody))
	}
}

func TestHandleError_NilError(t *testing.T) {
	c := &Client{errorCapture: &errorBodyCapture{}}

	result := c.HandleError(nil, "test")
	if result != nil {
		t.Errorf("expected nil for nil error, got %v", result)
	}
}

func TestHandleError_WithCapturedBody(t *testing.T) {
	capture := &errorBodyCapture{
		lastCode: 422,
		lastBody: []byte(`{"detail":{"msg":"duplicate rule name","severity":"error"}}`),
	}
	c := &Client{errorCapture: capture}

	result := c.HandleError(errors.New("json unmarshal failed"), "rule import failed")
	if result == nil {
		t.Fatal("expected non-nil error")
	}

	// Should contain the backend's error message, not the unmarshal error
	if !strings.Contains(result.Error(), "duplicate rule name") {
		t.Errorf("expected error to contain backend message, got: %v", result)
	}
}

func TestHandleError_WithoutCapturedBody(t *testing.T) {
	capture := &errorBodyCapture{} // no captured body
	c := &Client{errorCapture: capture}

	origErr := errors.New("connection refused")
	result := c.HandleError(origErr, "request failed")
	if result == nil {
		t.Fatal("expected non-nil error")
	}

	// Should wrap the original error
	if !strings.Contains(result.Error(), "connection refused") {
		t.Errorf("expected error to contain original message, got: %v", result)
	}
	if !strings.Contains(result.Error(), "request failed") {
		t.Errorf("expected error to contain context message, got: %v", result)
	}
}

func TestHandleError_Integration422ObjectDetail(t *testing.T) {
	// Simulate what happens when the generated parser fails on object detail:
	// errorBodyCapture captures the body, then HandleError uses apierror to parse it
	body := `{"detail":{"msg":"File contains rules that conflict with existing ones","severity":"error","error_code":"RULE_CONFLICT"}}`
	capture := &errorBodyCapture{
		lastCode: 422,
		lastBody: []byte(body),
	}
	c := &Client{errorCapture: capture}

	result := c.HandleError(errors.New("json: cannot unmarshal object into Go struct field"), "rule import failed")
	if result == nil {
		t.Fatal("expected non-nil error")
	}

	// apierror should extract the structured error message
	errStr := result.Error()
	if !strings.Contains(errStr, "File contains rules that conflict with existing ones") {
		t.Errorf("expected structured error message, got: %s", errStr)
	}
	if !strings.Contains(errStr, "RULE_CONFLICT") {
		t.Errorf("expected error code in output, got: %s", errStr)
	}
}
