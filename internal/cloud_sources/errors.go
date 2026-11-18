package cloud_sources

import (
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError is re-exported so command files under internal/commands/rows/ never
// have to import internal/comments directly. internal/comments is qctl's one
// implementation of the backend's error envelope; internal/datasets,
// internal/warnings, internal/rule_reviews and internal/comment_threads alias
// it exactly this way.
type APIError = comments.APIError

// wrapAPIError rebuilds the error from the transport's captured body. The spec
// declares HTTPValidationError{Detail *[]ValidationError} but the backend sends
// detail:{msg,error_code,context}, so the generated *WithResponse call returns
// (nil, err) with resp.Body never reachable. LastCapturedError() is the
// sanctioned workaround (CLAUDE.md, 422 gotcha).
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}
