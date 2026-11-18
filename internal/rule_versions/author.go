package rule_versions

import (
	"strings"

	"github.com/qlustered/qctl/internal/api"
)

// FormatAuthor returns a user as "First L.": the first name cut to 10
// characters, then the last name's initial. It returns "-" for a nil user or
// one with neither name, and "L." for a user with no first name.
func FormatAuthor(user *api.UserInfoTinyDictSchema) string {
	if user == nil {
		return "-"
	}
	first := []rune(strings.TrimSpace(user.FirstName))
	last := []rune(strings.TrimSpace(user.LastName))
	if len(first) == 0 && len(last) == 0 {
		return "-"
	}
	if len(first) > 10 {
		first = first[:10]
	}
	switch {
	case len(last) == 0:
		return string(first)
	case len(first) == 0:
		return string(last[0]) + "."
	default:
		return string(first) + " " + string(last[0]) + "."
	}
}
