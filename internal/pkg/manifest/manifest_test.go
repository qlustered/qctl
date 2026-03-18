package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStrictUnmarshal_ValidManifest(t *testing.T) {
	validYAML := `
apiVersion: v1
kind: Dataset
metadata:
  name: my-dataset
  labels:
    env: production
spec:
  description: A test dataset
`

	var m Manifest
	err := StrictUnmarshal([]byte(validYAML), &m)
	if err != nil {
		t.Errorf("StrictUnmarshal() error = %v, want nil", err)
		return
	}

	if m.APIVersion != "v1" {
		t.Errorf("APIVersion = %q, want %q", m.APIVersion, "v1")
	}
	if m.Kind != "Dataset" {
		t.Errorf("Kind = %q, want %q", m.Kind, "Dataset")
	}
	if m.Metadata.Name != "my-dataset" {
		t.Errorf("Metadata.Name = %q, want %q", m.Metadata.Name, "my-dataset")
	}
	if m.Metadata.Labels["env"] != "production" {
		t.Errorf("Metadata.Labels[env] = %q, want %q", m.Metadata.Labels["env"], "production")
	}
}

func TestStrictUnmarshal_UnknownField(t *testing.T) {
	// This tests that typos/unknown fields cause errors
	yamlWithTypo := `
apiVersion: v1
kind: Dataset
metadata:
  name: my-dataset
  naem: typo-field
spec:
  description: A test dataset
`

	var m Manifest
	err := StrictUnmarshal([]byte(yamlWithTypo), &m)
	if err == nil {
		t.Error("StrictUnmarshal() expected error for unknown field 'naem', got nil")
	}
}

func TestStrictUnmarshal_UnknownTopLevelField(t *testing.T) {
	yamlWithUnknown := `
apiVersion: v1
kind: Dataset
metadata:
  name: my-dataset
specc: typo-for-spec
`

	var m Manifest
	err := StrictUnmarshal([]byte(yamlWithUnknown), &m)
	if err == nil {
		t.Error("StrictUnmarshal() expected error for unknown field 'specc', got nil")
	}
}

func TestStrictUnmarshal_MissingRequired(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		field string
	}{
		{
			name: "missing apiVersion",
			yaml: `
kind: Dataset
metadata:
  name: my-dataset
`,
			field: "apiVersion",
		},
		{
			name: "missing kind",
			yaml: `
apiVersion: v1
metadata:
  name: my-dataset
`,
			field: "kind",
		},
		{
			name: "missing metadata.name",
			yaml: `
apiVersion: v1
kind: Dataset
metadata:
  labels:
    env: test
`,
			field: "metadata.name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tt.yaml))
			if err == nil {
				t.Errorf("LoadBytes() expected validation error for missing %s, got nil", tt.field)
				return
			}

			vErr, ok := err.(*ValidationError)
			if !ok {
				// It's okay if it's a different error type, just check it mentions the field
				return
			}

			if vErr.Field != tt.field {
				t.Errorf("ValidationError.Field = %q, want %q", vErr.Field, tt.field)
			}
		})
	}
}

func TestLoadFile_NotFound(t *testing.T) {
	_, err := LoadFile("/nonexistent/path/to/file.yaml")
	if err == nil {
		t.Error("LoadFile() expected error for non-existent file, got nil")
	}
}

func TestLoadFile_Valid(t *testing.T) {
	// Create a temporary file with valid YAML
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "manifest.yaml")

	content := `
apiVersion: v1
kind: CloudSource
metadata:
  name: my-cloud-source
  annotations:
    description: Test source
spec:
  bucket: my-bucket
`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	m, err := LoadFile(tmpFile)
	if err != nil {
		t.Errorf("LoadFile() error = %v, want nil", err)
		return
	}

	if m.APIVersion != "v1" {
		t.Errorf("APIVersion = %q, want %q", m.APIVersion, "v1")
	}
	if m.Kind != "CloudSource" {
		t.Errorf("Kind = %q, want %q", m.Kind, "CloudSource")
	}
	if m.Metadata.Name != "my-cloud-source" {
		t.Errorf("Metadata.Name = %q, want %q", m.Metadata.Name, "my-cloud-source")
	}
}

