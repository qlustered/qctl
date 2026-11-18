package apierror

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/qlustered/qctl/internal/prompt"
)

var tableAccessMessage = regexp.MustCompile(
	`^User is not authorized to access (table [1-9][0-9]*|tables \[[1-9][0-9]*(?:, [1-9][0-9]*)*\])$`,
)

// tableAccessNote returns grant advice for a recognized table-access denial.
func tableAccessNote(message string) string {
	s := recordedScope.Load()
	if s == nil || !tableAccessMessage.MatchString(message) {
		return ""
	}

	target := strings.TrimPrefix(message, "User is not authorized to access ")
	ids, owner := strings.TrimPrefix(target, "table "), "table's owner"
	if strings.HasPrefix(target, "tables ") {
		ids = strings.NewReplacer("[", "", "]", "", ",", "").Replace(strings.TrimPrefix(target, "tables "))
		owner = "tables' owners"
	}

	return fmt.Sprintf("Scoped to organization %s (%s).\n"+
		"This account has no access to %s. No other organization will help.\n"+
		"Run `qctl auth me` to see your role and email. Ask the %s or an\n"+
		"org admin to grant this account access:\n"+
		"  qctl share table %s --to <email from qctl auth me>%s\n"+
		"The grant level and your role both control allowed actions. Editing needs an editor or owner grant.\n"+
		"Then re-run this command.", s.display, s.source, target, owner, ids, prompt.YesFlag(false))
}

// piiViewDeniedMessage is atlas's denial for a caller who may not see a
// table's personal data unmasked (common/auth_types.py, the
// AppPermission.PII_VIEW entry). The table was found, so buildHTTPError
// attaches no organization-scope note to it.
const piiViewDeniedMessage = "You don't have access to see personal data unmasked. " +
	"Please contact your administrator to request access."

var tableViewOnlyMessage = regexp.MustCompile(
	`^You have view-only access to (table [1-9][0-9]*|tables \[[1-9][0-9]*(?:, [1-9][0-9]*)*\]). Ask a table owner for edit access\.$`,
)

// tableViewOnlyNote returns advice for a recognized read-only grant denial.
func tableViewOnlyNote(message string) string {
	if !tableViewOnlyMessage.MatchString(message) {
		return ""
	}
	return "Your table grant is read_only. Ask a table owner or an org admin to change it to editor or owner " +
		"with `qctl table-member set-permission`. Your organization role must also permit the action. " +
		"Run `qctl auth me` for your user ID, then re-run the failed command after the grant changes."
}
