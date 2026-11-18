package apierror

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
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
	StatusCode int
	// RecoveryHint overrides the default advice when non-nil.
	RecoveryHint *string
	Message      string
	Field        string
	ResponseMsg  string // human-readable message from the API (preferred over Message for display)
	Title        string
	Severity     string // "error", "warning", "info"
	ErrorCode    string
	ErrorID      string
	RedirectURL  string
	// ProgressPercent is set by the table-setup path
	// (atlas capi/capi/views/data_view_core.py). qctl's replacement copy
	// for TABLE_SETUP_IN_PROGRESS surfaces it -- it is the one genuinely
	// useful thing that message carried.
	ProgressPercent *int
	// Suggestions are the server's next-step hints, in the order it sent
	// them.
	Suggestions []string
	// Actions are the server's machine-readable next steps, in the order it
	// sent them.
	Actions []Action
	// Loc is the list an object-shaped detail carries as loc, which Field
	// renders; nil otherwise.
	Loc []interface{}
	// Context is an object-shaped detail's context, undecoded; nil when it
	// is absent or null.
	Context json.RawMessage
}

// ValidationError represents a single validation error
type ValidationError struct {
	Loc  []interface{} `json:"loc"`
	Msg  string        `json:"msg"`
	Type string        `json:"type"`
}

// StructuredError represents Format 2 (structured error object)
type StructuredError struct {
	Msg             string          `json:"msg"`
	Loc             json.RawMessage `json:"loc,omitempty"`
	ResponseMsg     string          `json:"response_msg,omitempty"`
	Title           string          `json:"title,omitempty"`
	Severity        string          `json:"severity,omitempty"`
	ErrorCode       string          `json:"error_code,omitempty"`
	ErrorID         string          `json:"error_id,omitempty"`
	RedirectURL     string          `json:"redirect_url,omitempty"`
	ProgressPercent *int            `json:"progress_percent,omitempty"`
	// Suggestions stays raw so that decodeSuggestions can ignore a shape it
	// does not expect instead of failing the whole object.
	Suggestions json.RawMessage `json:"suggestions,omitempty"`
	// Actions stays raw for the same reason as Suggestions.
	Actions json.RawMessage `json:"actions,omitempty"`
	// Context stays raw so that each code's hint decodes only what it reads.
	Context json.RawMessage `json:"context,omitempty"`
}

// ParseErrorResponse parses the backend error response
func ParseErrorResponse(body []byte, statusCode int) (*OperationalError, error) {
	opError := parseErrorResponse(body, statusCode)
	if opError != nil {
		opError.StatusCode = statusCode
	}
	return opError, nil
}

