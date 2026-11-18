package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/terminal"
)

// FlaggerName returns u's "First Last (role)", with u's email in place of the
// name when both name parts are blank and without " (role)" when u has no
// role. The result is sanitized for a terminal.
func FlaggerName(u api.UserInfoTinySchema) string {
	return terminal.Sanitize(flaggerBaseName(u) + comments.RoleSuffix(u.Role))
}

// FlaggerLabel returns "First Last (role) <email>", or FlaggerName alone when
// u has no email or its name is its email. The result is sanitized for a
// terminal.
func FlaggerLabel(u api.UserInfoTinySchema) string {
	name := FlaggerName(u)
	if u.Email == "" || terminal.Sanitize(flaggerBaseName(u)) == terminal.Sanitize(u.Email) {
		return name
	}
	return name + " <" + terminal.Sanitize(u.Email) + ">"
}

// flaggerBaseName returns u's trimmed "First Last", or u's email when both
// name parts are blank. The result is not sanitized.
func flaggerBaseName(u api.UserInfoTinySchema) string {
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	if name == "" {
		return u.Email
	}
	return name
}
