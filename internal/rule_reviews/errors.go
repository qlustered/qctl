// Package rule_reviews is the domain client for the rule review workflow
// (review requests, lifecycle, reviewers, audit log).
package rule_reviews //nolint:revive // Established package path; renaming would break imports.

import "github.com/qlustered/qctl/internal/comments"

// The rule arc's comment handling lives in internal/comments — the backend put
// rule-review, data-review and cell comments on one model, so qctl has one
// implementation. These aliases keep that an implementation detail of this
// package: callers under internal/commands/review/ do not need to know.
type (
	APIError  = comments.APIError
	Directory = comments.Directory
)

var (
	MentionLink      = comments.MentionLink
	DeRenderMentions = comments.DeRenderMentions
	UserDisplay      = comments.UserDisplay
)

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}
