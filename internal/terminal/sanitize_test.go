package terminal

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeEscapesEveryTerminalControl(t *testing.T) {
	var input strings.Builder
	for r := rune(0); r <= 0x9f; r++ {
		input.WriteRune(r)
	}

	got := Sanitize(input.String())
	for _, r := range got {
		if unsafeControl(r) {
			t.Fatalf("Sanitize left control U+%04X in %q", r, got)
		}
	}
	if !strings.Contains(got, `\x1b`) || !strings.Contains(got, `\x7f`) || !strings.Contains(got, `\x9f`) {
		t.Fatalf("Sanitize did not expose representative controls: %q", got)
	}
	if !strings.Contains(got, "\t\n") {
		t.Fatalf("Sanitize did not retain TAB and LF: %q", got)
	}
}

func TestSanitizeEscapesEveryBidiControl(t *testing.T) {
	tests := []struct {
		r    rune
		want string
	}{
		{0x061c, `\u061c`},
		{0x200e, `\u200e`},
		{0x200f, `\u200f`},
		{0x202a, `\u202a`},
		{0x202b, `\u202b`},
		{0x202c, `\u202c`},
		{0x202d, `\u202d`},
		{0x202e, `\u202e`},
		{0x2066, `\u2066`},
		{0x2067, `\u2067`},
		{0x2068, `\u2068`},
		{0x2069, `\u2069`},
	}

	for _, tt := range tests {
		input := "before" + string(tt.r) + "after"
		want := "before" + tt.want + "after"
		if got := Sanitize(input); got != want {
			t.Errorf("Sanitize with U+%04X = %q, want %q", tt.r, got, want)
		}
	}
}

func TestSanitizeLeavesJoinControlsUnchanged(t *testing.T) {
	input := "before" + string(rune(0x200c)) + string(rune(0x200d)) + "after"
	if got := Sanitize(input); got != input {
		t.Errorf("Sanitize join controls = %q, want %q", got, input)
	}
}

func TestSanitizeLeavesPrintableUnicodeUnchanged(t *testing.T) {
	const input = "plain 東 👩‍👩‍👧\tsecond column\nnext line"
	if got := Sanitize(input); got != input {
		t.Errorf("Sanitize(%q) = %q", input, got)
	}
}

func TestSanitizeReplacesInvalidUTF8(t *testing.T) {
	got := Sanitize("before\x9bafter\xff")
	if !utf8.ValidString(got) || got != "before�after�" {
		t.Errorf("Sanitize invalid UTF-8 = %q", got)
	}
}
