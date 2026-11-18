package history

import (
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError is re-exported so command files never import internal/comments
// directly. internal/comments is qctl's one implementation of the backend's
// error envelope; internal/warnings, internal/rule_reviews and
// internal/comment_threads alias it exactly this way.
type APIError = comments.APIError

// wrapAPIError converts an error returned by a generated *WithResponse call
// into an *APIError when the transport captured a structured error body.
//
// The LISTING path does not need this and deliberately does not use it:
// GetHistory goes through apiClient.HandleError, which already rebuilds from
// errorCapture. The four undo/redo routes are POSTs whose object-shaped 422
// the generated parser rejects outright — HTTPValidationError declares
// Detail *[]ValidationError and the backend sends detail:{msg,...} — so
// resp.Body is unreachable and LastCapturedError() is the only way to keep the
// exit code an unbroken Unwrap chain.
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}
