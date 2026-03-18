package markdown

import (
	"reflect"
	"testing"
)

func TestProcessFieldsPlain(t *testing.T) {
	tests := []struct {
		name       string
		data       interface{}
		fieldNames []string
		checkFunc  func(t *testing.T, result interface{})
	}{
		{
			name: "process custom syntax in struct",
			data: struct {
				Message string `json:"message"`
				ID      int    `json:"id"`
			}{
				Message: "Column ~|email|~ is missing",
				ID:      1,
			},
			fieldNames: []string{"message"},
			checkFunc: func(t *testing.T, result interface{}) {
				m, ok := result.(map[string]interface{})
				if !ok {
					t.Fatalf("expected map, got %T", result)
				}
				msg, ok := m["message"].(string)
				if !ok {
					t.Fatalf("expected string message, got %T", m["message"])
				}
				if msg != "Column email is missing" {
					t.Errorf("expected 'Column email is missing', got %q", msg)
				}
				if m["id"] != 1 {
					t.Errorf("expected id=1, got %v", m["id"])
				}
			},
		},
		{
			name: "process bold markdown",
			data: struct {
				Message string `json:"message"`
			}{
				Message: "This is **bold** text",
			},
			fieldNames: []string{"message"},
			checkFunc: func(t *testing.T, result interface{}) {
				m := result.(map[string]interface{})
				msg := m["message"].(string)
				if msg != "This is bold text" {
					t.Errorf("expected 'This is bold text', got %q", msg)
				}
			},
		},
		{
			name: "no fields to process returns original",
			data: struct {
				Message string `json:"message"`
			}{
				Message: "~|value|~",
			},
			fieldNames: []string{},
			checkFunc: func(t *testing.T, result interface{}) {
				// When no fields specified, original data is returned unchanged
				s, ok := result.(struct {
					Message string `json:"message"`
				})
				if !ok {
					t.Fatalf("expected original struct type, got %T", result)
				}
				if s.Message != "~|value|~" {
					t.Errorf("expected '~|value|~', got %q", s.Message)
				}
			},
		},
		{
			name: "process map",
			data: map[string]interface{}{
				"message": "Column ~|id|~ required",
				"count":   5,
			},
			fieldNames: []string{"message"},
			checkFunc: func(t *testing.T, result interface{}) {
				m := result.(map[string]interface{})
				msg := m["message"].(string)
				if msg != "Column id required" {
					t.Errorf("expected 'Column id required', got %q", msg)
				}
				if m["count"] != 5 {
					t.Errorf("expected count=5, got %v", m["count"])
				}
			},
		},
		{
			name: "process nested struct",
			data: struct {
				Spec struct {
					Message string `json:"message"`
				} `json:"spec"`
			}{
				Spec: struct {
					Message string `json:"message"`
				}{
					Message: "~|warning|~ detected",
				},
			},
			fieldNames: []string{"message"},
			checkFunc: func(t *testing.T, result interface{}) {
				m := result.(map[string]interface{})
				spec := m["spec"].(map[string]interface{})
				msg := spec["message"].(string)
				if msg != "warning detected" {
					t.Errorf("expected 'warning detected', got %q", msg)
				}
			},
		},
		{
			name: "process slice of structs",
			data: []struct {
				Message string `json:"message"`
			}{
				{Message: "~|first|~"},
				{Message: "~|second|~"},
			},
			fieldNames: []string{"message"},
			checkFunc: func(t *testing.T, result interface{}) {
				slice := result.([]interface{})
				if len(slice) != 2 {
					t.Fatalf("expected 2 items, got %d", len(slice))
				}
				first := slice[0].(map[string]interface{})
				if first["message"] != "first" {
					t.Errorf("expected 'first', got %v", first["message"])
				}
				second := slice[1].(map[string]interface{})
				if second["message"] != "second" {
					t.Errorf("expected 'second', got %v", second["message"])
				}
			},
		},
		{
			name:       "nil data",
			data:       nil,
			fieldNames: []string{"message"},
			checkFunc: func(t *testing.T, result interface{}) {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
			},
		},
		{
			name: "empty string field",
			data: struct {
				Message string `json:"message"`
			}{
				Message: "",
			},
			fieldNames: []string{"message"},
			checkFunc: func(t *testing.T, result interface{}) {
				m := result.(map[string]interface{})
				if m["message"] != "" {
					t.Errorf("expected empty string, got %v", m["message"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProcessFieldsPlain(tt.data, tt.fieldNames)
			tt.checkFunc(t, result)
		})
	}
}

func TestStripMarkdownSyntax(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bold with asterisks",
			input:    "This is **bold** text",
			expected: "This is bold text",
		},
		{
			name:     "bold with underscores",
			input:    "This is __bold__ text",
			expected: "This is bold text",
		},
		{
			name:     "italic with asterisk",
			input:    "This is *italic* text",
			expected: "This is italic text",
		},
		{
			name:     "inline code",
			input:    "Run `command` here",
			expected: "Run command here",
		},
		{
			name:     "mixed formatting",
			input:    "**Bold** and *italic* and `code`",
			expected: "Bold and italic and code",
		},
		{
			name:     "no markdown",
			input:    "Plain text here",
			expected: "Plain text here",
		},
		{
			name:     "unclosed bold",
			input:    "**unclosed bold",
			expected: "*unclosed bold", // One asterisk gets stripped by italic processing
		},
		{
			name:     "underscore in middle of word",
			input:    "snake_case_variable",
			expected: "snake_case_variable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripMarkdownSyntax(tt.input)
			if result != tt.expected {
				t.Errorf("stripMarkdownSyntax(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRenderPlainText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "custom syntax only",
			input:    "Column ~|email|~ is missing",
			expected: "Column email is missing",
		},
		{
			name:     "markdown only",
			input:    "See **documentation** for details",
			expected: "See documentation for details",
		},
		{
			name:     "mixed custom and markdown",
			input:    "Column ~|email|~ is **required**",
			expected: "Column email is required",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderPlainText(tt.input)
			if result != tt.expected {
				t.Errorf("renderPlainText(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestProcessFieldsPlain_PreservesNonStringFields(t *testing.T) {
	data := struct {
		Message string  `json:"message"`
		Count   int     `json:"count"`
		Active  bool    `json:"active"`
		Score   float64 `json:"score"`
	}{
		Message: "~|test|~",
		Count:   42,
		Active:  true,
		Score:   3.14,
	}

	result := ProcessFieldsPlain(data, []string{"message"})
	m := result.(map[string]interface{})

	if m["message"] != "test" {
		t.Errorf("expected 'test', got %v", m["message"])
	}
	if m["count"] != 42 {
		t.Errorf("expected 42, got %v", m["count"])
	}
	if m["active"] != true {
		t.Errorf("expected true, got %v", m["active"])
	}
	if !reflect.DeepEqual(m["score"], 3.14) {
		t.Errorf("expected 3.14, got %v", m["score"])
	}
}