func parseErrorResponse(body []byte, statusCode int) *OperationalError {
	if len(body) == 0 {
		return &OperationalError{
			Message:  fmt.Sprintf("Request failed with status %d", statusCode),
			Severity: SeverityError,
		}
	}

	var apiError Response
	if err := json.Unmarshal(body, &apiError); err != nil {
		if statusCode == http.StatusBadGateway || statusCode == http.StatusServiceUnavailable {
			return &OperationalError{
				Message:  fmt.Sprintf("server returned %d %s", statusCode, http.StatusText(statusCode)),
				Severity: SeverityError,
			}
		}
		// Not JSON, return as plain text
		return &OperationalError{
			Message:  string(body),
			Severity: SeverityError,
		}
	}

	opError, parseErr := parseDetail(apiError.Detail)
	if parseErr != nil {
		// detail field was missing or unparseable — include status code
		return &OperationalError{
			Message:  fmt.Sprintf("server returned %d %s", statusCode, http.StatusText(statusCode)),
			Severity: SeverityError,
		}
	}

	return opError
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
		var loc []interface{}
		_ = jsonnum.Unmarshal(structuredErr.Loc, &loc)
		return &OperationalError{
			Message:         structuredErr.Msg,
			Field:           formatField(loc),
			Loc:             loc,
			ResponseMsg:     structuredErr.ResponseMsg,
			Title:           structuredErr.Title,
			Severity:        severity,
			ErrorCode:       structuredErr.ErrorCode,
			ErrorID:         structuredErr.ErrorID,
			RedirectURL:     structuredErr.RedirectURL,
			ProgressPercent: structuredErr.ProgressPercent,
			Suggestions:     decodeSuggestions(structuredErr.Suggestions),
			Actions:         decodeActions(structuredErr.Actions),
			Context:         nonNullRaw(structuredErr.Context),
		}, nil
	}

	// Try Format 3 & 4: Array of errors
	var arrayErrors []json.RawMessage
	if err := json.Unmarshal(detail, &arrayErrors); err == nil && len(arrayErrors) > 0 {
		// Check if it's validation errors (has 'loc' field)
		var validationErrs []ValidationError
		if err := jsonnum.Unmarshal(detail, &validationErrs); err == nil && len(validationErrs) > 0 && validationErrs[0].Loc != nil {
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

// nonNullRaw returns raw, or nil when it is empty or the JSON null.
func nonNullRaw(raw json.RawMessage) json.RawMessage {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	return raw
}

// decodeSuggestions returns raw as a list of strings, or nil when it is absent,
// null, or anything other than an array of strings.
func decodeSuggestions(raw json.RawMessage) []string {
	var suggestions []string
	if err := json.Unmarshal(raw, &suggestions); err != nil {
		return nil
	}
	return suggestions
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

// Error implements the error interface so OperationalError can be wrapped in error chains.
func (e *OperationalError) Error() string {
	return e.Format()
}

// Format returns a user-friendly error message.
//
// Passthrough (the default) renders the server's own message and suggestions:
// most backend copy is good terminal copy, and replacing it wholesale would be
// a downgrade. Replace suppresses copy written for the React frontend and
// substitutes qctl's, keeping the code and request ID either way and parking
// the raw message and suggestions behind -v. An EmptyState that carries Text
// (a route the command layer did not convert to an empty result) renders the
// same way Replace does, for the same reason. See internal/apierror/codes.go.
// Under every outcome, the code line is followed by a "Hint:" line when the
// code's Classification has a hint, and the message ends with the qctl hint
// for each action the server sent that has one (see actions.go).
func (e *OperationalError) Format() string {
	var parts []string

	if title := e.displayTitle(); title != "" {
		parts = append(parts, title)
	}

	parts = append(parts, e.bodyParts()...)
	if e.Field != "" {
		parts = append(parts, "Field: "+e.Field)
	}

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

	if hint := e.codeHint(); hint != "" {
		parts = append(parts, "Hint: "+hint)
	}

	// Add actionable hints for auth errors
	if hint := e.authHint(); hint != "" {
		parts = append(parts, hint)
	}
	parts = append(parts, e.actionHintLines()...)

	return strings.Join(parts, "\n")
}

// codeHint returns the Hint Classify records for the error's code, or "".
func (e *OperationalError) codeHint() string {
	if hint := Classify(e.ErrorCode).Hint; hint != nil {
		return hint(e)
	}
	return ""
}

// bodyParts renders the lines between the title and the code: qctl's Text
// for a code Classify replaces, with the server's message and suggestions
// only under -v, or else the server's message and suggestions.
func (e *OperationalError) bodyParts() []string {
	var parts []string
	raw := toPlainText(e.displayMessage())

	class := Classify(e.ErrorCode)
	if class.Outcome == Replace || (class.Outcome == EmptyState && class.Text != "") {
		parts = append(parts, class.Text)
		if e.ProgressPercent != nil {
			parts = append(parts, fmt.Sprintf("Progress: %d%% complete.", *e.ProgressPercent))
		}
		if raw != "" && Verbosity() > 0 {
			said := "Server said:\n" + raw
			if list := e.suggestionList(raw); list != "" {
				said += "\n" + list
			}
			parts = append(parts, said)
		}
		return parts
	}

	if raw != "" {
		parts = append(parts, raw)
	}
	if list := e.suggestionList(raw); list != "" {
		parts = append(parts, list)
	}
	return parts
}

// suggestionList renders Suggestions under a "Suggestions:" heading, one
// "  - " bullet each, or returns "" when none is left to print. A suggestion
// that renders empty is skipped, and so is one that message already contains.
func (e *OperationalError) suggestionList(message string) string {
	var b strings.Builder
	for _, s := range e.Suggestions {
		s = toPlainText(s)
		if s == "" || strings.Contains(message, s) {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("Suggestions:")
		}
		b.WriteString("\n  - " + strings.ReplaceAll(s, "\n", "\n    "))
	}
	return b.String()
}

// displayMessage picks the field the backend actually populated. ResponseMsg
// is checked first for historical reasons only: this backend renames
// response_msg to "msg" on the way out (atlas capi/capi/middleware.py),
// so the branch never fires today.
func (e *OperationalError) displayMessage() string {
	if e.ResponseMsg != "" {
		return e.ResponseMsg
	}
	return e.Message
}

// displayTitle drops a title that carries no information. Every
// middleware-handled error sets title "Error"
// (atlas capi/capi/middleware.py); as a leading line it says nothing the
// message does not. A title a raise site chose deliberately ("Preparing
// table", "Ingestion Error") is kept.
func (e *OperationalError) displayTitle() string {
	if strings.EqualFold(strings.TrimSpace(e.Title), "error") {
		return ""
	}
	return toPlainText(e.Title)
}

// ToPlainText is toPlainText, exported for one caller with the same problem
// and no error to hang it on.
//
// internal/history renders the undo/redo STEP message with it. That message is
// a 200-response body, so it never reaches Format() -- but it is built by the
// SAME backend function that builds the body of the 400 conflict error,
// HistoryHumanReadableMixin.get_history_items_human_readable_msg. Rendering the
// two differently would make the same sentence look different depending on
// whether the undo succeeded.
//
// glamour (internal/markdown's Renderer) is not an alternative here and was measured, not
// assumed: its "notty" style leaves the literal ** in place and inlines the
// frontend route as body text.
func ToPlainText(s string) string { return toPlainText(s) }

// toPlainText renders backend message copy for a terminal: HTML line breaks
// and paragraphs, then the markdown the frontend renders and we do not.
//
// This runs on EVERY rendered message, Passthrough included. It is what keeps
// otherwise-fine copy out of the Replace set: a message whose only CLI problem
// was `**bold**` needs no qctl rewrite once the asterisks are gone.
//
// The renderer itself is markdown.PlainText; the one-line views use
// markdown.Inline, which applies the same prose rules. A value too long for
// the backend to show whole arrives in a ~*value*~ marker (atlas
// docs/message_value_markers.md); an error is a detail view, so PlainText
// prints it whole and literal.
func toPlainText(s string) string { return markdown.PlainText(s) }
