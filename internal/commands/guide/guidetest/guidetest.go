// Package guidetest checks a guide topic's quotes of command output against the
// code that renders it.
package guidetest

import (
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/commands/guide"
)

// AssertQuoted fails t unless the lines of rendered appear as consecutive whole
// lines of one code block — indented or fenced — of the guide topic, bounded on
// each side by the block's edge, a blank line, or a "..." line. Only the
// block's indent, trailing whitespace, and blank lines at either end of
// rendered are ignored. topic must be a topic name, not an alias.
func AssertQuoted(t testing.TB, topic, rendered string) {
	t.Helper()
	if !slices.Contains(guide.TopicNames(), topic) {
		t.Fatalf("%q is not a guide topic name", topic)
	}
	body, err := guide.TopicBody(topic)
	if err != nil {
		t.Fatalf("read guide %s: %v", topic, err)
	}
	want := renderedLines(rendered)
	if len(want) == 0 {
		t.Fatal("rendered output has no non-blank line: the pin's input no longer exercises the renderer")
	}
	blocks := codeBlocks(body)
	if quotedIn(blocks, want) {
		return
	}
	nearest := "(no code block shares a line with it)"
	if b := nearestBlock(blocks, want); b != nil {
		nearest = strings.Join(b, "\n")
	}
	t.Errorf("internal/commands/guide/topics/%s.md does not quote the rendered output as a whole passage of one code block\n"+
		"--- rendered by the code:\n%s\n--- nearest code block in the topic:\n%s\n"+
		"Update the topic's quote to match the code, or the pin's input if the topic changed deliberately.",
		topic, strings.Join(want, "\n"), nearest)
}

// trimTrailing drops trailing spaces and tabs.
func trimTrailing(line string) string {
	return strings.TrimRight(line, " \t")
}

// renderedLines splits rendered output into lines, without the blank lines at
// either end.
func renderedLines(s string) []string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = trimTrailing(lines[i])
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// codeBlocks returns a topic's code blocks, blank lines at either end dropped.
// An indented block is a maximal run of lines indented four spaces or blank,
// with the four spaces removed. A fenced block runs from an opening fence of
// three or more ` or ~ to a closing fence of the same character at least as
// long, or to the end of the body; each line loses up to as many leading
// spaces as the opening fence is indented.
func codeBlocks(body string) [][]string {
	var blocks [][]string
	var cur []string
	flush := func() {
		for len(cur) > 0 && cur[0] == "" {
			cur = cur[1:]
		}
		for len(cur) > 0 && cur[len(cur)-1] == "" {
			cur = cur[:len(cur)-1]
		}
		if len(cur) > 0 {
			blocks = append(blocks, cur)
		}
		cur = nil
	}
	fence, fenceIndent := "", 0
	for _, line := range strings.Split(body, "\n") {
		line = trimTrailing(line)
		if fence != "" {
			if closesFence(line, fence) {
				flush()
				fence = ""
			} else {
				cur = append(cur, stripSpaces(line, fenceIndent))
			}
			continue
		}
		if f, indent, ok := openingFence(line); ok {
			flush()
			fence, fenceIndent = f, indent
			continue
		}
		switch {
		case line == "":
			if len(cur) > 0 {
				cur = append(cur, "")
			}
		case strings.HasPrefix(line, "    "):
			cur = append(cur, line[4:])
		default:
			flush()
		}
	}
	flush()
	return blocks
}

// leadingSpaces counts the spaces line starts with.
func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// openingFence reports whether line opens a fenced block, returning the fence
// and its indent.
func openingFence(line string) (fence string, indent int, ok bool) {
	indent = leadingSpaces(line)
	if indent > 3 {
		return "", 0, false
	}
	rest := line[indent:]
	if rest == "" || (rest[0] != '`' && rest[0] != '~') {
		return "", 0, false
	}
	n := len(rest) - len(strings.TrimLeft(rest, rest[:1]))
	if n < 3 || (rest[0] == '`' && strings.Contains(rest[n:], "`")) {
		return "", 0, false
	}
	return rest[:n], indent, true
}

// closesFence reports whether line, trailing whitespace already trimmed,
// closes a block opened by fence.
func closesFence(line, fence string) bool {
	indent := leadingSpaces(line)
	if indent > 3 {
		return false
	}
	rest := line[indent:]
	return len(rest) >= len(fence) && strings.Trim(rest, fence[:1]) == ""
}

// stripSpaces removes up to n leading spaces from line.
func stripSpaces(line string, n int) string {
	return line[min(n, leadingSpaces(line)):]
}

// quotedIn reports whether want appears as consecutive whole lines of one
// block, with a passage boundary on each side. A "..." line at either end of
// want is its own boundary.
func quotedIn(blocks [][]string, want []string) bool {
	openStart, openEnd := isElision(want[0]), isElision(want[len(want)-1])
	for _, b := range blocks {
		for i := 0; i+len(want) <= len(b); i++ {
			if slices.Equal(b[i:i+len(want)], want) &&
				(openStart || atBoundary(b, i-1)) &&
				(openEnd || atBoundary(b, i+len(want))) {
				return true
			}
		}
	}
	return false
}

// atBoundary reports whether position i of block b is past either end of it,
// a blank line, or a "..." line.
func atBoundary(b []string, i int) bool {
	return i < 0 || i >= len(b) || strings.TrimSpace(b[i]) == "" || isElision(b[i])
}

// isElision reports whether line is a "..." line marking a cut.
func isElision(line string) bool {
	return strings.TrimSpace(line) == "..."
}

// nearestBlock returns the block sharing the most lines with want, or nil when
// none shares any.
func nearestBlock(blocks [][]string, want []string) []string {
	var best []string
	bestShared := 0
	for _, b := range blocks {
		shared := 0
		for _, line := range b {
			if slices.Contains(want, line) {
				shared++
			}
		}
		if shared > bestShared {
			best, bestShared = b, shared
		}
	}
	return best
}
