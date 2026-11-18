package markdown

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
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

func TestRendererEscapesServerControlsButKeepsGeneratedANSI(t *testing.T) {
	r, err := New(Options{ForceColor: true, Width: 200})
	if err != nil {
		t.Fatal(err)
	}
	input := "~|trusted style|~ and raw \x1b[31mred\x1b[0m plus \x1b]0;title\x07"

	rendered, err := r.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if !HasANSI(rendered) {
		t.Fatalf("Render generated no trusted styling: %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[31mred") || strings.Contains(rendered, "\x1b]0;title") {
		t.Errorf("Render retained a server escape sequence: %q", rendered)
	}
	for _, want := range []string{`\x1b[31mred\x1b[0m`, `\x1b]0;title\x07`} {
		if !strings.Contains(StripANSI(rendered), want) {
			t.Errorf("Render did not expose %q: %q", want, rendered)
		}
	}

	plain, err := r.RenderPlain(input)
	if err != nil {
		t.Fatal(err)
	}
	if HasANSI(plain) || strings.ContainsRune(plain, '\x1b') {
		t.Errorf("RenderPlain retained terminal controls: %q", plain)
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

// glamour's Render and RenderPlain extract markers before glamour runs and put
// the values back after it, so a payload's stars are never taken for emphasis.
func TestRendererKeepsMarkedValuesLiteral(t *testing.T) {
	r, err := New(Options{ForceColor: true, Width: 200})
	if err != nil {
		t.Fatal(err)
	}
	in := `Value ~|"~*\*\*x\*\* ~|y|~*~"|~ ok`
	rendered, err := r.Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := boldRedStart + `"**x** ~|y|~"` + boldRedEnd; !strings.Contains(rendered, want) {
		t.Errorf("Render = %q, want it to contain %q", rendered, want)
	}
	plain, err := r.RenderPlain(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := `Value "**x** ~|y|~" ok`; !strings.Contains(plain, want) {
		t.Errorf("RenderPlain = %q, want it to contain %q", plain, want)
	}
}

// A table cell always fits its width. When Inline fits the message by its
// marked values the cell is exactly Inline's; only when it cannot is the cell
// clipped, and then to the width, ending in the ellipsis. On a terminal
// the clip keeps every escape sequence: stripped, the styled cell is the plain
// one, and each style it opens is closed.
func TestRenderCellFitsThenClipsEveryCanonicalMessage(t *testing.T) {
	plain, err := New(Options{Writer: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	styled, err := New(Options{ForceColor: true})
	if err != nil {
		t.Fatal(err)
	}
	clipped, clippedStyled := 0, 0
	for _, c := range append(loadMarkerCases(t).ProducerCases, loadMarkerCases(t).MessageCases...) {
		whole := Inline(c.Message, InlineOptions{})
		for width := 1; width <= displayWidth(whole)+1; width++ {
			fitted := Inline(c.Message, InlineOptions{MaxWidth: width})
			got := plain.RenderCell(c.Message, width)
			switch w := displayWidth(got); {
			case displayWidth(fitted) <= width:
				if got != fitted {
					t.Fatalf("%s width %d: cell %q, want Inline's fit %q", c.Name, width, got, fitted)
				}
			case w > width || w < width-1 || !strings.HasSuffix(got, ellipsis):
				// One cell short is a wide character at the cut, which is
				// never split.
				t.Fatalf("%s width %d: clipped cell %q is %d cells, want %d (or %d) ending in %s", c.Name, width, got, w, width, width-1, ellipsis)
			default:
				clipped++
			}
			color := styled.RenderCell(c.Message, width)
			if StripANSI(color) != got {
				t.Fatalf("%s width %d: styled cell strips to %q, plain cell %q", c.Name, width, StripANSI(color), got)
			}
			opens := strings.Count(color, sgrBold) + strings.Count(color, sgrBoldRed)
			if opens != strings.Count(color, sgrBoldOff)+strings.Count(color, sgrBoldRedOff) {
				t.Fatalf("%s width %d: a style is left open in %q", c.Name, width, color)
			}
			// The clip cuts text, never an escape sequence: the cell carries
			// every one the fitted form had, the ones past the cut included.
			styledFit := Inline(c.Message, InlineOptions{Color: true, MaxWidth: width})
			if want := strings.Count(styledFit, "\x1b"); strings.Count(color, "\x1b") != want {
				t.Fatalf("%s width %d: styled cell %q lost escape sequences of %q", c.Name, width, color, styledFit)
			}
			if got != fitted && opens > 0 {
				clippedStyled++
			}
		}
		// No limit means no clip: the cell is Inline's unfitted form.
		if got := plain.RenderCell(c.Message, 0); got != whole {
			t.Fatalf("%s: RenderCell(width 0) = %q, want the whole message %q", c.Name, got, whole)
		}
	}
	if clipped == 0 {
		t.Fatal("no canonical message was ever clipped: the clip branch checked nothing")
	}
	if clippedStyled == 0 {
		t.Fatal("no clipped canonical message carried a style: the escape-sequence check checked nothing")
	}
}

// A cluster that starts with an ASCII byte -- a keycap, a letter with an emoji
// presentation selector -- is two cells wide. ansi.Truncate counts its first
// byte as one cell and the rest as none, so a clip through it came back wider
// than the cap the fit had measured against; the cell is now clipped with the
// same measure that fitted it. ansi.StringWidth, which agrees on these
// clusters, is the independent check.
func TestRenderCellClipsClustersThatStartWithASCII(t *testing.T) {
	plain, err := New(Options{Writer: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	styled, err := New(Options{ForceColor: true})
	if err != nil {
		t.Fatal(err)
	}
	keycaps := strings.Repeat("1\uFE0F\u20E3", 40)
	for _, msg := range []string{
		"Values **\u201C" + keycaps + "\u201D** are invalid",
		"Row " + strings.Repeat("1\uFE0F\u20E3", 10) + " has value **\u201C~*" + strings.Repeat("v", 100) + "*~\u201D**",
		"Values " + strings.Repeat("a\uFE0F", 40) + " are invalid",
	} {
		for _, r := range []*Renderer{plain, styled} {
			got := r.RenderCell(msg, 40)
			if w := ansi.StringWidth(got); w > 40 || w < 39 || !strings.HasSuffix(StripANSI(got), ellipsis) {
				t.Errorf("RenderCell(%q, 40) = %q, %d cells, want 39 or 40 ending in %s", msg, got, w, ellipsis)
			}
		}
	}
}

// A lone ESC that starts an unterminated OSC is made visible before fitting,
// so it cannot swallow the text that follows or escape the column cap.
func TestRenderCellCountsTextAfterAnUnterminatedEscape(t *testing.T) {
	plain, err := New(Options{Writer: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	msg := "Upload failed for \x1b]" + strings.Repeat("x", 200)
	got := plain.RenderCell(msg, 20)
	if n := utf8.RuneCountInString(got); n > 20 || !strings.HasSuffix(got, ellipsis) || strings.ContainsRune(got, '\x1b') {
		t.Errorf("RenderCell(%q, 20) = %q, %d runes, want safe text within 20 cells ending in %s", msg, got, n, ellipsis)
	}
	// A stray 0xff byte is one cell, and the walk over invalid UTF-8 still ends.
	if got := plain.RenderCell(strings.Repeat("\xff", 50), 10); displayWidth(got) != 10 {
		t.Errorf("RenderCell of invalid UTF-8 = %q, %d cells, want 10", got, displayWidth(got))
	}
}

func TestRenderCellCapsRunsOfZeroWidthControlsAfterSanitizing(t *testing.T) {
	plain, err := New(Options{Writer: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	got := plain.RenderCell(strings.Repeat("\x00", 200), 16)
	if strings.ContainsRune(got, '\x00') || displayWidth(got) > 16 || !strings.HasSuffix(got, ellipsis) {
		t.Errorf("RenderCell of NUL run = %q, width %d; want safe output capped at 16", got, displayWidth(got))
	}
}

// displayWidth is the one measure behind the fit, shortenValue and the clip.
func TestDisplayWidth(t *testing.T) {
	for s, want := range map[string]int{
		"abc":                3,
		"1\uFE0F\u20E3":      2,
		"a\uFE0F":            2,
		"\x1b[1mab\x1b[22m":  2,
		"\x1b]abc":           4,
		"\x1b]0;title\x07ab": 2,
		"\x1b[?25lab":        7,
		"\u6771":             2,
	} {
		if got := displayWidth(s); got != want {
			t.Errorf("displayWidth(%q) = %d, want %d", s, got, want)
		}
	}
}
