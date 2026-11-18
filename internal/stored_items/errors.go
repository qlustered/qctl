package stored_items

import (
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError carries the backend's status, code and context.
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
