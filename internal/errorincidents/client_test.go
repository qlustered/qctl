package errorincidents

import (
	"testing"
	"time"

	qclient "github.com/qlustered/qctl/internal/client"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

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
	// Asserted through ResolveTimeout rather than against the field, because
	// the field is this package's own OPINION and it deliberately has none.
	// The old assertion pinned a literal `30 * time.Second` here -- one of
	// twenty-six copies of DefaultTimeout, and the reason --request-timeout
	// could not move anything. What matters is the deadline the request
	// actually gets.
	if got := qclient.ResolveTimeout(client.timeout); got != qclient.DefaultTimeout {
		t.Errorf("resolved deadline = %v, want the %v default", got, qclient.DefaultTimeout)
	}
	qclient.SetRequestPolicy(qclient.RequestPolicy{Timeout: 90 * time.Second})
	defer qclient.ResetRequestPolicy()
	if got := qclient.ResolveTimeout(client.timeout); got != 90*time.Second {
		t.Errorf("resolved deadline = %v under --request-timeout 90s, want 1m30s", got)
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
		ID:     1,
		Module: "sensor",
		Msg:    "test message",
		Count:  5,
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
	full := ErrorIncidentFull{
		ID:      123,
		Error:   "TestError",
		Module:  "sensor",
		JobName: &jobName,
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
