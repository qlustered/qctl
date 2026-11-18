package privilege_requests //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, value_mapping).

import (
	"fmt"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/qlustered/qctl/internal/terminal"
)

// RenderDetail renders the agent-facing text payload for
// `describe privilege-request`: the request, the people on it, the audit rows
// its approval wrote, and — for a Pending request only — the two verbs that
// decide it.
//
// It never computes or displays an expiry. The TTL is a server setting that
// is not on the wire, and the server evaluates it only when someone tries to
// approve, so "Pending" here is what the server says, not a promise that an
// approval will be accepted.
func RenderDetail(d *Detail) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# privilege-request/%s\n", d.ID)
	fmt.Fprintf(&b, "Status: %s\n", d.Status)
	fmt.Fprintf(&b, "Action: %s (%s: %s → %s)\n",
		ActionOf(d.OldValue, d.NewValue), d.PermissionKey, d.OldValue, d.NewValue)

	target := personLabel(d.TargetUser, d.TargetUserID)
	if d.TargetUser != nil {
		target += fmt.Sprintf(" — is_ops now: %t", d.TargetUser.IsOps)
	}
	fmt.Fprintf(&b, "Target: %s\n", target)
	fmt.Fprintf(&b, "Requestor: %s\n", personLabel(d.Requestor, d.RequestorID))
	renderDecision(&b, d)
	fmt.Fprintf(&b, "Requested: %s\n", timestamp(d.RequestedAt))

	b.WriteString("Justification:\n")
	for _, line := range strings.Split(strings.TrimRight(d.Justification, "\n"), "\n") {
		fmt.Fprintf(&b, "  %s\n", line)
	}

	renderAudit(&b, d)

	if d.Status == api.PrivilegeRequestStatusPending {
		id := d.ID.String()
		b.WriteString("\n## NEXT ACTIONS\n")
		fmt.Fprintf(&b, "- approve: qctl privilege approve %s%s\n", id, prompt.YesFlag(false))
		fmt.Fprintf(&b, "- reject:  qctl privilege reject %s\n", id)
		b.WriteString("The approver must be neither the requestor nor the target (the two-person rule). " +
			"The target cannot reject it either, but the requestor can withdraw their own request with reject.\n")
	}

	return terminal.Sanitize(b.String())
}

// renderDecision writes who decided the request and when. The wire has one
// approver_id for both outcomes, so a rejected request's "approver" is the
// user who rejected it and is labelled that way; an expired request has a
// decision time and nobody on it.
func renderDecision(b *strings.Builder, d *Detail) {
	if d.Status == api.PrivilegeRequestStatusPending {
		b.WriteString("Approver: pending (no decision yet)\n")
		return
	}
	label := "Approver"
	if d.Status == api.PrivilegeRequestStatusRejected {
		label = "Rejected by"
	}
	who := "(none recorded)"
	if d.ApproverID != nil {
		who = personLabel(d.Approver, *d.ApproverID)
	}
	fmt.Fprintf(b, "%s: %s\n", label, who)
	decided := "(not recorded)"
	if d.DecisionAt != nil {
		decided = timestamp(*d.DecisionAt)
	}
	fmt.Fprintf(b, "Decided: %s\n", decided)
}

// renderAudit writes the AUDIT section. Only an APPLIED change writes a row,
// so a rejected or expired request always shows none.
func renderAudit(b *strings.Builder, d *Detail) {
	var entries []AuditEntry
	if d.AuditEntries != nil {
		entries = *d.AuditEntries
	}
	fmt.Fprintf(b, "\n## AUDIT (%d)\n", len(entries))
	if len(entries) == 0 {
		b.WriteString("(none — no change has been applied)\n")
		return
	}
	// The rows carry people as bare UUIDs. The detail already embeds the
	// three people a row can name, so they are labelled from there rather
	// than from a users-directory request.
	known := map[openapi_types.UUID]*UserInfo{}
	for _, u := range []*UserInfo{d.Requestor, d.TargetUser, d.Approver} {
		if u != nil {
			known[u.ID] = u
		}
	}
	for _, e := range entries {
		fmt.Fprintf(b, "- %s [entry %s]\n", timestamp(e.PerformedAt), e.ID)
		fmt.Fprintf(b, "  change: %s (%s: %s → %s)\n",
			ActionOf(e.OldValue, e.NewValue), e.PermissionKey, e.OldValue, e.NewValue)
		fmt.Fprintf(b, "  performed by: %s\n", personLabel(known[e.PerformedByID], e.PerformedByID))
		fmt.Fprintf(b, "  from: %s, user agent %q\n", e.PerformedByIP, e.PerformedByUserAgent)
	}
}

// personLabel renders an embedded user as "email (First Last, uuid)", falling
// back to the bare UUID when the server sent no user object — every embedded
// user on the detail is nullable.
func personLabel(u *UserInfo, id openapi_types.UUID) string {
	if u == nil {
		return id.String()
	}
	parts := make([]string, 0, 2)
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		parts = append(parts, name)
	}
	parts = append(parts, id.String())
	if u.Email == "" {
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%s (%s)", u.Email, strings.Join(parts, ", "))
}

// timestamp renders an absolute UTC time followed by its relative age.
func timestamp(t time.Time) string {
	return fmt.Sprintf("%s (%s)", t.UTC().Format(time.RFC3339), timeutil.FormatRelative(t))
}
