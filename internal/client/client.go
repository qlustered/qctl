package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/pkg/secretmask"
	"github.com/qlustered/qctl/internal/terminal"
)

// DefaultTimeout is the default HTTP client timeout
const DefaultTimeout = 30 * time.Second

// Verbosity levels for logging
const (
	VerbosityOff          = 0 // No debug output
	VerbosityStructured   = 7 // Structured HTTP request/response logging
	VerbosityCurlRedacted = 8 // Curl commands with redacted tokens
	VerbosityCurlFull     = 9 // Curl commands with full tokens
)

// Config holds client configuration
type Config struct {
	BaseURL     string
	AccessToken string
	// Timeout is this caller's own deliberate per-attempt deadline, and
	// almost every caller should leave it zero. Zero means "no opinion" and
	// resolves to --request-timeout, or to DefaultTimeout when the flag is
	// unset. Set it only where the work is genuinely not a 30-second request
	// -- stored_items asks for 300s because it moves files -- and note that
	// an explicit --request-timeout still wins over it. See ResolveTimeout.
	Timeout   time.Duration
	Verbosity int // 0=off, 7=structured with redacted bodies, 8=curl redacted, 9=curl full
}

// Client wraps the generated API client with qctl-specific behavior
type Client struct {
	API          *api.ClientWithResponses
	config       Config
	verbosity    int
	errorCapture *errorBodyCapture
}

// New creates a new Client instance
func New(cfg Config) (*Client, error) {
	// Resolved here as well as inside NewDoer -- ResolveTimeout is idempotent
	// -- so that c.config records the deadline this client actually ran with
	// rather than the "no opinion" zero almost every caller passes.
	cfg.Timeout = ResolveTimeout(cfg.Timeout)

	// Create error body capture transport to preserve error response bodies.
	// When the generated parser fails to unmarshal non-standard error formats
	// (e.g., 422 with object detail instead of array), HandleError can still
	// extract the error message from the captured body.
	capture := &errorBodyCapture{
		base: &bearerTransport{
			accessToken: cfg.AccessToken,
			base: &loggingTransport{
				base:      http.DefaultTransport,
				verbosity: cfg.Verbosity,
			},
		},
	}

	// NewDoer, not a bare *http.Client: it is what applies --retries, and
	// going through it is what keeps every API call on the same policy. The
	// retry sits OUTSIDE the capture, so errorCapture holds the last attempt's
	// body rather than the first one's -- which is the response the user is
	// shown.
	httpClient := NewDoer(cfg.Timeout, capture)

	apiClient, err := api.NewClientWithResponses(
		cfg.BaseURL,
		api.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, err
	}

	return &Client{
		API:          apiClient,
		config:       cfg,
		verbosity:    cfg.Verbosity,
		errorCapture: capture,
	}, nil
}

// errorBodyCapture wraps a transport to capture error response bodies.
// When the generated client's parser fails on non-standard error formats
// (e.g., 422 with object detail), the original body is lost. This transport
// preserves a copy so HandleError can delegate to apierror for proper parsing.
type errorBodyCapture struct {
	base     http.RoundTripper
	lastCode int
	lastBody []byte
	// lastHeader is the error response's headers. The body alone cannot
	// describe a 429: its backoff lives in Retry-After, and the generated
	// parser returns (nil, err) whenever a typed error branch fails to
	// unmarshal -- so by the time HandleError runs there is no *http.Response
	// left to read it from. Captured here, it survives that.
	lastHeader http.Header
}

func (t *errorBodyCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clear previous capture
	t.lastCode = 0
	t.lastBody = nil
	t.lastHeader = nil

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	if resp.StatusCode >= 400 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			t.lastCode = resp.StatusCode
			t.lastBody = body
			t.lastHeader = resp.Header
			// Replace body so downstream code (generated parser) can still read it
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}
	}

	return resp, nil
}

