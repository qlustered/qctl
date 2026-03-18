package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
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
	Timeout     time.Duration
	Verbosity   int // 0=off, 8=curl with redacted tokens, 9=curl with full tokens
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
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}

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

	httpClient := &http.Client{
		Timeout:   cfg.Timeout,
		Transport: capture,
	}

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
}

func (t *errorBodyCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clear previous capture
	t.lastCode = 0
	t.lastBody = nil

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	if resp.StatusCode >= 400 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			t.lastCode = resp.StatusCode
			t.lastBody = body
			// Replace body so downstream code (generated parser) can still read it
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}
	}

	return resp, nil
}

// HandleError processes an error from the generated client. If the transport
// captured an error response body (4xx/5xx), it delegates to apierror for
// proper parsing of all error formats. Otherwise it wraps the original error.
func (c *Client) HandleError(err error, contextMsg string) error {
	if err == nil {
		return nil
	}

	if c.errorCapture != nil && len(c.errorCapture.lastBody) > 0 {
		return apierror.HandleHTTPErrorFromBytes(c.errorCapture.lastCode, c.errorCapture.lastBody, contextMsg)
	}

	return fmt.Errorf("%s: %w", contextMsg, err)
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

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var bodyBytes []byte

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
		fmt.Fprintf(os.Stderr, "→ %s %s %s\n", funcName, req.Method, req.URL.RequestURI())
		if len(bodyBytes) > 0 {
			fmt.Fprintf(os.Stderr, "%s\n", string(bodyBytes))
		}
		fmt.Fprintln(os.Stderr)
	}

	// Log at verbosity >= 8: curl commands
	if t.verbosity >= VerbosityCurlRedacted {
		curlCmd := t.buildCurlCommand(req, bodyBytes)
		fmt.Fprintln(os.Stderr, curlCmd)
	}

	resp, err := t.base.RoundTrip(req)

	// Log response at verbosity 7
	if t.verbosity == VerbosityStructured && resp != nil {
		respBodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			// Replace the body with a new reader so callers can still read it
			resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
			fmt.Fprintf(os.Stderr, "← %d %s\n%s\n\n", resp.StatusCode, http.StatusText(resp.StatusCode), string(respBodyBytes))
		} else {
			fmt.Fprintf(os.Stderr, "← %d %s\n\n", resp.StatusCode, http.StatusText(resp.StatusCode))
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
				fmt.Fprintf(os.Stderr, "\n← %d %s\n%s\n", resp.StatusCode, http.StatusText(resp.StatusCode), string(respBodyBytes))
			} else {
				fmt.Fprintf(os.Stderr, "\n← %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))
			}
		} else {
			fmt.Fprintf(os.Stderr, "\n← %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
	}

	return resp, err
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
	parts = append(parts, fmt.Sprintf("'%s'", req.URL.String()))

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
		// Escape single quotes in the body for shell safety
		bodyStr := strings.ReplaceAll(string(body), "'", "'\"'\"'")
		parts = append(parts, "-d", fmt.Sprintf("'%s'", bodyStr))
	}

	return strings.Join(parts, " \\\n  ")
}

// redactAuthorizationHeader redacts the token value in an Authorization header
func redactAuthorizationHeader(value string) string {
	if strings.HasPrefix(value, "Bearer ") {
		return "Bearer <REDACTED>"
	}
	return value
}

// BaseURL returns the configured base URL
func (c *Client) BaseURL() string {
	return c.config.BaseURL
}

// Verbosity returns the verbosity level
func (c *Client) Verbosity() int {
	return c.verbosity
}
