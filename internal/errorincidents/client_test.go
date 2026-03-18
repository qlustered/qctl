package errorincidents

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// mockTransport intercepts HTTP requests for testing
type mockTransport struct {
	handlers map[string]http.HandlerFunc
	mu       sync.RWMutex
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.Path
	m.mu.RLock()
	handler, ok := m.handlers[key]
	m.mu.RUnlock()

	if ok {
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Result(), nil
	}

	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusNotFound)
	return rec.Result(), nil
}

func setupMockTransport(handlers map[string]http.HandlerFunc) func() {
	transport := &mockTransport{handlers: handlers}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	prevInsecure := os.Getenv("QCTL_INSECURE_HTTP")
	os.Setenv("QCTL_INSECURE_HTTP", "1")

	return func() {
		http.DefaultTransport = originalTransport
		if prevInsecure == "" {
			os.Unsetenv("QCTL_INSECURE_HTTP")
		} else {
			os.Setenv("QCTL_INSECURE_HTTP", prevInsecure)
		}
	}
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Simple JSON encoding without importing encoding/json in test
	switch v := data.(type) {
	case string:
		w.Write([]byte(v))
	default:
		// For complex types, we'll use pre-formatted JSON strings
	}
}

func TestNewClient(t *testing.T) {
	client, err := NewClient("http://test.local", testOrgID, 0)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.baseURL != "http://test.local" {
		t.Errorf("expected baseURL 'http://test.local', got %q", client.baseURL)
	}
	if client.verbosity != 0 {
		t.Errorf("expected verbosity 0, got %d", client.verbosity)
	}
	if client.timeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", client.timeout)
	}
}

func TestGetErrorIncidentsParams(t *testing.T) {
	// Test that params can be created with various filters
	params := GetErrorIncidentsParams{
		OrderBy: "id",
		Reverse: true,
		Page:    1,
		Limit:   50,
	}

	if params.OrderBy != "id" {
		t.Errorf("expected OrderBy 'id', got %q", params.OrderBy)
	}
	if !params.Reverse {
		t.Error("expected Reverse to be true")
	}
	if params.Page != 1 {
		t.Errorf("expected Page 1, got %d", params.Page)
	}
	if params.Limit != 50 {
		t.Errorf("expected Limit 50, got %d", params.Limit)
	}
}

func TestErrorIncidentTinyTypeAlias(t *testing.T) {
	// Verify the type alias works correctly
	tiny := ErrorIncidentTiny{
		ID:          1,
		Module:      "sensor",
		Msg:         "test message",
		Count:       5,
		DatasetName: "test-dataset",
	}

	if tiny.ID != 1 {
		t.Errorf("expected ID 1, got %d", tiny.ID)
	}
	if tiny.Module != "sensor" {
		t.Errorf("expected Module 'sensor', got %q", tiny.Module)
	}
	if tiny.Msg != "test message" {
		t.Errorf("expected Msg 'test message', got %q", tiny.Msg)
	}
	if tiny.Count != 5 {
		t.Errorf("expected Count 5, got %d", tiny.Count)
	}
}

func TestErrorIncidentFullTypeAlias(t *testing.T) {
	// Verify the type alias works correctly
	jobName := "sensor-1"
	jobType := api.JobType("ingestion_job")
	stackTrace := "test stack trace"
	createdAt := time.Now()

	full := ErrorIncidentFull{
		ID:         123,
		Error:      "TestError",
		Msg:        "test message",
		Module:     "sensor",
		Count:      5,
		JobName:    &jobName,
		JobType:    &jobType,
		StackTrace: &stackTrace,
		CreatedAt:  &createdAt,
		Deleted:    false,
	}

	if full.ID != 123 {
		t.Errorf("expected ID 123, got %d", full.ID)
	}
	if full.Error != "TestError" {
		t.Errorf("expected Error 'TestError', got %q", full.Error)
	}
	if full.Module != "sensor" {
		t.Errorf("expected Module 'sensor', got %q", full.Module)
	}
	if full.JobName == nil || *full.JobName != "sensor-1" {
		t.Errorf("expected JobName 'sensor-1', got %v", full.JobName)
	}
}

func TestListPageTypeAlias(t *testing.T) {
	// Verify the type alias works correctly
	page := 1
	totalRows := 10
	listPage := ListPage{
		Results:   []api.ErrorIncidentTinySchema{},
		Page:      &page,
		TotalRows: &totalRows,
	}

	if listPage.Page == nil || *listPage.Page != 1 {
		t.Errorf("expected Page 1, got %v", listPage.Page)
	}
	if listPage.TotalRows == nil || *listPage.TotalRows != 10 {
		t.Errorf("expected TotalRows 10, got %v", listPage.TotalRows)
	}
}
