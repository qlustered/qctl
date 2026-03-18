package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
)

// MockAPIServer creates a test server that mocks the Qluster API
type MockAPIServer struct {
	Server            *httptest.Server
	Handlers          map[string]http.HandlerFunc
	transport         *mockTransport
	originalTransport http.RoundTripper
	prevInsecureEnv   string
}

// NewMockAPIServer creates a new mock API server
func NewMockAPIServer() *MockAPIServer {
	mock := &MockAPIServer{
		Handlers: make(map[string]http.HandlerFunc),
	}

	mock.transport = &mockTransport{
		handlers: mock.Handlers,
		fallback: nil,
	}
	mock.originalTransport = http.DefaultTransport
	http.DefaultTransport = mock.transport
	mock.prevInsecureEnv = os.Getenv("QCTL_INSECURE_HTTP")
	os.Setenv("QCTL_INSECURE_HTTP", "1")

	// Provide a fake server URL without opening sockets
	mock.Server = &httptest.Server{
		URL: "http://mock.local",
	}
	return mock
}

// RegisterHandler registers a handler for a specific method and path
func (m *MockAPIServer) RegisterHandler(method, path string, handler http.HandlerFunc) {
	m.Handlers[method+" "+path] = handler
}

// Close closes the mock server
func (m *MockAPIServer) Close() {
	http.DefaultTransport = m.originalTransport
	if m.prevInsecureEnv == "" {
		os.Unsetenv("QCTL_INSECURE_HTTP")
	} else {
		os.Setenv("QCTL_INSECURE_HTTP", m.prevInsecureEnv)
	}
}

// URL returns the base URL of the mock server
func (m *MockAPIServer) URL() string {
	return m.Server.URL
}

type mockTransport struct {
	fallback http.RoundTripper
	handlers map[string]http.HandlerFunc
	mu       sync.RWMutex
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() != "mock.local" {
		return nil, fmt.Errorf("mock transport: refused connection to host %s", req.URL.Host)
	}

	key := req.Method + " " + req.URL.Path
	m.mu.RLock()
	handler, ok := m.handlers[key]
	m.mu.RUnlock()

	if ok {
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Result(), nil
	}

	if m.fallback != nil {
		return m.fallback.RoundTrip(req)
	}

	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusNotFound)
	return rec.Result(), nil
}

// Helper functions for common responses

// RespondJSON writes a JSON response with the given status code
func RespondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// RespondError writes an error response with the given status code
func RespondError(w http.ResponseWriter, status int, message string) {
	RespondJSON(w, status, map[string]string{"error": message})
}

// RespondText writes a plain text response with the given status code
func RespondText(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	w.Write([]byte(text))
}

// RespondWithCookie sets a cookie and writes a JSON response
func RespondWithCookie(w http.ResponseWriter, status int, data interface{}, cookieName, cookieValue string) {
	http.SetCookie(w, &http.Cookie{
		Name:  cookieName,
		Value: cookieValue,
		Path:  "/",
	})
	RespondJSON(w, status, data)
}

// Generic helper for creating paginated response handlers
// The callback receives the page number and should return (results, totalRows, hasNext)
func MockPaginatedHandler(callback func(page int) (interface{}, int, bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse page number from query params
		pageStr := r.URL.Query().Get("page")
		page := 1
		if pageStr != "" {
			if _, err := fmt.Sscanf(pageStr, "%d", &page); err != nil {
				page = 1
			}
		}

		results, totalRows, hasNext := callback(page)

		// Build pagination schema - use proper object format for generated client
		var next interface{}
		if hasNext {
			// PaginationSchema format: {"start": int, "end": int}
			next = map[string]interface{}{
				"start": page + 1,
			}
		}

		response := map[string]interface{}{
			"results":    results,
			"total_rows": totalRows,
			"page":       page,
			"next":       next,
			"previous":   nil,
		}
		RespondJSON(w, http.StatusOK, response)
	}
}

// Common error handlers

// MockUnauthorizedHandler creates a handler that returns a 401 error
func MockUnauthorizedHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		RespondError(w, http.StatusUnauthorized, "Unauthorized")
	}
}

// MockNotFoundHandler creates a handler that returns a 404 error
func MockNotFoundHandler(message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if message == "" {
			message = "Not found"
		}
		RespondError(w, http.StatusNotFound, message)
	}
}

// MockInternalServerErrorHandler creates a handler that returns a 500 error
func MockInternalServerErrorHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		RespondError(w, http.StatusInternalServerError, "Internal server error")
	}
}
