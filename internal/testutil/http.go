package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

// Requests returns every request the transport saw, in arrival order, as
// "METHOD /path". Query strings are excluded: a test asserting the request
// BOUNDARY cares which endpoints were reached, not how they were filtered.
func (m *MockAPIServer) Requests() []string {
	m.transport.mu.RLock()
	defer m.transport.mu.RUnlock()
	return append([]string(nil), m.transport.requests...)
}

type mockTransport struct {
	fallback http.RoundTripper
	handlers map[string]http.HandlerFunc
	requests []string
	mu       sync.RWMutex
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() != "mock.local" {
		return nil, fmt.Errorf("mock transport: refused connection to host %s", req.URL.Host)
	}

	// Recorded BEFORE dispatch, so a request no handler is registered for
	// still shows up: a test proving qctl made exactly one request needs to
	// see the call it did NOT expect, not have it silently 404'd away.
	//
	// Only requests to mock.local get this far -- the host guard above
	// returns first -- so a request to any OTHER host is not recorded. It is
	// not lost either: the connection error it returns fails whatever the
	// command was doing, which is how commands/root/org_scope_test.go still
	// catches a device-flow poll to the Kinde host.
	m.mu.Lock()
	m.requests = append(m.requests, req.Method+" "+req.URL.Path)
	m.mu.Unlock()

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

// MockPaginatedHandler serves paged results with cursor metadata only on
// page 1 in default ID order. callback returns results, totalRows and hasNext.
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
		orderBy := r.URL.Query().Get("order_by")
		if hasNext && page == 1 && (orderBy == "" || orderBy == "id") {
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

// MockOffsetPagedHandler serves rows 1..total the way atlas's OFFSET path
// does (get_paginated_results_order_by, pagination.py): it slices by the page
// and limit query parameters, reports total_rows, and never sends next. item
// builds the row for one id.
func MockOffsetPagedHandler(total int, item func(id int) interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, limit := 1, 20
		if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil {
			page = v
		}
		if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
			limit = v
		}
		results := []interface{}{}
		for id := (page-1)*limit + 1; id <= min(page*limit, total); id++ {
			results = append(results, item(id))
		}
		RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results":    results,
			"total_rows": total,
			"page":       page,
		})
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
