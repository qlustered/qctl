package markdown

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"gopkg.in/yaml.v3"
)

type customJSONValue struct {
	message string
}

func (v customJSONValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"message": v.message})
}

type customTextValue [2]byte

func (v customTextValue) MarshalText() ([]byte, error) {
	return []byte("canonical-text"), nil
}

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
			name: "no fields to process still converts",
			data: struct {
				Message string `json:"message"`
			}{
				Message: "~|value|~",
			},
			fieldNames: []string{},
			checkFunc: func(t *testing.T, result interface{}) {
				m, ok := result.(map[string]interface{})
				if !ok {
					t.Fatalf("expected map, got %T", result)
				}
				if m["message"] != "~|value|~" {
					t.Errorf("expected '~|value|~', got %q", m["message"])
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

func TestProcessFieldsPlainHonorsCustomMarshalers(t *testing.T) {
	data := struct {
		JSON customJSONValue `json:"json"`
		Text customTextValue `json:"text"`
	}{
		JSON: customJSONValue{message: "This is **important**"},
		Text: customTextValue{1, 2},
	}

	result := ProcessFieldsPlain(data, []string{"message"}).(map[string]interface{})
	jsonValue := result["json"].(map[string]interface{})
	if jsonValue["message"] != "This is important" {
		t.Errorf("custom JSON message = %q, want plain text", jsonValue["message"])
	}
	if result["text"] != "canonical-text" {
		t.Errorf("custom text value = %v, want canonical-text", result["text"])
	}
}

// describe's json and yaml views run message fields through renderPlainText. A
// marked value comes back whole, its escapes decoded, and neither the ~|..|~
// pass nor the star and underscore stripping reaches inside it.
func TestProcessFieldsPlainDecodesValueMarkers(t *testing.T) {
	data := map[string]interface{}{
		"message": `~|"~*snake_case_name **not bold** a|~b C:\\temp\\*~"|~ is invalid in **model**`,
	}
	got := ProcessFieldsPlain(data, []string{"message"}).(map[string]interface{})["message"]
	if want := `"snake_case_name **not bold** a|~b C:\temp\" is invalid in model`; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// ProcessFieldsPlain feeds JSON/YAML encoders, not a terminal renderer. The
// encoder will quote controls safely on the output stream, but decoding that
// document must still recover the exact server value.
func TestProcessFieldsPlainDoesNotSanitizeStructuredValues(t *testing.T) {
	const input = "**value\x1b[31m\x00\x7f\u0085**"
	got := ProcessFieldsPlain(map[string]interface{}{"message": input}, []string{"message"}).(map[string]interface{})["message"]
	if want := "value\x1b[31m\x00\x7f\u0085"; got != want {
		t.Errorf("message = %q, want original controls in %q", got, want)
	}
}

// A number in a generated union comes out as an int64 when it fits one, and as
// the json.Number of its literal when it does not.
func TestProcessFieldsPlainKeepsUnionIntegers(t *testing.T) {
	var union api.DryRunJobFullSchema_StructuredSummary
	if err := union.UnmarshalJSON([]byte(`{"rows": 1234567, "huge": 18446744073709551616}`)); err != nil {
		t.Fatal(err)
	}
	data := struct {
		Summary api.DryRunJobFullSchema_StructuredSummary `json:"summary"`
	}{union}

	summary := ProcessFieldsPlain(data, []string{"message"}).(map[string]interface{})["summary"].(map[string]interface{})
	if got, ok := summary["rows"].(int64); !ok || got != 1234567 {
		t.Errorf("rows = %T(%v), want int64(1234567)", summary["rows"], summary["rows"])
	}
	if got, ok := summary["huge"].(json.Number); !ok || got != "18446744073709551616" {
		t.Errorf("huge = %T(%v), want json.Number(18446744073709551616)", summary["huge"], summary["huge"])
	}
}

type failingPointerJSON struct{ Name string }

func (*failingPointerJSON) MarshalJSON() ([]byte, error) {
	return nil, errors.New("pointer method failed")
}

type failingJSON struct{ Name string }

func (failingJSON) MarshalJSON() ([]byte, error) { return nil, errors.New("json method failed") }

type failingText struct{ Name string }

func (failingText) MarshalText() ([]byte, error) { return nil, errors.New("text method failed") }

// A field whose MarshalJSON or MarshalText fails makes the result fail to
// encode as JSON and as YAML with that method's error, and none of the
// field's own fields is written.
func TestProcessFieldsPlainKeepsAFailingMarshaler(t *testing.T) {
	encoders := map[string]func(io.Writer, interface{}) error{
		"json": func(w io.Writer, v interface{}) error { return json.NewEncoder(w).Encode(v) },
		"yaml": func(w io.Writer, v interface{}) error {
			enc := yaml.NewEncoder(w)
			if err := enc.Encode(v); err != nil {
				return err
			}
			return enc.Close()
		},
	}
	for format, encode := range encoders {
		for _, tc := range []struct {
			name string
			data interface{}
			want string
		}{
			{"pointer MarshalJSON", &struct {
				F failingPointerJSON `json:"f"`
			}{failingPointerJSON{Name: "go-field"}}, "pointer method failed"},
			{"MarshalJSON", struct {
				F failingJSON `json:"f"`
			}{failingJSON{Name: "go-field"}}, "json method failed"},
			{"MarshalText", struct {
				F failingText `json:"f"`
			}{failingText{Name: "go-field"}}, "text method failed"},
		} {
			t.Run(tc.name+" -o "+format, func(t *testing.T) {
				var buf bytes.Buffer
				err := encode(&buf, ProcessFieldsPlain(tc.data, []string{"message"}))
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("encode error = %v, want one containing %q", err, tc.want)
				}
				if strings.Contains(buf.String(), "go-field") {
					t.Errorf("wrote the field:\n%s", buf.String())
				}
			})
		}
	}
}
