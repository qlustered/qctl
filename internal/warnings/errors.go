package warnings

import (
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError is re-exported so command files under internal/commands/warning/
// never have to import internal/comments directly. internal/comments is qctl's
// one implementation of the backend's error envelope; internal/rule_reviews and
// internal/comment_threads alias it exactly this way.
type APIError = comments.APIError

// wrapAPIError converts an error returned by a generated *WithResponse call
// into an *APIError when the transport captured a structured error body.
//
// This is what the resolve path needs. ParseResolveWarningResponse decodes a
// 400, 401, 403, 404 or 409 into struct{Detail APIError}, so a body whose
// detail is not an object, such as a plain string, fails the parse and
// ResolveWarningWithResponse returns (nil, err) with resp.Body never
// reachable. Rebuilding from LastCapturedError() keeps the exit code an
// unbroken Unwrap chain instead of a bare 1.
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}
