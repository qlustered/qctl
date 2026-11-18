package value_mapping //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, rule_upgrades).

import (
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError is re-exported so command files never have to import
// internal/comments directly. internal/comments is qctl's one implementation
// of the backend's error envelope; internal/rule_reviews,
// internal/comment_threads and internal/warnings alias it exactly this way,
// and internal/alerts and internal/rule_suggestions still carry verbatim
// copies that this package deliberately does not become a third of.
type APIError = comments.APIError

// wrapAPIError rebuilds the error from the transport's captured body. The spec
// declares HTTPValidationError{Detail *[]ValidationError} but the backend
// sends detail:{msg,error_code,context}, so the generated *WithResponse call
// returns (nil, err) with resp.Body never reachable. LastCapturedError() is
// the sanctioned workaround (CLAUDE.md, 422 gotcha), and it is what keeps the
// exit code an unbroken Unwrap chain instead of a bare 1.
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}
