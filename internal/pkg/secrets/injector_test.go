package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandEnv_AllPresent(t *testing.T) {
	// Set up test environment variables
	os.Setenv("TEST_USER", "admin")
	os.Setenv("TEST_PASS", "secret123")
	defer os.Unsetenv("TEST_USER")
	defer os.Unsetenv("TEST_PASS")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "braced variable",
			input:    "user: ${TEST_USER}",
			expected: "user: admin",
		},
		{
			name:     "unbraced variable",
			input:    "user: $TEST_USER",
			expected: "user: admin",
		},
		{
			name:     "multiple variables",
			input:    "user: ${TEST_USER}, pass: ${TEST_PASS}",
			expected: "user: admin, pass: secret123",
		},
		{
			name:     "mixed braced and unbraced",
			input:    "user: $TEST_USER, pass: ${TEST_PASS}",
			expected: "user: admin, pass: secret123",
		},
		{
			name:     "variable in YAML context",
			input:    "password: ${TEST_PASS}\nusername: ${TEST_USER}",
			expected: "password: secret123\nusername: admin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandEnv([]byte(tt.input))
			if err != nil {
				t.Errorf("ExpandEnv() error = %v, want nil", err)
				return
			}

			if string(result) != tt.expected {
				t.Errorf("ExpandEnv() = %q, want %q", string(result), tt.expected)
			}
		})
	}
}

func TestExpandEnv_MissingVar(t *testing.T) {
	// Make sure the variable doesn't exist
	os.Unsetenv("NONEXISTENT_VAR")

	input := "secret: ${NONEXISTENT_VAR}"
	_, err := ExpandEnv([]byte(input))

	if err == nil {
		t.Error("ExpandEnv() expected error for missing variable, got nil")
		return
	}

	if !strings.Contains(err.Error(), "NONEXISTENT_VAR") {
		t.Errorf("Error should mention missing variable name, got: %v", err)
	}

	if !strings.Contains(err.Error(), "missing required environment variables") {
		t.Errorf("Error should contain 'missing required environment variables', got: %v", err)
	}
}

func TestExpandEnv_MultipleMissing(t *testing.T) {
	// Make sure the variables don't exist
	os.Unsetenv("MISSING_VAR_1")
	os.Unsetenv("MISSING_VAR_2")
	os.Unsetenv("MISSING_VAR_3")

	input := "a: ${MISSING_VAR_1}, b: ${MISSING_VAR_2}, c: ${MISSING_VAR_3}"
	_, err := ExpandEnv([]byte(input))

	if err == nil {
		t.Error("ExpandEnv() expected error for missing variables, got nil")
		return
	}

	// Should list ALL missing variables
	errStr := err.Error()
	if !strings.Contains(errStr, "MISSING_VAR_1") {
		t.Errorf("Error should mention MISSING_VAR_1, got: %v", err)
	}
	if !strings.Contains(errStr, "MISSING_VAR_2") {
		t.Errorf("Error should mention MISSING_VAR_2, got: %v", err)
	}
	if !strings.Contains(errStr, "MISSING_VAR_3") {
		t.Errorf("Error should mention MISSING_VAR_3, got: %v", err)
	}
}

func TestExpandEnv_NoVars(t *testing.T) {
	// Content with no variable references should pass through unchanged
	input := "no variables here, just plain text"

	result, err := ExpandEnv([]byte(input))
	if err != nil {
		t.Errorf("ExpandEnv() error = %v, want nil", err)
		return
	}

	if string(result) != input {
		t.Errorf("ExpandEnv() = %q, want %q", string(result), input)
	}
}

