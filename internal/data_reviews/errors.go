// Package data_reviews is the domain client for the data-row review workflow:
// submission groups, per-row reviews, per-cell decisions on the change ledger,
// reviewer management and review comments. It wraps all eighteen backend
// routes exactly once — seventeen in capi/capi/views/data_review_view.py plus
// count-by-state in data_view.py.
package data_reviews //nolint:revive // Mirrors the backend's data-review arc, like rule_reviews.

import (
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// The backend put rule-review, data-review and cell-thread comments on one
// model, so qctl has one implementation of the error shaping and the user
// directory that hang off it: internal/comments. These aliases keep that an
// implementation detail of this package, exactly as internal/rule_reviews,
// internal/comment_threads and internal/warnings do. internal/alerts and
// internal/rule_suggestions still carry verbatim copies — those are the
// anti-pattern, and this package is deliberately not a third one.
type (
	APIError  = comments.APIError
	Directory = comments.Directory
)

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}

func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}
