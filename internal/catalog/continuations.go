package catalog

import "strings"

// A LogicalLine is one joined line of a body plus the indentation of the
// physical line it started on.
//
// Indent counts leading whitespace CHARACTERS, not display columns — a tab
// counts as one. The only test anyone makes of it in this repo is against
// zero ("did this line start at column 0?"), which is the same answer either
// way; a caller that wants to compare two nonzero indents in a body that
// mixes tabs and spaces would have to expand them first.
//
// A blank line reports Indent 0 even when the physical line held whitespace.
// A whitespace-only line has no indentation to speak of, and reporting the
// 2 in a "  " line inside an indented block would make it read as content.
// Text is "" for such a line, so a caller that tests Text first never sees
// this; a caller that switches on Indent first must not be misled by it.
type LogicalLine struct {
	Indent int
	Text   string
}

// LogicalLines splits body into lines and joins each line that ends in a
// trailing backslash with the line that follows it, so a shell invocation
// wrapped across several physical lines is returned as the single command it
// is. Every returned line is already trimmed of surrounding whitespace.
//
// It exists because two separate walkers in this repo scan a body for lines
// beginning with "qctl ", and neither could see past the first physical line
// of a wrapped example. Both failed silently, in opposite directions: the
// guide resolver (internal/commands/guide/resolve_test.go) never handed the
// continuation to ResolveCommandLine, so the flags on it went unchecked, while
// commandExamples read it as the prose that ends an examples block and
// truncated the list there. Feeding both from here is what keeps a trailing
// backslash from meaning two different things in two places.
//
// Callers must apply their own predicates — the "qctl " prefix test, skips for
// backticks, "$(" and shell operator tokens — to the JOINED line. A pipeline
// or a command substitution that lives on the continuation is part of the same
// invocation as its first line, and skipping only the physical line it appears
// on would let the rest through.
//
// A trailing backslash on the last line of body has nothing to continue into.
// What accumulated is emitted anyway: dropping it would silently lose a whole
// invocation, which is the failure mode this function exists to end.
//
// Known limitation, zero instances today: a trailing backslash is also
// markdown's hard line break, so prose ending in one directly above a "qctl "
// line would swallow that line into the prose and the command would stop being
// checked. No guide topic or page uses the hard-break form. The precise guards
// for it all need this function to know what its caller is looking for, which
// is worse than the risk; a `grep -n '\\$'` over the bodies is what finds it.
//
// A caller that needs to know which column a line started in — commandExamples
// ends an examples block at a column-0 line — wants LogicalLinesIndented; the
// trimming here is what hides that.
func LogicalLines(body string) []string {
	lines := LogicalLinesIndented(body)
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Text)
	}
	return out
}

// LogicalLinesIndented is LogicalLines with each line's indentation kept. The
// joining is identical and lives only here; LogicalLines is a wrapper that
// drops the indent.
//
// A joined line reports the indent of its FIRST physical line — the column the
// invocation begins in. Continuation lines are conventionally indented further
// than the line they continue, and that deeper indent belongs to the wrap, not
// to the invocation.
func LogicalLinesIndented(body string) []LogicalLine {
	var out []LogicalLine
	var pending []string
	indent := 0
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if len(pending) == 0 {
			indent = indentOf(raw)
		}
		if cont, ok := strings.CutSuffix(line, `\`); ok {
			pending = append(pending, strings.TrimSpace(cont))
			continue
		}
		if len(pending) > 0 {
			line = strings.TrimSpace(strings.Join(append(pending, line), " "))
			pending = nil
		}
		out = append(out, LogicalLine{Indent: indent, Text: line})
	}
	if len(pending) > 0 {
		out = append(out, LogicalLine{Indent: indent, Text: strings.TrimSpace(strings.Join(pending, " "))})
	}
	return out
}

// indentOf counts the leading whitespace of raw, and reports 0 for a line with
// no content — see LogicalLine.Indent.
func indentOf(raw string) int {
	trimmed := strings.TrimLeft(raw, " \t")
	if trimmed == "" {
		return 0
	}
	return len(raw) - len(trimmed)
}
