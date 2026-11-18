//nolint:revive // Established package-path style; internal/rule_families and internal/rule_versions do the same.
package rule_upgrades

import (
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError is re-exported so command files under internal/commands/rule_upgrade/
// never import internal/comments directly. internal/comments is qctl's one
// implementation of the backend's error envelope; internal/warnings,
// internal/rule_reviews and internal/comment_threads alias it exactly this way.
// CLAUDE.md forbids a fourth verbatim copy.
type APIError = comments.APIError

// wrapAPIError rebuilds a typed error from the transport's captured body when a
// generated *WithResponse call failed before the body was inspectable — the
// object-shaped-422 gotcha in CLAUDE.md. It keeps the Unwrap chain, and with it
// the exit code, intact.
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}
