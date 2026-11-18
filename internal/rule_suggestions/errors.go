// Package rule_suggestions is the domain client for staged dataset-rule
// changes ("rule suggestions"): list, get, accept, dismiss, bulk
// accept/dismiss, and edit.
package rule_suggestions

import (
	"encoding/json"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// APIError carries the backend's machine-readable metadata for a failed
// call. Every Client method returns an *APIError for non-2xx responses, so
// command files can errors.As it and switch on Code.
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

// wireDetail is the machine-readable subset of the backend error body:
// {"detail": {"msg": ..., "error_code": ...?, "context": {...}?}}.
type wireDetail struct {
	ErrorCode string                 `json:"error_code"`
	Context   map[string]interface{} `json:"context"`
}

// handleError builds the *APIError for a non-2xx response. The display
// error (and exit code) still comes from apierror; this only adds
// Code/Context (422 object-shaped detail passthrough included).
func handleError(statusCode int, body []byte, contextMsg string) error {
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
