package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

// Issue is one entry of the composite issue list the apply route answers
// with: under errors and warnings of a validate_only answer, and as the
// detail of a 400 or 409 refusal.
type Issue = api.CompositeValidationIssue

// FormatIssue renders an issue as "<loc>: <msg> (<code>)", with loc joined by
// dots exactly as the server sent it. user_message stands in for an empty msg.
func FormatIssue(issue Issue) string {
	text := issue.Msg
	if text == "" && issue.UserMessage != nil {
		text = *issue.UserMessage
	}
	var b strings.Builder
	if len(issue.Loc) > 0 {
		b.WriteString(strings.Join(issue.Loc, "."))
		b.WriteString(": ")
	}
	b.WriteString(text)
	if issue.Code != nil && *issue.Code != "" {
		fmt.Fprintf(&b, " (%s)", *issue.Code)
	}
	return b.String()
}

// WriteWarnings prints each warning on its own "Warning: " line.
func WriteWarnings(w io.Writer, warnings []Issue) {
	for _, warning := range warnings {
		fmt.Fprintf(w, "Warning: %s\n", terminal.Sanitize(FormatIssue(warning)))
	}
}

// formatIssueList renders issues one per line as an indented bullet list.
func formatIssueList(issues []Issue) string {
	lines := make([]string, len(issues))
	for i, issue := range issues {
		lines[i] = "  - " + FormatIssue(issue)
	}
	return strings.Join(lines, "\n")
}

// IssuesError is a refusal whose detail is an issue list. It unwraps to the
// HTTP error, which carries the exit code.
type IssuesError struct {
	StatusCode int
	Issues     []Issue
	err        error
}

func (e *IssuesError) Error() string {
	return fmt.Sprintf("the server refused the manifest (%d %s):\n%s",
		e.StatusCode, http.StatusText(e.StatusCode), formatIssueList(e.Issues))
}

// Unwrap returns the HTTP error.
func (e *IssuesError) Unwrap() error { return e.err }

// withIssues returns err as an *IssuesError when a 400 or 409 body's detail is
// an issue list, and err unchanged otherwise.
func withIssues(statusCode int, body []byte, err error) error {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusConflict {
		return err
	}
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return err
	}
	var issues []Issue
	if json.Unmarshal(envelope.Detail, &issues) != nil || len(issues) == 0 {
		return err
	}
	for _, issue := range issues {
		if issue.Msg == "" && issue.UserMessage == nil {
			return err
		}
	}
	return &IssuesError{StatusCode: statusCode, Issues: issues, err: err}
}
