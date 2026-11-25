package change_proposals //nolint:revive // Established package-path style in this repo (rule_reviews, privilege_requests).

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/terminal"
)

// APIError is the error every Client method returns for a non-2xx response.
type APIError = comments.APIError

func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}

// Error codes of the proposal write routes.
const (
	CodeActive          = "CHANGE_PROPOSAL_ACTIVE"
	CodeVersionMismatch = "CHANGE_PROPOSAL_VERSION_MISMATCH"
	CodeStateConflict   = "CHANGE_PROPOSAL_STATE_CONFLICT"
	CodeStale           = "CHANGE_PROPOSAL_STALE"
	CodeImpactChanged   = "CHANGE_PROPOSAL_IMPACT_CHANGED"
	CodeBlocked         = "CHANGE_PROPOSAL_BLOCKED"
	CodePolicyDenied    = "CHANGE_REVIEW_POLICY_DENIED"
)

// IsProposalConflict reports whether err is one of the CHANGE_PROPOSAL_*
// errors Explain handles.
func IsProposalConflict(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case CodeActive, CodeVersionMismatch, CodeStateConflict, CodeStale, CodeImpactChanged, CodeBlocked:
		return true
	}
	return false
}

// Explain appends the next step to a CHANGE_PROPOSAL_* error, naming the
// proposals its context carries and, for a refused approval, the fresh
// review's reasons or blockers. Any other error is returned unchanged.
func Explain(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	var b strings.Builder
	switch apiErr.Code {
	case CodeActive:
		ids := contextStrings(apiErr.Context["active_change_proposal_ids"])
		if len(ids) == 0 {
			return err
		}
		if rows := contextInts(apiErr.Context["row_ids"]); len(rows) > 0 {
			fmt.Fprintf(&b, "another live proposal holds the issue, or its rows %s; withdraw it, or wait for its decision:",
				strings.Join(rows, ", "))
		} else {
			b.WriteString("another live proposal holds the issue; withdraw it, or wait for its decision:")
		}
		for _, id := range ids {
			fmt.Fprintf(&b, "\n  qctl describe change-proposal %s", terminal.Sanitize(id))
		}
	case CodeVersionMismatch, CodeStateConflict:
		proposal, _ := apiErr.Context["change_proposal"].(map[string]interface{})
		id, _ := proposal["id"].(string)
		if id == "" {
			return err
		}
		if state, _ := proposal["state"].(string); state != "" && apiErr.Code == CodeStateConflict {
			fmt.Fprintf(&b, "the proposal is now %s; ", terminal.Sanitize(state))
		}
		if apiErr.Code == CodeVersionMismatch {
			b.WriteString("the proposal changed since it was read; ")
		}
		b.WriteString("nothing was changed.\n")
		writeNow(&b, proposal)
		fmt.Fprintf(&b, "Re-read it:\n  qctl describe change-proposal %s", terminal.Sanitize(id))
	case CodeStale, CodeImpactChanged, CodeBlocked:
		proposal, _ := apiErr.Context["change_proposal"].(map[string]interface{})
		id, _ := proposal["id"].(string)
		if id == "" {
			return err
		}
		review, _ := apiErr.Context["review"].(map[string]interface{})
		explainReviewConflict(&b, apiErr.Code, review, proposal)
		b.WriteString("Nothing ran.\n")
		writeNow(&b, proposal)
		fmt.Fprintf(&b, "Re-read it:\n  qctl describe change-proposal %s", terminal.Sanitize(id))
	default:
		return err
	}
	return fmt.Errorf("%w\n%s", err, b.String())
}

func contextStrings(v interface{}) []string {
	items, _ := v.([]interface{})
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// contextInts returns the whole numbers of a JSON array as decimal text.
func contextInts(v interface{}) []string {
	items, _ := v.([]interface{})
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch n := item.(type) {
		case int64:
			out = append(out, strconv.FormatInt(n, 10))
		case float64:
			if n == float64(int64(n)) {
				out = append(out, strconv.FormatInt(int64(n), 10))
			}
		}
	}
	return out
}

// writeNow writes the proposal a 409 carries: its state, version and the
// steps it allows the caller.
func writeNow(b *strings.Builder, proposal map[string]interface{}) {
	steps := contextStrings(proposal["allowed_actions"])
	allowed := "none"
	if len(steps) > 0 {
		allowed = strings.Join(steps, ", ")
	}
	fmt.Fprintf(b, "Now: state %s, version %v; your next steps: %s\n",
		terminal.Sanitize(fmt.Sprint(proposal["state"])), proposal["version_id"], terminal.Sanitize(allowed))
}

// explainReviewConflict writes what a refused approval's fresh review of
// proposal found.
func explainReviewConflict(b *strings.Builder, code string, review, proposal map[string]interface{}) {
	id, _ := proposal["id"].(string)
	dependsOn, _ := proposal["depends_on_id"].(string)
	var steps strings.Builder
	switch code {
	case CodeStale:
		b.WriteString("the proposal no longer applies as submitted:\n")
		for _, r := range contextMaps(review["reasons"]) {
			reason := fmt.Sprint(r["reason"])
			fmt.Fprintf(b, "  - %s: %s\n", terminal.Sanitize(reason), terminal.Sanitize(fmt.Sprint(r["message"])))
			writeDependencyStep(&steps, reason, id, dependsOn)
		}
		b.WriteString(terminal.Sanitize(steps.String()))
		b.WriteString("Its authors must edit it; send it back with 'qctl change-proposal request-changes'.\n")
	case CodeImpactChanged:
		b.WriteString("what approving it changes moved since you read it; read the new impact and approve again.\n")
	case CodeBlocked:
		b.WriteString("the operation would be refused now:\n")
		for _, bl := range contextMaps(review["blockers"]) {
			fmt.Fprintf(b, "  - %s\n", terminal.Sanitize(BlockerLine(bl)))
			name, _ := bl["blocker"].(string)
			writeDependencyStep(&steps, name, id, dependsOn)
		}
		b.WriteString(terminal.Sanitize(steps.String()))
	}
}

// BlockerLine renders one review blocker as "<blocker> (<status> <code>):
// <message>", leaving out the parts it lacks.
func BlockerLine(m map[string]interface{}) string {
	var head []string
	for _, k := range []string{"status_code", "error_code"} {
		if v, ok := m[k]; ok && v != nil && fmt.Sprint(v) != "" {
			head = append(head, fmt.Sprint(v))
		}
	}
	line := fmt.Sprint(m["blocker"])
	if m["blocker"] == nil {
		line = "blocker"
	}
	if len(head) > 0 {
		line += " (" + strings.Join(head, " ") + ")"
	}
	if msg, _ := m["message"].(string); msg != "" {
		line += ": " + msg
	}
	return line
}

func contextMaps(v interface{}) []map[string]interface{} {
	items, _ := v.([]interface{})
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}
