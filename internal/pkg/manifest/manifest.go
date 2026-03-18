package manifest

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// API version constants for manifest schemas
const (
	// APIVersionV1 is the current manifest schema version
	APIVersionV1 = "qluster.ai/v1"
)

// Metadata holds resource metadata
type Metadata struct {
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
	Name        string            `yaml:"name,omitempty" json:"name,omitempty"`
	ID          string            `yaml:"id,omitempty" json:"id,omitempty"`
}

// Manifest is the universal wrapper for all resources
type Manifest struct {
	Spec       interface{} `yaml:"spec" json:"spec"`
	Metadata   Metadata    `yaml:"metadata" json:"metadata"`
	Kind       string      `yaml:"kind" json:"kind"`
	APIVersion string      `yaml:"apiVersion" json:"apiVersion"`
	Status     interface{} `yaml:"status,omitempty" json:"status,omitempty"`
}

// ValidationError represents a manifest validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s: %s", e.Field, e.Message)
}

// StrictUnmarshal parses YAML with strict validation (errors on unknown fields).
// Uses gopkg.in/yaml.v3 with KnownFields(true) to reject unknown fields,
// preventing AI agent typos from silently being ignored.
func StrictUnmarshal(data []byte, v interface{}) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	if err := decoder.Decode(v); err != nil {
		if err == io.EOF {
			return fmt.Errorf("empty YAML document")
		}
		// Enhance error messages for common issues
		return formatYAMLError(err)
	}

	return nil
}

// formatYAMLError converts YAML parsing errors into user-friendly messages
func formatYAMLError(err error) error {
	errStr := err.Error()

	// Check for unknown field errors from yaml.v3
	// Format: "yaml: unmarshal errors:\n  line X: field Y not found in type Z"
	if contains(errStr, "field") && contains(errStr, "not found in type") {
		return &ValidationError{
			Field:   extractFieldName(errStr),
			Message: fmt.Sprintf("unknown field (possible typo). %s", getSuggestion(errStr)),
		}
	}

	// Check for type mismatch errors
	if contains(errStr, "cannot unmarshal") {
		return fmt.Errorf("type error: %s. Check that field values match expected types (string, integer, etc.)", errStr)
	}

	// Check for syntax errors
	if contains(errStr, "yaml:") && (contains(errStr, "did not find expected") || contains(errStr, "mapping values")) {
		return fmt.Errorf("YAML syntax error: %s. Check indentation and formatting", errStr)
	}

	return fmt.Errorf("YAML parsing failed: %w", err)
}

// extractFieldName attempts to extract the field name from a yaml error
func extractFieldName(errStr string) string {
	// Look for pattern "field X not found"
	if idx := indexOf(errStr, "field "); idx != -1 {
		start := idx + len("field ")
		end := indexOf(errStr[start:], " not found")
		if end != -1 {
			return errStr[start : start+end]
		}
	}
	return "unknown"
}

// getSuggestion returns helpful suggestions for common typos
func getSuggestion(errStr string) string {
	suggestions := map[string]string{
		"usernmae":       "Did you mean 'user'?",
		"username":       "Did you mean 'user'?",
		"passwd":         "Did you mean 'password'?",
		"pass":           "Did you mean 'password'?",
		"dbname":         "Did you mean 'database_name'?",
		"db_name":        "Did you mean 'database_name'?",
		"database":       "Did you mean 'database_name'?",
		"db":             "Did you mean 'database_name'?",
		"hostname":       "Did you mean 'host'?",
		"server":         "Did you mean 'host'?",
		"addr":           "Did you mean 'host'?",
		"address":        "Did you mean 'host'?",
		"timeout":        "Did you mean 'connect_timeout'?",
		"connexTimeout":  "Did you mean 'connect_timeout'?",
		"apiversion":     "Did you mean 'apiVersion'? (note the capital V)",
		"api_version":    "Did you mean 'apiVersion'?",
		"databaseName":   "Did you mean 'database_name'? (use snake_case for spec fields)",
		"connectTimeout": "Did you mean 'connect_timeout'? (use snake_case for spec fields)",
	}

	errLower := toLower(errStr)
	for typo, suggestion := range suggestions {
		if contains(errLower, toLower(typo)) {
			return suggestion
		}
	}

	return "Run 'qctl explain <resource>' to see valid field names."
}

// Helper functions to avoid strings import (keep package small)
func contains(s, substr string) bool {
	return indexOf(s, substr) != -1
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}

// LoadFile reads and parses a manifest file with strict validation.
// Returns an error if the file contains unknown fields or is missing required fields.
func LoadFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file %s: %w", path, err)
	}

	var manifest Manifest
	if err := StrictUnmarshal(data, &manifest); err != nil {
		return nil, err
	}

	// Validate required fields
	if err := validateManifest(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// LoadFileInto reads and parses a manifest into a typed struct.
// The type T should match the expected manifest structure.
func LoadFileInto[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file %s: %w", path, err)
	}

	var result T
	if err := StrictUnmarshal(data, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// LoadBytes parses manifest data from bytes with strict validation.
func LoadBytes(data []byte) (*Manifest, error) {
	var manifest Manifest
	if err := StrictUnmarshal(data, &manifest); err != nil {
		return nil, err
	}

	// Validate required fields
	if err := validateManifest(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// LoadBytesInto parses manifest data from bytes into a typed struct.
func LoadBytesInto[T any](data []byte) (*T, error) {
	var result T
	if err := StrictUnmarshal(data, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// validateManifest validates required fields in a manifest
func validateManifest(m *Manifest) error {
	if m.APIVersion == "" {
		return &ValidationError{Field: "apiVersion", Message: "required field is missing"}
	}
	if m.Kind == "" {
		return &ValidationError{Field: "kind", Message: "required field is missing"}
	}
	if m.Metadata.Name == "" && m.Metadata.ID == "" {
		return &ValidationError{Field: "metadata.name", Message: "required field is missing (or provide metadata.id)"}
	}
	return nil
}
