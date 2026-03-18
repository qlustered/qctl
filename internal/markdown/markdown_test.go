package markdown

import (
	"bytes"
	"strings"
	"testing"
)

func TestPreprocessCustomSyntax(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple custom syntax",
			input:    "Column ~|email|~ is missing",
			expected: "Column " + placeholderStart + "email" + placeholderEnd + " is missing",
		},
		{
			name:     "multiple custom syntax",
			input:    "Fields ~|name|~ and ~|email|~ are required",
			expected: "Fields " + placeholderStart + "name" + placeholderEnd + " and " + placeholderStart + "email" + placeholderEnd + " are required",
		},
		{
			name:     "no custom syntax",
			input:    "Just regular text",
			expected: "Just regular text",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "custom syntax with spaces",
			input:    "Missing ~|user name|~ field",
			expected: "Missing " + placeholderStart + "user name" + placeholderEnd + " field",
		},
		{
			name:     "adjacent to markdown",
			input:    "**bold** ~|value|~ *italic*",
			expected: "**bold** " + placeholderStart + "value" + placeholderEnd + " *italic*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PreprocessCustomSyntax(tt.input)
			if result != tt.expected {
				t.Errorf("PreprocessCustomSyntax(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPostprocessCustomSyntax(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		isTTY    bool
		expected string
	}{
		{
			name:     "TTY with placeholder",
			input:    "Column " + placeholderStart + "email" + placeholderEnd + " is missing",
			isTTY:    true,
			expected: "Column " + boldRedStart + "email" + boldRedEnd + " is missing",
		},
		{
			name:     "non-TTY strips placeholders",
			input:    "Column " + placeholderStart + "email" + placeholderEnd + " is missing",
			isTTY:    false,
			expected: "Column email is missing",
		},
		{
			name:     "no placeholders",
			input:    "Just regular text",
			isTTY:    true,
			expected: "Just regular text",
		},
		{
			name:     "multiple placeholders TTY",
			input:    placeholderStart + "a" + placeholderEnd + " and " + placeholderStart + "b" + placeholderEnd,
			isTTY:    true,
			expected: boldRedStart + "a" + boldRedEnd + " and " + boldRedStart + "b" + boldRedEnd,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PostprocessCustomSyntax(tt.input, tt.isTTY)
			if result != tt.expected {
				t.Errorf("PostprocessCustomSyntax(%q, %v) = %q, want %q",
					tt.input, tt.isTTY, result, tt.expected)
			}
		})
	}
}

