package apierror

import (
	"fmt"
	"io"
	"net/http"

	"github.com/qlustered/qctl/internal/errors"
)

// HandleHTTPError processes an HTTP error response and returns a user-friendly error
func HandleHTTPError(resp *http.Response, contextMsg string) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// If we can't read the body, still return a useful error with the status code
		return errors.FromHTTPStatus(resp.StatusCode, fmt.Sprintf("%s: failed to read response body", contextMsg))
	}
	return HandleHTTPErrorFromBytes(resp.StatusCode, body, contextMsg)
}

// HandleHTTPErrorFromBytes processes an HTTP error from status code and body bytes
// This is useful when the response body has already been read (e.g., by generated clients)
func HandleHTTPErrorFromBytes(statusCode int, body []byte, contextMsg string) error {
	// Parse the error
	opError, err := ParseErrorResponse(body, statusCode)
	if err != nil {
		// If parsing fails, return raw error
		return errors.FromHTTPStatus(
			statusCode,
			fmt.Sprintf("%s: %s", contextMsg, string(body)),
		)
	}

	// Format user-friendly message
	message := opError.Format()
	if contextMsg != "" {
		message = fmt.Sprintf("%s: %s", contextMsg, message)
	}

	// Map to appropriate exit code
	return errors.FromHTTPStatus(statusCode, message)
}
