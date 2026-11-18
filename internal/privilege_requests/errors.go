package privilege_requests //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, value_mapping).

import (
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError is re-exported so command files never have to import
// internal/comments directly. internal/comments is qctl's one implementation
// of the backend's error envelope; this package aliases it the way
// internal/value_mapping and internal/rule_reviews do, rather than becoming a
// third verbatim copy beside internal/alerts and internal/rule_suggestions.
//
// The privilege view's own errors carry no error_code (atlas
// capi/capi/views/privilege_ticketing_view.py raises APIError(status, msg)
// and nothing more), so Code is "" for every refusal the workflow itself
// makes — two-person rule, already decided, expired alike. Callers branch on
// StatusCode, never on Code.
type APIError = comments.APIError

// wrapAPIError rebuilds the error from the transport's captured body when the
// generated parser rejected it: every route here declares a list-shaped 422,
// so an object-shaped one fails to parse before resp.Body is reachable.
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}
