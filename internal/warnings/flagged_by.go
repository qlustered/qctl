package warnings

import (
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/user_submitted"
)

// RenderFlaggedByPlain renders w's user_submitted_issues as the FLAGGED BY
// footer section, as plain lines. Returns "" when w is nil or has none.
func RenderFlaggedByPlain(w *WarningFull) string {
	if w == nil || w.UserSubmittedIssues == nil {
		return ""
	}
	return user_submitted.RenderFlaggedByPlain(*w.UserSubmittedIssues, w.DatasetID)
}

// RenderFlaggedBy is RenderFlaggedByPlain as a YAML comment block.
func RenderFlaggedBy(w *WarningFull) string {
	return output.YAMLComment(RenderFlaggedByPlain(w))
}
