package cmdutil

import "strings"

// ShellArg makes a value safe to paste into a POSIX shell command line.
// Values made only of common word and path characters stay bare; everything
// else is single-quoted.
func ShellArg(s string) string {
	if s != "" && !strings.ContainsFunc(s, needsShellQuote) {
		return s
	}
	return ShellQuote(s)
}

// ShellQuote is ShellArg's always-quoting twin for prose that must preserve
// visible value boundaries. Both functions share the same quote splice so
// their escaping cannot drift.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// needsShellQuote is an allowlist: unnecessary quoting is harmless, while an
// omitted shell metacharacter can change a pasted command's meaning.
func needsShellQuote(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case r == '_', r == '-', r == '.', r == '/', r == '@', r == ':':
		return false
	default:
		return true
	}
}