func TestExpandEnv_EdgeCases(t *testing.T) {
	os.Setenv("EDGE_VAR", "value")
	defer os.Unsetenv("EDGE_VAR")

	tests := []struct {
		name        string
		input       string
		expected    string
		shouldError bool
	}{
		{
			name:        "empty input",
			input:       "",
			expected:    "",
			shouldError: false,
		},
		{
			name:        "dollar sign not followed by valid var",
			input:       "price: $100",
			expected:    "price: $100",
			shouldError: false,
		},
		{
			name:        "variable at start of string",
			input:       "${EDGE_VAR} is here",
			expected:    "value is here",
			shouldError: false,
		},
		{
			name:        "variable at end of string",
			input:       "here is ${EDGE_VAR}",
			expected:    "here is value",
			shouldError: false,
		},
		{
			name:        "empty braces",
			input:       "empty: ${}",
			expected:    "empty: ${}",
			shouldError: false,
		},
		{
			name:        "nested braces - treated as variable reference",
			input:       "nested: ${${VAR}}",
			expected:    "",
			shouldError: true, // ${VAR is treated as a variable name (missing from env)
		},
		{
			name:        "variable with underscore",
			input:       "${EDGE_VAR}_suffix",
			expected:    "value_suffix",
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandEnv([]byte(tt.input))

			if tt.shouldError {
				if err == nil {
					t.Errorf("ExpandEnv() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("ExpandEnv() error = %v, want nil", err)
				return
			}

			if string(result) != tt.expected {
				t.Errorf("ExpandEnv() = %q, want %q", string(result), tt.expected)
			}
		})
	}
}

func TestExpandEnvFile(t *testing.T) {
	os.Setenv("FILE_TEST_VAR", "file_value")
	defer os.Unsetenv("FILE_TEST_VAR")

	t.Run("reads and expands file", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "test.yaml")

		content := "value: ${FILE_TEST_VAR}\n"
		err := os.WriteFile(tmpFile, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}

		result, err := ExpandEnvFile(tmpFile)
		if err != nil {
			t.Errorf("ExpandEnvFile() error = %v, want nil", err)
			return
		}

		expected := "value: file_value\n"
		if string(result) != expected {
			t.Errorf("ExpandEnvFile() = %q, want %q", string(result), expected)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		_, err := ExpandEnvFile("/nonexistent/path/file.yaml")
		if err == nil {
			t.Error("ExpandEnvFile() expected error for non-existent file, got nil")
		}
	})

	t.Run("file with missing variable", func(t *testing.T) {
		os.Unsetenv("MISSING_FILE_VAR")

		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "test.yaml")

		content := "value: ${MISSING_FILE_VAR}\n"
		err := os.WriteFile(tmpFile, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}

		_, err = ExpandEnvFile(tmpFile)
		if err == nil {
			t.Error("ExpandEnvFile() expected error for missing variable, got nil")
		}
		if !strings.Contains(err.Error(), "MISSING_FILE_VAR") {
			t.Errorf("Error should mention missing variable, got: %v", err)
		}
	})
}

func TestExpandEnv_SameVariableMultipleTimes(t *testing.T) {
	os.Setenv("REPEATED_VAR", "repeated_value")
	defer os.Unsetenv("REPEATED_VAR")

	input := "${REPEATED_VAR} and ${REPEATED_VAR} and $REPEATED_VAR"
	expected := "repeated_value and repeated_value and repeated_value"

	result, err := ExpandEnv([]byte(input))
	if err != nil {
		t.Errorf("ExpandEnv() error = %v, want nil", err)
		return
	}

	if string(result) != expected {
		t.Errorf("ExpandEnv() = %q, want %q", string(result), expected)
	}
}

func TestExpandEnv_EmptyVariableValue(t *testing.T) {
	os.Setenv("EMPTY_VAR", "")
	defer os.Unsetenv("EMPTY_VAR")

	input := "value: ${EMPTY_VAR}"
	expected := "value: "

	result, err := ExpandEnv([]byte(input))
	if err != nil {
		t.Errorf("ExpandEnv() error = %v, want nil", err)
		return
	}

	if string(result) != expected {
		t.Errorf("ExpandEnv() = %q, want %q", string(result), expected)
	}
}

func TestMustExpandEnv(t *testing.T) {
	os.Setenv("MUST_TEST_VAR", "must_value")
	defer os.Unsetenv("MUST_TEST_VAR")

	t.Run("success case", func(t *testing.T) {
		input := "${MUST_TEST_VAR}"
		result := MustExpandEnv([]byte(input))

		if string(result) != "must_value" {
			t.Errorf("MustExpandEnv() = %q, want %q", string(result), "must_value")
		}
	})

	t.Run("panics on missing variable", func(t *testing.T) {
		os.Unsetenv("MISSING_MUST_VAR")

		defer func() {
			if r := recover(); r == nil {
				t.Error("MustExpandEnv() should panic on missing variable")
			}
		}()

		_ = MustExpandEnv([]byte("${MISSING_MUST_VAR}"))
	})
}

func TestExpandEnv_RealWorldYAMLExample(t *testing.T) {
	// Set up realistic environment variables
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("DB_PORT", "5432")
	os.Setenv("DB_USER", "postgres")
	os.Setenv("DB_PASS", "super_secret_password")
	os.Setenv("API_KEY", "sk-1234567890abcdef")
	defer func() {
		os.Unsetenv("DB_HOST")
		os.Unsetenv("DB_PORT")
		os.Unsetenv("DB_USER")
		os.Unsetenv("DB_PASS")
		os.Unsetenv("API_KEY")
	}()

	input := `
apiVersion: v1
kind: CloudSource
metadata:
  name: my-db-source
spec:
  type: postgres
  connection:
    host: ${DB_HOST}
    port: ${DB_PORT}
    username: ${DB_USER}
    password: ${DB_PASS}
  api_key: ${API_KEY}
`

	expected := `
apiVersion: v1
kind: CloudSource
metadata:
  name: my-db-source
spec:
  type: postgres
  connection:
    host: localhost
    port: 5432
    username: postgres
    password: super_secret_password
  api_key: sk-1234567890abcdef
`

	result, err := ExpandEnv([]byte(input))
	if err != nil {
		t.Errorf("ExpandEnv() error = %v, want nil", err)
		return
	}

	if string(result) != expected {
		t.Errorf("ExpandEnv() result mismatch.\nGot:\n%s\nWant:\n%s", string(result), expected)
	}
}