// HandleError processes an error from the generated client. If the transport
// captured an error response (4xx/5xx), it delegates to apierror for proper
// parsing of all error formats. A status with an empty body still counts: a
// gateway-generated 403 or 404 must keep its exit code and scope note.
// Otherwise it wraps the original error.
func (c *Client) HandleError(err error, contextMsg string) error {
	if err == nil {
		return nil
	}

	if c.errorCapture != nil && c.errorCapture.lastCode != 0 {
		return apierror.HandleHTTPErrorWithHeader(
			c.errorCapture.lastCode, c.errorCapture.lastBody, c.errorCapture.lastHeader, contextMsg,
		)
	}
	var transportErr *url.Error
	if errors.As(err, &transportErr) {
		advice := "This is a network failure, not an authentication or organization problem.\n" +
			"Read-only commands accept --retries N."
		return fmt.Errorf("%s: cannot reach %s%s: %w\n%s", contextMsg, c.config.BaseURL,
			sourceForServer(c.config.BaseURL), err, advice)
	}

	return fmt.Errorf("%s: %w", contextMsg, err)
}

// LastCapturedError returns the most recently captured 4xx/5xx response
// (status code + raw body). ok is false when nothing was captured — e.g. a
// transport-level failure with no HTTP response. Domain clients use this to
// reconstruct a machine-readable error when the generated parser rejects a
// non-standard error body (notably a 422 with object-shaped detail, which the
// generated HTTPValidationError parser cannot unmarshal), so the structured
// error_code/context survives instead of collapsing to a display string.
func (c *Client) LastCapturedError() (int, []byte, bool) {
	if c.errorCapture == nil || c.errorCapture.lastCode == 0 {
		return 0, nil, false
	}
	return c.errorCapture.lastCode, c.errorCapture.lastBody, true
}

// bearerTransport adds Authorization: Bearer header to requests
type bearerTransport struct {
	base        http.RoundTripper
	accessToken string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.accessToken)
	}
	return t.base.RoundTrip(req)
}

// loggingTransport wraps transport with verbose logging
type loggingTransport struct {
	base      http.RoundTripper
	verbosity int
}