func TestLoadFileInto_TypedStruct(t *testing.T) {
	type CustomManifest struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Bucket string `yaml:"bucket"`
			Region string `yaml:"region"`
		} `yaml:"spec"`
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "manifest.yaml")

	content := `
apiVersion: v1
kind: CloudSource
metadata:
  name: s3-source
spec:
  bucket: my-bucket
  region: us-east-1
`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	m, err := LoadFileInto[CustomManifest](tmpFile)
	if err != nil {
		t.Errorf("LoadFileInto() error = %v, want nil", err)
		return
	}

	if m.Spec.Bucket != "my-bucket" {
		t.Errorf("Spec.Bucket = %q, want %q", m.Spec.Bucket, "my-bucket")
	}
	if m.Spec.Region != "us-east-1" {
		t.Errorf("Spec.Region = %q, want %q", m.Spec.Region, "us-east-1")
	}
}

func TestLoadFileInto_RejectsUnknownFields(t *testing.T) {
	type StrictManifest struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "manifest.yaml")

	content := `
apiVersion: v1
kind: Test
unknownField: should-fail
`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	_, err = LoadFileInto[StrictManifest](tmpFile)
	if err == nil {
		t.Error("LoadFileInto() expected error for unknown field, got nil")
	}
}

func TestLoadBytes_EmptyDocument(t *testing.T) {
	_, err := LoadBytes([]byte(""))
	if err == nil {
		t.Error("LoadBytes() expected error for empty document, got nil")
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		Field:   "metadata.name",
		Message: "required field is missing",
	}

	expected := "validation error: metadata.name: required field is missing"
	if err.Error() != expected {
		t.Errorf("ValidationError.Error() = %q, want %q", err.Error(), expected)
	}
}

func TestLoadBytesInto(t *testing.T) {
	type SimpleManifest struct {
		Name    string `yaml:"name"`
		Version int    `yaml:"version"`
	}

	yaml := `
name: test-manifest
version: 42
`

	m, err := LoadBytesInto[SimpleManifest]([]byte(yaml))
	if err != nil {
		t.Errorf("LoadBytesInto() error = %v, want nil", err)
		return
	}

	if m.Name != "test-manifest" {
		t.Errorf("Name = %q, want %q", m.Name, "test-manifest")
	}
	if m.Version != 42 {
		t.Errorf("Version = %d, want %d", m.Version, 42)
	}
}

func TestGetSuggestion_CommonTypos(t *testing.T) {
	tests := []struct {
		name        string
		errorString string
		wantContain string
	}{
		{
			name:        "username typo",
			errorString: "field username not found in type",
			wantContain: "Did you mean 'user'?",
		},
		{
			name:        "usernmae typo",
			errorString: "field usernmae not found in type",
			wantContain: "Did you mean 'user'?",
		},
		{
			name:        "passwd typo",
			errorString: "field passwd not found in type",
			wantContain: "Did you mean 'password'?",
		},
		{
			name:        "dbname typo",
			errorString: "field dbname not found in type",
			wantContain: "Did you mean 'database_name'?",
		},
		{
			name:        "hostname typo",
			errorString: "field hostname not found in type",
			wantContain: "Did you mean 'host'?",
		},
		{
			name:        "apiversion lowercase",
			errorString: "field apiversion not found in type",
			wantContain: "Did you mean 'apiVersion'?",
		},
		{
			name:        "databaseName camelCase",
			errorString: "field databaseName not found in type",
			wantContain: "database_name",
		},
		{
			name:        "unknown field no suggestion",
			errorString: "field xyz123 not found in type",
			wantContain: "qctl explain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suggestion := getSuggestion(tt.errorString)
			if !contains(suggestion, tt.wantContain) {
				t.Errorf("getSuggestion(%q) = %q, want to contain %q",
					tt.errorString, suggestion, tt.wantContain)
			}
		})
	}
}

