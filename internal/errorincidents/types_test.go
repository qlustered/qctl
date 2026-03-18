package errorincidents

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestErrorIncidentSpec(t *testing.T) {
	spec := ErrorIncidentSpec{
		Error:    "TestError",
		Msg:      "Test message",
		Module:   "sensor",
		Count:    5,
		JobName:  stringPtr("sensor-1"),
		JobType:  stringPtr("ingestion_job"),
		MetaData: map[string]interface{}{"key": "value"},
	}

	if spec.Error != "TestError" {
		t.Errorf("expected Error 'TestError', got %q", spec.Error)
	}
	if spec.Msg != "Test message" {
		t.Errorf("expected Msg 'Test message', got %q", spec.Msg)
	}
	if spec.Module != "sensor" {
		t.Errorf("expected Module 'sensor', got %q", spec.Module)
	}
	if spec.Count != 5 {
		t.Errorf("expected Count 5, got %d", spec.Count)
	}
	if spec.JobName == nil || *spec.JobName != "sensor-1" {
		t.Errorf("expected JobName 'sensor-1', got %v", spec.JobName)
	}
}

func TestErrorIncidentStatus(t *testing.T) {
	createdAt := "2024-01-15T10:30:00Z"
	status := ErrorIncidentStatus{
		ID:        123,
		Deleted:   false,
		CreatedAt: &createdAt,
	}

	if status.ID != 123 {
		t.Errorf("expected ID 123, got %d", status.ID)
	}
	if status.Deleted != false {
		t.Errorf("expected Deleted false, got %v", status.Deleted)
	}
	if status.CreatedAt == nil || *status.CreatedAt != "2024-01-15T10:30:00Z" {
		t.Errorf("expected CreatedAt '2024-01-15T10:30:00Z', got %v", status.CreatedAt)
	}
}

func TestErrorIncidentManifest_YAMLMarshal(t *testing.T) {
	manifest := ErrorIncidentManifest{
		APIVersion: APIVersionV1,
		Kind:       "ErrorIncident",
		Metadata:   ErrorIncidentMetadata{},
		Spec: ErrorIncidentSpec{
			Error:  "AccessDeniedError",
			Msg:    "Access denied",
			Module: "sensor",
			Count:  5,
		},
		Status: &ErrorIncidentStatus{
			ID:      123,
			Deleted: false,
		},
	}

	data, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}

	yamlStr := string(data)

	// Check key fields are present
	if !contains(yamlStr, "apiVersion: qluster.ai/v1") {
		t.Error("expected apiVersion in YAML output")
	}
	if !contains(yamlStr, "kind: ErrorIncident") {
		t.Error("expected kind in YAML output")
	}
	if !contains(yamlStr, "error: AccessDeniedError") {
		t.Error("expected error in YAML output")
	}
	if !contains(yamlStr, "module: sensor") {
		t.Error("expected module in YAML output")
	}
}

func TestErrorIncidentManifest_YAMLUnmarshal(t *testing.T) {
	yamlData := `
apiVersion: qluster.ai/v1
kind: ErrorIncident
metadata: {}
spec:
  error: TestError
  msg: Test message
  module: sensor
  count: 10
status:
  id: 456
  deleted: true
`

	var manifest ErrorIncidentManifest
	err := yaml.Unmarshal([]byte(yamlData), &manifest)
	if err != nil {
		t.Fatalf("failed to unmarshal manifest: %v", err)
	}

	if manifest.APIVersion != "qluster.ai/v1" {
		t.Errorf("expected APIVersion 'qluster.ai/v1', got %q", manifest.APIVersion)
	}
	if manifest.Kind != "ErrorIncident" {
		t.Errorf("expected Kind 'ErrorIncident', got %q", manifest.Kind)
	}
	if manifest.Spec.Error != "TestError" {
		t.Errorf("expected Spec.Error 'TestError', got %q", manifest.Spec.Error)
	}
	if manifest.Spec.Module != "sensor" {
		t.Errorf("expected Spec.Module 'sensor', got %q", manifest.Spec.Module)
	}
	if manifest.Spec.Count != 10 {
		t.Errorf("expected Spec.Count 10, got %d", manifest.Spec.Count)
	}
	if manifest.Status == nil {
		t.Fatal("expected Status to be present")
	}
	if manifest.Status.ID != 456 {
		t.Errorf("expected Status.ID 456, got %d", manifest.Status.ID)
	}
	if !manifest.Status.Deleted {
		t.Error("expected Status.Deleted to be true")
	}
}

func TestErrorIncidentManifest_JSONMarshal(t *testing.T) {
	manifest := ErrorIncidentManifest{
		APIVersion: APIVersionV1,
		Kind:       "ErrorIncident",
		Metadata:   ErrorIncidentMetadata{},
		Spec: ErrorIncidentSpec{
			Error:  "AccessDeniedError",
			Msg:    "Access denied",
			Module: "sensor",
			Count:  5,
		},
		Status: &ErrorIncidentStatus{
			ID:      123,
			Deleted: false,
		},
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}

	jsonStr := string(data)

	// Check key fields are present
	if !contains(jsonStr, `"apiVersion":"qluster.ai/v1"`) {
		t.Error("expected apiVersion in JSON output")
	}
	if !contains(jsonStr, `"kind":"ErrorIncident"`) {
		t.Error("expected kind in JSON output")
	}
	if !contains(jsonStr, `"error":"AccessDeniedError"`) {
		t.Error("expected error in JSON output")
	}
}

func TestErrorIncidentManifest_JSONUnmarshal(t *testing.T) {
	jsonData := `{
		"apiVersion": "qluster.ai/v1",
		"kind": "ErrorIncident",
		"metadata": {},
		"spec": {
			"error": "TestError",
			"msg": "Test message",
			"module": "sensor",
			"count": 10
		},
		"status": {
			"id": 456,
			"deleted": true
		}
	}`

	var manifest ErrorIncidentManifest
	err := json.Unmarshal([]byte(jsonData), &manifest)
	if err != nil {
		t.Fatalf("failed to unmarshal manifest: %v", err)
	}

	if manifest.APIVersion != "qluster.ai/v1" {
		t.Errorf("expected APIVersion 'qluster.ai/v1', got %q", manifest.APIVersion)
	}
	if manifest.Kind != "ErrorIncident" {
		t.Errorf("expected Kind 'ErrorIncident', got %q", manifest.Kind)
	}
	if manifest.Spec.Error != "TestError" {
		t.Errorf("expected Spec.Error 'TestError', got %q", manifest.Spec.Error)
	}
	if manifest.Status.ID != 456 {
		t.Errorf("expected Status.ID 456, got %d", manifest.Status.ID)
	}
}

func TestAPIVersionV1Constant(t *testing.T) {
	if APIVersionV1 != "qluster.ai/v1" {
		t.Errorf("expected APIVersionV1 'qluster.ai/v1', got %q", APIVersionV1)
	}
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
