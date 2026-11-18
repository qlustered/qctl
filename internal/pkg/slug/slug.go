// Package slug derives slugs the way atlas does.
package slug

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Generate returns the slug atlas common.slug.generate_slug derives from name,
// cut to its first maxLen characters when maxLen > 0.
func Generate(name string, maxLen int) string {
	var ascii strings.Builder
	for _, r := range norm.NFKD.String(name) {
		if r < 128 {
			ascii.WriteRune(r)
		}
	}
	s := strings.TrimSpace(ascii.String())
	var out strings.Builder
	space := false
	for _, r := range s {
		switch {
		case slugRune(r):
			if space {
				out.WriteByte('-')
				space = false
			}
			out.WriteRune(r)
		case asciiSpace(r):
			space = true
		}
	}
	s = strings.Trim(strings.ToLower(out.String()), "-")
	if s != "" && (s[0] < 'a' || s[0] > 'z') {
		s = "d-" + s
	}
	if maxLen > 0 && len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

func slugRune(r rune) bool {
	return r == '_' || r == '-' || r == '+' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func asciiSpace(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}
