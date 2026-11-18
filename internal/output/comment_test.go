package output

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLCommentPrefixesEveryContentLine(t *testing.T) {
	// The shape both NEXT ACTIONS footers actually have: a leading blank line,
	// a heading, indented command lines, and a trailing newline.
	in := "\nNEXT ACTIONS\n  qctl alert resolve 1 --action try_again\n    not idempotent\n"
	want := "\n# NEXT ACTIONS\n#   qctl alert resolve 1 --action try_again\n#     not idempotent\n"
	if got := YAMLComment(in); got != want {
		t.Errorf("YAMLComment(%q)\n  = %q\n want %q", in, got, want)
	}
}

// "" means "there is no footer", and that has to survive the round trip --
// a lone "#" would print a stray comment on every alert that offers nothing.
func TestYAMLCommentLeavesAnEmptyBlockEmpty(t *testing.T) {
	if got := YAMLComment(""); got != "" {
		t.Errorf("YAMLComment(\"\") = %q, want \"\"", got)
	}
}

// Indentation is what the footer uses to subordinate a note to the command it
// annotates, so "# " must be a prefix and never a replacement for the indent.
func TestYAMLCommentKeepsIndentation(t *testing.T) {
	if got := YAMLComment("      qctl alert resolve -f <file>\n"); got != "#       qctl alert resolve -f <file>\n" {
		t.Errorf("indentation not preserved: %q", got)
	}
}

// No line may end in whitespace: a "# " written over an empty line is exactly
// the trailing space a reviewer or a linter flags next.
func TestYAMLCommentWritesNoTrailingWhitespace(t *testing.T) {
	got := YAMLComment("\nNEXT ACTIONS\n\n  qctl warning resolve 55 --action not_anomaly\n")
	for _, line := range strings.Split(got, "\n") {
		if line != "" && line[len(line)-1] == ' ' {
			t.Errorf("line %q ends in whitespace, in:\n%s", line, got)
		}
	}
	if want := "\n# NEXT ACTIONS\n\n#   qctl warning resolve 55 --action not_anomaly\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestYAMLCommentSanitizesControlsThatBypassTheYAMLEncoder(t *testing.T) {
	got := YAMLComment("\nserver value: \x1b]0;title\x07\x00\x7f\u0085\n")
	want := "\n# server value: \\x1b]0;title\\x07\\x00\\x7f\\x85\n"
	if got != want || strings.ContainsRune(got, '\x1b') {
		t.Errorf("YAMLComment = %q, want %q", got, want)
	}
}

func TestYAMLCommentEscapesTheLineSeparatorsYAMLBreaksOn(t *testing.T) {
	got := YAMLComment("\nEve\xe2\x80\xa8injected: true\xe2\x80\xa9#\n")
	want := "\n# Eve\\u2028injected: true\\u2029#\n"
	if got != want {
		t.Fatalf("YAMLComment = %q, want %q", got, want)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte("a: 1\n"+got), &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if len(doc) != 1 {
		t.Errorf("document keys = %v, want only a", doc)
	}
}
