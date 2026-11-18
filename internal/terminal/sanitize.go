// Package terminal contains helpers for rendering untrusted text safely in a
// terminal. It is deliberately separate from structured encoders: JSON and
// YAML must preserve the server's value and escape it according to their own
// formats.
package terminal

import (
	"strings"
	"unicode/utf8"
)

const hex = "0123456789abcdef"

// Sanitize replaces terminal-active C0/C1 control characters and Unicode
// bidirectional ordering controls with visible ASCII escapes. LF and TAB are
// retained because callers use them as document structure; table-cell
// renderers are responsible for their existing one-line policy. Invalid UTF-8
// is replaced with the Unicode replacement character.
//
// The result is safe to hand to a renderer that may add its own ANSI styling:
// sanitization happens before those trusted sequences exist.
func Sanitize(text string) string {
	if utf8.ValidString(text) && strings.IndexFunc(text, needsEscape) < 0 {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case unsafeControl(r):
			b.WriteString(`\x`)
			b.WriteByte(hex[byte(r)>>4])
			b.WriteByte(hex[byte(r)&0x0f])
		case bidiControl(r):
			b.WriteString(`\u`)
			b.WriteByte(hex[(r>>12)&0x0f])
			b.WriteByte(hex[(r>>8)&0x0f])
			b.WriteByte(hex[(r>>4)&0x0f])
			b.WriteByte(hex[r&0x0f])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsEscape(r rune) bool {
	return unsafeControl(r) || bidiControl(r)
}

func unsafeControl(r rune) bool {
	return (r >= 0 && r < ' ' && r != '\t' && r != '\n') ||
		r >= 0x7f && r <= 0x9f
}

func bidiControl(r rune) bool {
	return r == 0x061c ||
		(r >= 0x200e && r <= 0x200f) ||
		(r >= 0x202a && r <= 0x202e) ||
		(r >= 0x2066 && r <= 0x2069)
}
