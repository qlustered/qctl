package manifest

import (
	"strconv"
	"strings"
)

// CommentSafe renders s so that it cannot escape the single YAML comment line
// it is written on.
//
// The values this exists for are customer cell data — an alert's field_value,
// a table name — and a cell can hold anything a quoted CSV field can, newlines
// included. Interpolated raw into "# field_value: %s" a newline ends the
// comment and everything after it is parsed as YAML, which had two effects,
// both reproduced: an address or a free-text note made a generated skeleton
// fail to parse at all, and a value carrying "\n---\nkind: AlertResolution..."
// made it parse as TWO documents, the injected one naming an alert the
// operator never did.
//
// A value that is already one printable line is returned unchanged, so an
// ordinary skeleton reads exactly as it did before. Anything carrying a line
// break of any flavour, or any other control character, is returned as a Go
// quoted string: one physical line, no byte dropped, and the escapes (\n, \r,
// \x00) are legible.
//
// Quoting rather than prefixing every continuation line with "# " is
// deliberate. Both stop the escape, but a commented continuation reads as
// ANOTHER fact in the block — which is precisely the confusion an injected
// "kind: AlertResolution" trades on — while a quoted value keeps its own
// boundaries visible and stays one grep-able line.
//
// It guards line breaks and control characters only. Everything else a cell
// can hold — "---", "#", ": " — is inert inside a comment, so quoting it would
// cost legibility and buy nothing. That also means CommentSafe is NOT a YAML
// scalar quoter: do not reach for it to emit a VALUE.
func CommentSafe(s string) string {
	if !strings.ContainsFunc(s, breaksACommentLine) {
		return s
	}
	return strconv.Quote(s)
}

// breaksACommentLine reports whether r would end the comment line it sits on,
// or is otherwise unfit to print raw. The five named runes are the line breaks
// YAML recognises — LF, CR, NEL, LINE SEPARATOR and PARAGRAPH SEPARATOR — and
// the range check covers the remaining C0 controls plus DEL.
func breaksACommentLine(r rune) bool {
	switch r {
	case '\n', '\r', '\u0085', '\u2028', '\u2029':
		return true
	}
	return r < 0x20 || r == 0x7f
}
