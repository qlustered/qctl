// Package comments holds the handling for the backend's unified Comment
// model. The backend put rule-review, data-review and cell-thread comments on
// one type (CommentResponseSchema); this package is qctl's one implementation
// of rendering, mention resolution and error shaping for it.
package comments

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// APIError carries the backend's machine-readable metadata for a failed call.
// Every Client method returns an *APIError for non-2xx responses, so command
// files can errors.As it and switch on Code.
type APIError struct {
	StatusCode int
	Code       string                 // detail.error_code, else detail.context.code, else ""
	Context    map[string]interface{} // detail.context verbatim; nil when absent
	Err        error                  // the apierror error (display text + exit code)
}

func (e *APIError) Error() string { return e.Err.Error() }

// Unwrap keeps internal/errors exit-code resolution working.
func (e *APIError) Unwrap() error { return e.Err }

// ContextString returns Context[key] as a string ("" when absent or non-string).
func (e *APIError) ContextString(key string) string {
	if e.Context == nil {
		return ""
	}
	s, _ := e.Context[key].(string)
	return s
}

// IsMissingParent reports whether the server rejected the named reply parent.
func IsMissingParent(err error, parentID string) bool {
	id, parseErr := uuid.Parse(parentID)
	var opErr *apierror.OperationalError
	return parseErr == nil && errors.As(err, &opErr) &&
		opErr.StatusCode == http.StatusUnprocessableEntity &&
		opErr.Message == "Comment "+id.String()+" not found"
}

// wireDetail is the machine-readable subset of the backend error body:
// {"detail": {"msg": ..., "error_code": ...?, "context": {...}?}}.
type wireDetail struct {
	ErrorCode string                 `json:"error_code"`
	Context   map[string]interface{} `json:"context"`
}

// HandleError builds the *APIError for a non-2xx response. The display error
// (and exit code) still comes from apierror; this only adds Code/Context.
func HandleError(statusCode int, body []byte, contextMsg string) error {
	apiErr := &APIError{
		StatusCode: statusCode,
		Err:        apierror.HandleHTTPErrorFromBytes(statusCode, body, contextMsg),
	}
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	// detail may be an object, a list (FastAPI validation), or absent —
	// only the object form carries error_code/context.
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Detail) > 0 {
		var detail wireDetail
		if err := jsonnum.Unmarshal(envelope.Detail, &detail); err == nil {
			apiErr.Code = detail.ErrorCode
			apiErr.Context = detail.Context
			if apiErr.Code == "" {
				if code, ok := detail.Context["code"].(string); ok {
					apiErr.Code = code
				}
			}
		}
	}
	return apiErr
}

// WrapAPIError converts an error returned by a generated *WithResponse call
// into an *APIError when the transport captured a structured error body. This
// covers the 422 case: the generated parser rejects the backend's
// object-shaped detail (its HTTPValidationError expects a list), returning a
// parse error before the caller can inspect resp.Body — so we rebuild the
// error from the captured bytes to preserve error_code/context. Falls back to
// the client's generic handling for transport-level failures with no HTTP body.
func WrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	if code, body, ok := apiClient.LastCapturedError(); ok {
		return HandleError(code, body, contextMsg)
	}
	return apiClient.HandleError(err, contextMsg)
}