// Logged bodies are redacted at verbosity 7 and 8. All logged server text
// passes through terminal.Sanitize before reaching stderr.
func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	out := DiagnosticWriter(req.Context(), os.Stderr)

	// Read and buffer the request body if present (needed for both verbosity 7 and 8+)
	if t.verbosity >= VerbosityStructured && req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		// Replace the body with a new reader so the request can proceed
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// Log at verbosity 7: structured HTTP logging
	if t.verbosity == VerbosityStructured {
		funcName := getCallerFunctionName()
		// Format: → FunctionName METHOD /path
		fmt.Fprintf(out, "→ %s %s %s\n", funcName, req.Method, terminal.Sanitize(redactURL(req.URL).RequestURI()))
		if len(bodyBytes) > 0 {
			fmt.Fprintf(out, "%s\n", terminal.Sanitize(redactJSONBody(bodyBytes)))
		}
		fmt.Fprintln(out)
	}

	// Log at verbosity >= 8: curl commands
	if t.verbosity >= VerbosityCurlRedacted {
		curlCmd := t.buildCurlCommand(req, bodyBytes)
		fmt.Fprintln(out, terminal.Sanitize(curlCmd))
	}

	resp, err := t.base.RoundTrip(req)

	// Log response at verbosity 7
	if t.verbosity == VerbosityStructured && resp != nil {
		respBodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			// Replace the body with a new reader so callers can still read it
			resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
			fmt.Fprintf(out, "← %d %s\n%s\n\n", resp.StatusCode, http.StatusText(resp.StatusCode), terminal.Sanitize(redactJSONBody(respBodyBytes)))
		} else {
			fmt.Fprintf(out, "← %d %s\n\n", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
	}

	// Log response at verbosity >= 8
	if t.verbosity >= VerbosityCurlRedacted && resp != nil {
		// Read response body for error responses
		if resp.StatusCode >= 400 {
			respBodyBytes, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				// Replace the body with a new reader so callers can still read it
				resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
				fmt.Fprintf(out, "\n← %d %s\n%s\n", resp.StatusCode, http.StatusText(resp.StatusCode), terminal.Sanitize(redactJSONBody(respBodyBytes)))
			} else {
				fmt.Fprintf(out, "\n← %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))
			}
		} else {
			fmt.Fprintf(out, "\n← %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
	}

	return resp, err
}

// redactJSONBody masks secret keys in a logged JSON object or array.
func redactJSONBody(body []byte) string {
	if !json.Valid(body) {
		return "<body omitted: invalid JSON>"
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "<body omitted: invalid JSON>"
	}
	switch value.(type) {
	case map[string]any, []any:
	default:
		return "<body omitted: scalar JSON>"
	}
	masked, err := json.Marshal(secretmask.New("simple_url").Mask(value))
	if err != nil {
		return "<body omitted: JSON encoding failed>"
	}
	return string(masked)
}

// redactURL masks query parameters whose names identify secrets.
func redactURL(original *url.URL) *url.URL {
	redacted := *original
	if original.RawQuery == "" {
		return &redacted
	}
	query, err := url.ParseQuery(original.RawQuery)
	if err != nil {
		redacted.RawQuery = ""
		return &redacted
	}
	mask := secretmask.New("simple_url")
	for key := range query {
		if mask.IsSecret(key) {
			query[key] = []string{"***"}
		}
	}
	redacted.RawQuery = query.Encode()
	return &redacted
}

// getCallerFunctionName walks the call stack to find the API method name
func getCallerFunctionName() string {
	// Walk up the stack to find the generated client method (e.g., GetDatasets)
	for i := 2; i < 15; i++ {
		pc, _, _, ok := runtime.Caller(i)
		if !ok {
			break
		}
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		name := fn.Name()
		// Look for api.(*ClientWithResponses).XxxWithResponse or api.(*Client).Xxx
		if strings.Contains(name, "internal/api.(*Client") {
			// Extract just the method name
			parts := strings.Split(name, ".")
			methodName := parts[len(parts)-1]
			// Remove "WithResponse" suffix if present for cleaner output
			methodName = strings.TrimSuffix(methodName, "WithResponse")
			// Remove "WithBody" suffix if present
			methodName = strings.TrimSuffix(methodName, "WithBody")
			return methodName
		}
	}
	return ""
}

// buildCurlCommand constructs a copy-pasteable curl command from the request
func (t *loggingTransport) buildCurlCommand(req *http.Request, body []byte) string {
	var parts []string
	parts = append(parts, "curl")

	// Add method
	if req.Method != http.MethodGet {
		parts = append(parts, "-X", req.Method)
	}

	// Add URL (with query params)
	requestURL := req.URL
	if t.verbosity < VerbosityCurlFull {
		requestURL = redactURL(req.URL)
	}
	parts = append(parts, fmt.Sprintf("'%s'", requestURL.String()))

	// Add headers (sorted for deterministic output)
	headerNames := make([]string, 0, len(req.Header))
	for name := range req.Header {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)

	for _, name := range headerNames {
		for _, value := range req.Header[name] {
			// Skip certain internal headers
			if strings.EqualFold(name, "Content-Length") {
				continue
			}
			// Handle Authorization header specially for token redaction
			if strings.EqualFold(name, "Authorization") && t.verbosity < VerbosityCurlFull {
				redactedValue := redactAuthorizationHeader(value)
				parts = append(parts, "-H", fmt.Sprintf("'%s: %s'", name, redactedValue))
			} else {
				parts = append(parts, "-H", fmt.Sprintf("'%s: %s'", name, value))
			}
		}
	}

	// Add body if present
	if len(body) > 0 {
		if t.verbosity < VerbosityCurlFull {
			body = []byte(redactJSONBody(body))
		}
		// Escape single quotes in the body for shell safety
		bodyStr := strings.ReplaceAll(string(body), "'", "'\"'\"'")
		parts = append(parts, "-d", fmt.Sprintf("'%s'", bodyStr))
	}

	return strings.Join(parts, " \\\n  ")
}

// redactAuthorizationHeader redacts the token value in an Authorization header
func redactAuthorizationHeader(value string) string {
	fields := strings.Fields(value)
	if len(fields) > 1 {
		return fields[0] + " <REDACTED>"
	}
	return "<REDACTED>"
}

// BaseURL returns the configured base URL
func (c *Client) BaseURL() string {
	return c.config.BaseURL
}

// Verbosity returns the verbosity level
func (c *Client) Verbosity() int {
	return c.verbosity
}
