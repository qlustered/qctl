package client

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
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
			want:  "Basic <REDACTED>",
		},
		{
			name:  "empty",
			input: "",
			want:  "<REDACTED>",
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
	header     http.Header
}

func (t *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	header := t.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: t.statusCode,
		Body:       io.NopCloser(bytes.NewReader([]byte(t.body))),
		Header:     header,
	}, nil
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

// TestErrorBodyCapture_CapturesResponseHeader: the body alone is not enough to
// describe a 4xx. A 429 carries its backoff in Retry-After, and the generated
// parser can hand HandleError an error with no *http.Response attached at all
// (it returns nil, err whenever a typed error branch fails to unmarshal), so
// the header has to survive here or it is gone for good.
func TestErrorBodyCapture_CapturesResponseHeader(t *testing.T) {
	capture := &errorBodyCapture{
		base: &stubTransport{
			statusCode: 429,
			body:       `{"detail":"You have 3 dry run(s) in progress (limit: 3)."}`,
			header:     http.Header{"Retry-After": []string{"7"}},
		},
	}

	req, _ := http.NewRequest("PUT", "http://example.com/api/test", nil)
	if _, err := capture.RoundTrip(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := capture.lastHeader.Get("Retry-After"); got != "7" {
		t.Errorf("captured Retry-After = %q, want \"7\"", got)
	}
}

func TestErrorBodyCapture_ClearsHeaderOnNewRequest(t *testing.T) {
	capture := &errorBodyCapture{
		base: &stubTransport{statusCode: 429, body: `{"detail":"slow down"}`,
			header: http.Header{"Retry-After": []string{"7"}}},
	}
	req, _ := http.NewRequest("PUT", "http://example.com/api/test", nil)
	if _, err := capture.RoundTrip(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	capture.base = &stubTransport{statusCode: 200, body: `{"result":"ok"}`}
	if _, err := capture.RoundTrip(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.lastHeader != nil {
		t.Errorf("lastHeader = %v after a 2xx, want nil", capture.lastHeader)
	}
}

// TestHandleError_PreservesRetryAfter is the regression this pair of tests
// exists for. Whenever a 429's detail does not match the APIError object the
// spec declares -- an older server's bare string, a gateway's own page --
// ParseLaunchDryRunJobResponse returns (nil, unmarshal error) and the 429
// reaches the caller through HandleError with no *http.Response left to read.
// If HandleError drops the captured header, Retry-After silently reads 0 and
// the retry schedule falls back to its own backoff with no way to tell.
// (atlas now sends an object-shaped detail, which the generated parser reads
// fine; this path is what keeps the non-conforming 429s working.)
func TestHandleError_PreservesRetryAfter(t *testing.T) {
	capture := &errorBodyCapture{
		lastCode:   429,
		lastBody:   []byte(`{"detail":"You have 3 dry run(s) in progress (limit: 3)."}`),
		lastHeader: http.Header{"Retry-After": []string{"7"}},
	}
	c := &Client{errorCapture: capture}

	err := c.HandleError(errors.New("json: cannot unmarshal string into Go struct field"), "failed to launch dry-run job")
	var throttled *apierror.ThrottleError
	if !errors.As(err, &throttled) {
		t.Fatalf("expected a *apierror.ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %s, want 7s -- the response header was dropped", throttled.RetryAfter)
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

func TestHandleError_BodilessAccessFailure(t *testing.T) {
	apierror.SetOrgScope("Qluster-dev", "from the current context")
	t.Cleanup(func() { apierror.SetOrgScope("", "") })

	for _, tc := range []struct {
		status   int
		wantCode int
	}{
		{status: http.StatusForbidden, wantCode: qerrors.ExitUnauthorized},
		{status: http.StatusNotFound, wantCode: qerrors.ExitNotFound},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			c := &Client{errorCapture: &errorBodyCapture{lastCode: tc.status}}
			err := c.HandleError(errors.New("generated response parser failed"), "request failed")

			if got := qerrors.GetExitCode(err); got != tc.wantCode {
				t.Errorf("exit code = %d, want %d", got, tc.wantCode)
			}
			if !strings.Contains(err.Error(), "Scoped to organization Qluster-dev") {
				t.Errorf("error = %q, want the organization-scope note", err)
			}
		})
	}
}

func TestLastCapturedError_WithBody(t *testing.T) {
	body := []byte(`{"detail":{"msg":"nope","error_code":"REVIEW_NOT_OPEN"}}`)
	c := &Client{errorCapture: &errorBodyCapture{lastCode: 422, lastBody: body}}

	code, got, ok := c.LastCapturedError()
	if !ok {
		t.Fatal("expected ok=true when a body was captured")
	}
	if code != 422 {
		t.Errorf("code = %d, want 422", code)
	}
	if string(got) != string(body) {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestLastCapturedError_NoBody(t *testing.T) {
	c := &Client{errorCapture: &errorBodyCapture{}} // nothing captured
	if code, body, ok := c.LastCapturedError(); ok || code != 0 || body != nil {
		t.Errorf("expected (0, nil, false), got (%d, %v, %t)", code, body, ok)
	}
}

func TestLastCapturedError_EmptyBodyStillHasAResponse(t *testing.T) {
	c := &Client{errorCapture: &errorBodyCapture{lastCode: http.StatusForbidden}}
	code, body, ok := c.LastCapturedError()
	if !ok || code != http.StatusForbidden || len(body) != 0 {
		t.Errorf("expected (%d, empty, true), got (%d, %v, %t)",
			http.StatusForbidden, code, body, ok)
	}
}

func TestLastCapturedError_NilCapture(t *testing.T) {
	c := &Client{} // errorCapture nil
	if _, _, ok := c.LastCapturedError(); ok {
		t.Error("expected ok=false when errorCapture is nil")
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

// A 429 that never reached atlas -- a gateway or load balancer shedding load --
// carries a body conforming to nothing. It must still arrive as a throttle
// with the intermediary's own Retry-After, because that is the availability
// path: degrading it to an opaque transport error costs the user the retry.
func TestHandleError_GatewayThrottleIsStillAThrottle(t *testing.T) {
	capture := &errorBodyCapture{
		lastCode:   429,
		lastBody:   []byte(`<html><head><title>429 Too Many Requests</title></head></html>`),
		lastHeader: http.Header{"Retry-After": []string{"3"}},
	}
	c := &Client{errorCapture: capture}

	err := c.HandleError(errors.New("json: cannot unmarshal"), "failed to apply linked table")
	var throttled *apierror.ThrottleError
	if !errors.As(err, &throttled) {
		t.Fatalf("expected a *apierror.ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 3*time.Second {
		t.Errorf("RetryAfter = %s, want 3s from the gateway's header", throttled.RetryAfter)
	}
}

// The header can be stripped in transit while the body survives. HandleError
// hands both to apierror, so the body-side fallback has to work through this
// path too, not just when apierror is called directly.
func TestHandleError_UsesContextRetryAfterWhenTheHeaderWasStripped(t *testing.T) {
	capture := &errorBodyCapture{
		lastCode: 429,
		lastBody: []byte(`{"detail":{"msg":"You have 3 dry run(s) in progress (limit: 3).",` +
			`"error_code":"DRY_RUN_RATE_LIMIT","context":{"current_count":3,"max_allowed":3,` +
			`"limit_kind":"concurrent_execution","retry_after_seconds":15}}}`),
		lastHeader: http.Header{}, // proxy dropped Retry-After
	}
	c := &Client{errorCapture: capture}

	err := c.HandleError(errors.New("json: cannot unmarshal"), "failed to launch dry-run job")
	var throttled *apierror.ThrottleError
	if !errors.As(err, &throttled) {
		t.Fatalf("expected a *apierror.ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 15*time.Second {
		t.Errorf("RetryAfter = %s, want 15s from context.retry_after_seconds", throttled.RetryAfter)
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = saved }()

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()

	fn()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func TestLoggingTransportRedactsSecretFields(t *testing.T) {
	const requestBody = `{"id":42,"s3_secret_key":"request-secret","nested":[{"PASSWORD":"nested-secret"}],` +
		`"simple_url":"https://files.example.com/request-url-secret",` +
		`"data_sources":[{"source_id":42,"simple_url":"https://files.example.com/copy-url-secret"}],` +
		`"count":12345678901234567890}`
	const responseBody = `{"s3_access_key":"response-secret","results":[{"api_key":"nested-response-secret"}],` +
		`"SIMPLE_URL":"https://files.example.com/response-url-secret","name":"source"}`
	const queryURL = "https://files.example.com/query-url-secret"
	receivedBodies := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBodies <- string(body)
		if got := r.URL.Query().Get("simple_url"); got != queryURL {
			t.Errorf("request query simple_url = %q, want %q", got, queryURL)
		}
		if r.Header.Get("X-Test-Success") == "true" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
		_, _ = io.WriteString(w, responseBody)
	}))
	defer server.Close()

	for _, verbosity := range []int{VerbosityStructured, VerbosityCurlRedacted, VerbosityCurlFull} {
		req, err := http.NewRequest(http.MethodPatch,
			server.URL+"/api/data-sources/42?token=query-secret&simple_url="+url.QueryEscape(queryURL), strings.NewReader(requestBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Basic encoded-credential")
		if verbosity == VerbosityStructured {
			req.Header.Set("X-Test-Success", "true")
		}
		transport := &loggingTransport{base: http.DefaultTransport, verbosity: verbosity}
		var returnedBody string
		log := captureStderr(t, func() {
			resp, roundTripErr := transport.RoundTrip(req)
			if roundTripErr != nil {
				t.Errorf("RoundTrip: %v", roundTripErr)
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			returnedBody = string(body)
		})
		receivedBody := <-receivedBodies
		if receivedBody != requestBody || returnedBody != responseBody {
			t.Errorf("verbosity %d changed HTTP bodies: request %q, response %q", verbosity, receivedBody, returnedBody)
		}
		if verbosity == VerbosityCurlFull {
			for _, want := range []string{"request-secret", "encoded-credential", "query-secret", "request-url-secret", "copy-url-secret", "query-url-secret"} {
				if !strings.Contains(log, want) {
					t.Errorf("verbosity 9 full trace missing %q: %q", want, log)
				}
			}
			continue
		}
		for _, secret := range []string{
			"request-secret", "nested-secret", "response-secret", "nested-response-secret", "encoded-credential", "query-secret",
			"request-url-secret", "copy-url-secret", "response-url-secret", "query-url-secret",
		} {
			if strings.Contains(log, secret) {
				t.Errorf("verbosity %d leaked %q: %q", verbosity, secret, log)
			}
		}
		for _, want := range []string{
			`"s3_secret_key":"***"`, `"PASSWORD":"***"`, `"s3_access_key":"***"`, `"api_key":"***"`,
			`"simple_url":"***"`, `"SIMPLE_URL":"***"`, `simple_url=%2A%2A%2A`, `12345678901234567890`,
		} {
			if !strings.Contains(log, want) {
				t.Errorf("verbosity %d log missing %q: %q", verbosity, want, log)
			}
		}
	}
}

func TestRedactJSONBodyOmitsUnstructuredContent(t *testing.T) {
	for _, body := range []string{`secret=plaintext`, `"plaintext"`, `{"s3_secret_key":"plaintext"} trailing`} {
		if got := redactJSONBody([]byte(body)); strings.Contains(got, "plaintext") {
			t.Errorf("unstructured body leaked: %q", got)
		}
	}
}

// Invalid JSON may contain control characters and credentials, so its trace
// is omitted rather than printed.
func TestLoggingTransportEscapesControlCharactersInTheResponseBody(t *testing.T) {
	transport := &loggingTransport{
		verbosity: VerbosityStructured,
		base:      &stubTransport{statusCode: 200, body: "{\"msg\":\"\x1b[31mred\x1b]0;retitled\x07\"}"},
	}
	req, _ := http.NewRequest("GET", "http://example.com/api/test", nil)

	got := captureStderr(t, func() {
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}
		_, _ = io.ReadAll(resp.Body)
	})

	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("a raw control character reached the terminal: %q", got)
	}
	if !strings.Contains(got, "<body omitted: invalid JSON>") || strings.Contains(got, "red") {
		t.Errorf("invalid response body must be omitted, got: %q", got)
	}
}

// The body must still reach the caller byte for byte: the trace is sanitized,
// the payload the decoder sees is not.
func TestLoggingTransportLeavesTheResponseBodyItselfUntouched(t *testing.T) {
	const body = "{\"msg\":\"\x1b[31mred\"}"
	transport := &loggingTransport{
		verbosity: VerbosityStructured,
		base:      &stubTransport{statusCode: 200, body: body},
	}
	req, _ := http.NewRequest("GET", "http://example.com/api/test", nil)

	var got []byte
	_ = captureStderr(t, func() {
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}
		got, _ = io.ReadAll(resp.Body)
	})

	if string(got) != body {
		t.Errorf("response body was modified: got %q, want %q", got, body)
	}
}
