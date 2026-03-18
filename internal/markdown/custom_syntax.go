package markdown

import (
	"regexp"
	"strings"
)

// Placeholders for custom syntax - chosen to be unlikely in normal text
const (
	placeholderStart = "<<<QCTL_BOLDRED_START>>>"
	placeholderEnd   = "<<<QCTL_BOLDRED_END>>>"
)

// ANSI escape codes for bold red text
const (
	boldRedStart = "\x1b[1;31m"
	boldRedEnd   = "\x1b[0m"
)

// customSyntaxPattern matches ~|value|~ where value is any non-pipe characters
var customSyntaxPattern = regexp.MustCompile(`~\|([^|]+)\|~`)

// PreprocessCustomSyntax converts ~|value|~ to placeholders before markdown rendering.
// This prevents the custom syntax from being interpreted as markdown.
func PreprocessCustomSyntax(text string) string {
	return customSyntaxPattern.ReplaceAllString(text, placeholderStart+"$1"+placeholderEnd)
}

// PostprocessCustomSyntax converts placeholders to ANSI codes (for TTY) or removes them (for plain text).
func PostprocessCustomSyntax(text string, isTTY bool) string {
	if isTTY {
		// Replace placeholders with ANSI bold red codes
		text = strings.ReplaceAll(text, placeholderStart, boldRedStart)
		text = strings.ReplaceAll(text, placeholderEnd, boldRedEnd)
	} else {
		// Remove placeholders for plain text output
		text = strings.ReplaceAll(text, placeholderStart, "")
		text = strings.ReplaceAll(text, placeholderEnd, "")
	}
	return text
}

// HasCustomSyntax checks if the text contains any ~|value|~ patterns
func HasCustomSyntax(text string) bool {
	return customSyntaxPattern.MatchString(text)
}