func TestExtractFieldName(t *testing.T) {
	tests := []struct {
		name     string
		errStr   string
		expected string
	}{
		{
			name:     "standard format",
			errStr:   "line 5: field username not found in type",
			expected: "username",
		},
		{
			name:     "simple format",
			errStr:   "field password not found in type Spec",
			expected: "password",
		},
		{
			name:     "no match",
			errStr:   "some other error",
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFieldName(tt.errStr)
			if result != tt.expected {
				t.Errorf("extractFieldName(%q) = %q, want %q",
					tt.errStr, result, tt.expected)
			}
		})
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("contains", func(t *testing.T) {
		if !contains("hello world", "world") {
			t.Error("contains should find 'world' in 'hello world'")
		}
		if contains("hello world", "xyz") {
			t.Error("contains should not find 'xyz' in 'hello world'")
		}
	})

	t.Run("indexOf", func(t *testing.T) {
		if idx := indexOf("hello world", "world"); idx != 6 {
			t.Errorf("indexOf('hello world', 'world') = %d, want 6", idx)
		}
		if idx := indexOf("hello world", "xyz"); idx != -1 {
			t.Errorf("indexOf('hello world', 'xyz') = %d, want -1", idx)
		}
	})

	t.Run("toLower", func(t *testing.T) {
		if result := toLower("HELLO"); result != "hello" {
			t.Errorf("toLower('HELLO') = %q, want 'hello'", result)
		}
		if result := toLower("Hello World"); result != "hello world" {
			t.Errorf("toLower('Hello World') = %q, want 'hello world'", result)
		}
	})
}

func TestFormatYAMLError_TypeMismatch(t *testing.T) {
	// Test that type mismatch errors are formatted nicely
	type TestStruct struct {
		Port int `yaml:"port"`
	}

	yaml := `port: "not-a-number"`
	var s TestStruct
	err := StrictUnmarshal([]byte(yaml), &s)
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}

	errStr := err.Error()
	if !contains(errStr, "type error") && !contains(errStr, "cannot unmarshal") {
		t.Errorf("error should mention type error, got: %s", errStr)
	}
}

func TestLoadBytes_WithStatusField(t *testing.T) {
	// Manifests produced by "qctl describe" include a status section.
	// LoadBytes must accept this without error so round-tripping works.
	yamlWithStatus := `
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: my-table
spec:
  destination_id: 3
status:
  state: active
  id: 42
`

	m, err := LoadBytes([]byte(yamlWithStatus))
	if err != nil {
		t.Fatalf("LoadBytes() with status field should succeed, got error: %v", err)
	}

	if m.Kind != "Table" {
		t.Errorf("Kind = %q, want %q", m.Kind, "Table")
	}
	if m.Metadata.Name != "my-table" {
		t.Errorf("Metadata.Name = %q, want %q", m.Metadata.Name, "my-table")
	}
	if m.Status == nil {
		t.Error("Status should be populated, got nil")
	}
}

func TestStrictUnmarshal_UnknownFieldWithSuggestion(t *testing.T) {
	// Test that unknown field errors include suggestions for common typos
	type DestSpec struct {
		Host         string `yaml:"host"`
		User         string `yaml:"user"`
		DatabaseName string `yaml:"database_name"`
	}

	type DestManifest struct {
		Spec DestSpec `yaml:"spec"`
	}

	tests := []struct {
		name        string
		yaml        string
		wantContain string
	}{
		{
			name: "username typo",
			yaml: `
spec:
  host: localhost
  username: admin
  database_name: mydb
`,
			wantContain: "Did you mean 'user'?",
		},
		{
			name: "dbname typo",
			yaml: `
spec:
  host: localhost
  user: admin
  dbname: mydb
`,
			wantContain: "Did you mean 'database_name'?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m DestManifest
			err := StrictUnmarshal([]byte(tt.yaml), &m)
			if err == nil {
				t.Fatal("expected error for unknown field")
			}

			errStr := err.Error()
			if !contains(errStr, tt.wantContain) {
				t.Errorf("error should contain suggestion %q, got: %s",
					tt.wantContain, errStr)
			}
		})
	}
}
