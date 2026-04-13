package apierror

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	SeverityError = "error"
)

// Response represents the backend error response
type Response struct {
	Detail json.RawMessage `json:"detail"`
}

// OperationalError is the parsed, user-friendly error structure
type OperationalError struct {
	Message     string
	ResponseMsg string // human-readable message from the API (preferred over Message for display)
	Title       string
	Severity    string // "error", "warning", "info"
	ErrorCode   string
	ErrorID     string
	RedirectURL string
}

// ValidationError represents a single validation error
type ValidationError struct {
	Loc  []interface{} `json:"loc"`
	Msg  string        `json:"msg"`
	Type string        `json:"type"`
}

// StructuredError represents Format 2 (structured error object)
type StructuredError struct {
	Msg         string `json:"msg"`
	ResponseMsg string `json:"response_msg,omitempty"`
	Title       string `json:"title,omitempty"`
	Severity    string `json:"severity,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	ErrorID     string `json:"error_id,omitempty"`
	RedirectURL string `json:"redirect_url,omitempty"`
}

// ParseErrorResponse parses the backend error response
func ParseErrorResponse(body []byte, statusCode int) (*OperationalError, error) {
	if len(body) == 0 {
		return &OperationalError{
			Message:  fmt.Sprintf("Request failed with status %d", statusCode),
			Severity: SeverityError,
		}, nil
	}

	var apiError Response
	if err := json.Unmarshal(body, &apiError); err != nil {
		// Not JSON, return as plain text
		return &OperationalError{
			Message:  string(body),
			Severity: SeverityError,
		}, nil
	}

	opError, parseErr := parseDetail(apiError.Detail)
	if parseErr != nil {
		// detail field was missing or unparseable — include status code
		return &OperationalError{
			Message:  fmt.Sprintf("server returned %d %s", statusCode, http.StatusText(statusCode)),
			Severity: SeverityError,
		}, nil
	}

	return opError, nil
}

func parseDetail(detail json.RawMessage) (*OperationalError, error) {
	if len(detail) == 0 {
		return nil, fmt.Errorf("no error detail in response")
	}

	// Try Format 1: String detail
	var strDetail string
	if err := json.Unmarshal(detail, &strDetail); err == nil {
		return &OperationalError{
			Message:  strDetail,
			Severity: SeverityError,
		}, nil
	}

	// Try Format 2: Structured error object
	var structuredErr StructuredError
	if err := json.Unmarshal(detail, &structuredErr); err == nil && (structuredErr.Msg != "" || structuredErr.ResponseMsg != "") {
		severity := structuredErr.Severity
		if severity == "" {
			severity = SeverityError
		}
		return &OperationalError{
			Message:     structuredErr.Msg,
			ResponseMsg: structuredErr.ResponseMsg,
			Title:       structuredErr.Title,
			Severity:    severity,
			ErrorCode:   structuredErr.ErrorCode,
			ErrorID:     structuredErr.ErrorID,
			RedirectURL: structuredErr.RedirectURL,
		}, nil
	}

	// Try Format 3 & 4: Array of errors
	var arrayErrors []json.RawMessage
	if err := json.Unmarshal(detail, &arrayErrors); err == nil && len(arrayErrors) > 0 {
		// Check if it's validation errors (has 'loc' field)
		var validationErrs []ValidationError
		if err := json.Unmarshal(detail, &validationErrs); err == nil && len(validationErrs) > 0 && validationErrs[0].Loc != nil {
			return parseValidationErrors(validationErrs), nil
		}

		// Format 4: Array of simple error objects
		var simpleErrs []StructuredError
		if err := json.Unmarshal(detail, &simpleErrs); err == nil && len(simpleErrs) > 0 {
			return &OperationalError{
				Message:  simpleErrs[0].Msg,
				Severity: SeverityError,
			}, nil
		}
	}

	// Fallback: return raw detail as string
	return &OperationalError{
		Message:  string(detail),
		Severity: SeverityError,
	}, nil
}

func parseValidationErrors(errors []ValidationError) *OperationalError {
	var messages []string
	for _, err := range errors {
		field := formatField(err.Loc)
		if field != "" {
			messages = append(messages, err.Msg+"\nField: "+field)
		} else {
			messages = append(messages, err.Msg)
		}
	}

	return &OperationalError{
		Message:  strings.Join(messages, "\n"),
		Severity: SeverityError,
	}
}

// formatField extracts the field path from loc, skipping "body" prefix
func formatField(loc []interface{}) string {
	if len(loc) == 0 {
		return ""
	}

	var parts []string
	for i, part := range loc {
		// Skip "body" if it's the first element
		if i == 0 {
			if s, ok := part.(string); ok && s == "body" {
				continue
			}
		}
		parts = append(parts, fmt.Sprint(part))
	}

	return strings.Join(parts, ".")
}

// Format returns a user-friendly error message
func (e *OperationalError) Format() string {
	var parts []string

	// Add title if present
	if e.Title != "" {
		parts = append(parts, e.Title)
	}

	// Prefer response_msg (human-readable) over msg for display
	displayMsg := e.Message
	if e.ResponseMsg != "" {
		displayMsg = e.ResponseMsg
	}

	// Convert HTML to plain text for terminal display
	message := htmlToPlainText(displayMsg)

	// Add main message
	parts = append(parts, message)

	// Add error code and ID on a separate line if present
	var meta []string
	if e.ErrorCode != "" {
		meta = append(meta, fmt.Sprintf("Code: %s", e.ErrorCode))
	}
	if e.ErrorID != "" && e.ErrorID != e.ErrorCode {
		meta = append(meta, fmt.Sprintf("ID: %s", e.ErrorID))
	}
	if len(meta) > 0 {
		parts = append(parts, fmt.Sprintf("(%s)", strings.Join(meta, ", ")))
	}

	// Add actionable hints for auth errors
	if strings.HasPrefix(e.ErrorCode, "AUTH_ERR") {
		parts = append(parts, "Run `qctl auth login` to authenticate.")
	}

	return strings.Join(parts, "\n")
}

// htmlToPlainText converts common HTML elements to plain text for terminal display
func htmlToPlainText(s string) string {
	// Replace common HTML line breaks
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</p><p>", "\n\n")
	s = strings.ReplaceAll(s, "<p>", "")
	s = strings.ReplaceAll(s, "</p>", "")
	return s
}
