package alerts

import (
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/user_submitted"
)

// RenderFlaggedByPlain renders a's user_submitted_issues as the FLAGGED BY
// footer section, as plain lines. Returns "" when a is nil or has none.
func RenderFlaggedByPlain(a *AlertFull) string {
	if a == nil || a.UserSubmittedIssues == nil {
		return ""
	}
	return user_submitted.RenderFlaggedByPlain(*a.UserSubmittedIssues, a.DatasetID)
}

// RenderFlaggedBy is RenderFlaggedByPlain as a YAML comment block.
func RenderFlaggedBy(a *AlertFull) string {
	return output.YAMLComment(RenderFlaggedByPlain(a))
}
