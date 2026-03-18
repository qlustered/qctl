package markdown

import "regexp"

// ansiPattern matches ANSI escape sequences
// This includes CSI sequences (ESC[) and OSC sequences (ESC])
var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-9;]*[a-zA-Z]|\][^\x07]*\x07)`)

// StripANSI removes all ANSI escape sequences from the given string.
// This is used to convert ANSI-styled output to plain text for JSON/YAML output.
func StripANSI(text string) string {
	return ansiPattern.ReplaceAllString(text, "")
}

// HasANSI checks if the text contains any ANSI escape sequences
func HasANSI(text string) bool {
	return ansiPattern.MatchString(text)
}