func TestHasCustomSyntax(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "has custom syntax",
			input:    "Missing ~|email|~ column",
			expected: true,
		},
		{
			name:     "no custom syntax",
			input:    "Just regular text",
			expected: false,
		},
		{
			name:     "partial syntax - no closing",
			input:    "Missing ~|email column",
			expected: false,
		},
		{
			name:     "partial syntax - no opening",
			input:    "Missing email|~ column",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasCustomSyntax(tt.input)
			if result != tt.expected {
				t.Errorf("HasCustomSyntax(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "strip bold red",
			input:    "Column \x1b[1;31memail\x1b[0m is missing",
			expected: "Column email is missing",
		},
		{
			name:     "strip multiple codes",
			input:    "\x1b[1mBold\x1b[0m and \x1b[32mgreen\x1b[0m",
			expected: "Bold and green",
		},
		{
			name:     "no ANSI codes",
			input:    "Just plain text",
			expected: "Just plain text",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "complex CSI sequence",
			input:    "\x1b[38;5;196mred text\x1b[0m",
			expected: "red text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripANSI(tt.input)
			if result != tt.expected {
				t.Errorf("StripANSI(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestHasANSI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "has ANSI",
			input:    "\x1b[1;31mred\x1b[0m",
			expected: true,
		},
		{
			name:     "no ANSI",
			input:    "plain text",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasANSI(tt.input)
			if result != tt.expected {
				t.Errorf("HasANSI(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRendererNew(t *testing.T) {
	// Test with nil writer (defaults to stdout)
	r, err := New(Options{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if r == nil {
		t.Fatal("New() returned nil renderer")
	}
	// Width should be DefaultWidth when no TTY detected
	if r.Width() != DefaultWidth {
		t.Errorf("Width() = %d, want %d", r.Width(), DefaultWidth)
	}

	// Test with buffer writer (non-TTY)
	var buf bytes.Buffer
	r, err = New(Options{Writer: &buf})
	if err != nil {
		t.Fatalf("New() with buffer failed: %v", err)
	}
	if r.IsTTY() {
		t.Error("IsTTY() should be false for buffer writer")
	}

	// Test with ForceColor
	r, err = New(Options{ForceColor: true})
	if err != nil {
		t.Fatalf("New() with ForceColor failed: %v", err)
	}
	if !r.IsTTY() {
		t.Error("IsTTY() should be true when ForceColor is set")
	}

	// Test with explicit width
	r, err = New(Options{Width: 120})
	if err != nil {
		t.Fatalf("New() with Width failed: %v", err)
	}
	if r.Width() != 120 {
		t.Errorf("Width() = %d, want 120", r.Width())
	}
}

func TestRendererRender(t *testing.T) {
	// Create a non-TTY renderer for predictable output
	r, err := New(Options{Width: 80})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	tests := []struct {
		name        string
		input       string
		shouldError bool
	}{
		{
			name:        "plain text",
			input:       "Hello world",
			shouldError: false,
		},
		{
			name:        "markdown bold",
			input:       "**bold text**",
			shouldError: false,
		},
		{
			name:        "custom syntax",
			input:       "Column ~|email|~ is missing",
			shouldError: false,
		},
		{
			name:        "mixed content",
			input:       "**Important**: Column ~|email|~ is missing. See *documentation*.",
			shouldError: false,
		},
		{
			name:        "empty string",
			input:       "",
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := r.Render(tt.input)
			if tt.shouldError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if !tt.shouldError && tt.input != "" && result == "" {
				t.Error("Expected non-empty result")
			}
		})
	}
}

func TestRendererRenderPlain(t *testing.T) {
	r, err := New(Options{ForceColor: true, Width: 80})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Custom syntax should be stripped in plain mode
	input := "Column ~|email|~ is missing"
	result, err := r.RenderPlain(input)
	if err != nil {
		t.Fatalf("RenderPlain() failed: %v", err)
	}

	// Should not contain ANSI codes
	if HasANSI(result) {
		t.Error("RenderPlain() output should not contain ANSI codes")
	}

	// Should not contain placeholders
	if strings.Contains(result, placeholderStart) || strings.Contains(result, placeholderEnd) {
		t.Error("RenderPlain() output should not contain placeholders")
	}

	// Should contain the plain text
	if !strings.Contains(result, "email") {
		t.Error("RenderPlain() output should contain 'email'")
	}
}

func TestRendererRenderField(t *testing.T) {
	r, err := New(Options{Width: 80})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Empty string should return empty
	result := r.RenderField("")
	if result != "" {
		t.Errorf("RenderField('') = %q, want empty string", result)
	}

	// Non-empty should return rendered content
	result = r.RenderField("Hello world")
	if result == "" {
		t.Error("RenderField() returned empty for non-empty input")
	}

	// Should not have trailing newlines
	if strings.HasSuffix(result, "\n") {
		t.Error("RenderField() should not have trailing newlines")
	}
}

func TestRendererRenderFieldPlain(t *testing.T) {
	r, err := New(Options{ForceColor: true, Width: 80})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Empty string should return empty
	result := r.RenderFieldPlain("")
	if result != "" {
		t.Errorf("RenderFieldPlain('') = %q, want empty string", result)
	}

	// Should strip ANSI even with ForceColor
	result = r.RenderFieldPlain("~|value|~")
	if HasANSI(result) {
		t.Error("RenderFieldPlain() should not contain ANSI codes")
	}
}

func TestTrimWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "trailing newline",
			input:    "hello\n",
			expected: "hello",
		},
		{
			name:     "multiple trailing newlines",
			input:    "hello\n\n\n",
			expected: "hello",
		},
		{
			name:     "leading newline",
			input:    "\nhello",
			expected: "hello",
		},
		{
			name:     "leading and trailing newlines",
			input:    "\n\nhello\n\n",
			expected: "hello",
		},
		{
			name:     "leading spaces",
			input:    "  hello",
			expected: "hello",
		},
		{
			name:     "trailing spaces",
			input:    "hello  ",
			expected: "hello",
		},
		{
			name:     "glamour-style output",
			input:    "\n  Simple message                    ",
			expected: "Simple message",
		},
		{
			name:     "no whitespace",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only whitespace",
			input:    "\n  \n  ",
			expected: "",
		},
		{
			name:     "newline in middle preserved",
			input:    "\nhello\nworld\n",
			expected: "hello\nworld",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trimWhitespace(tt.input)
			if result != tt.expected {
				t.Errorf("trimWhitespace(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCustomSyntaxEndToEnd(t *testing.T) {
	// Test the full flow: preprocess -> (simulated render) -> postprocess

	input := "Column ~|email|~ is missing. See **documentation**."

	// Preprocess
	preprocessed := PreprocessCustomSyntax(input)
	if !strings.Contains(preprocessed, placeholderStart) {
		t.Error("Preprocessing should add placeholder markers")
	}
	if strings.Contains(preprocessed, "~|") {
		t.Error("Preprocessing should remove ~| markers")
	}

	// Postprocess for TTY
	ttyOutput := PostprocessCustomSyntax(preprocessed, true)
	if !strings.Contains(ttyOutput, boldRedStart) {
		t.Error("TTY postprocessing should add ANSI codes")
	}
	if strings.Contains(ttyOutput, placeholderStart) {
		t.Error("TTY postprocessing should remove placeholders")
	}

	// Postprocess for non-TTY
	plainOutput := PostprocessCustomSyntax(preprocessed, false)
	if strings.Contains(plainOutput, boldRedStart) {
		t.Error("Plain postprocessing should not have ANSI codes")
	}
	if strings.Contains(plainOutput, placeholderStart) {
		t.Error("Plain postprocessing should remove placeholders")
	}
	if !strings.Contains(plainOutput, "email") {
		t.Error("Plain output should still contain the value")
	}
}
