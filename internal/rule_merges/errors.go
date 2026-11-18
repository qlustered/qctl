package rule_merges //nolint:revive // Public package name matches the established resource domain.

import (
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError carries the backend's error code and context for a failed call.
type APIError = comments.APIError

// wrapAPIError converts an error returned by a generated *WithResponse call
// into an *APIError when the transport captured an error body.
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

// handleError builds the *APIError for a non-2xx response.
func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}
