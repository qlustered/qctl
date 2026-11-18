package rule_reviews //nolint:revive // Established package path; renaming would break imports.

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// RenderDetail renders the agent-facing markdown payload for
// `describe rule-review`. Section order is frozen — later phases insert
// DECISIONS/COMMENTS between POLICY and NEXT ACTIONS, never reorder.
func RenderDetail(d *ReviewDetail) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# RULE REVIEW %s\n", d.ID)
	fmt.Fprintf(&b, "Title: %s\n", d.Title)

	ruleName, ruleRelease := "", ""
	if d.RuleRevision != nil {
		ruleName, ruleRelease = d.RuleRevision.Name, d.RuleRevision.Release
	}
	fmt.Fprintf(&b, "Rule: %s @ %s (revision %s)\n", ruleName, ruleRelease, d.RuleRevisionID)

	switch {
	case d.DatasetID != nil && d.DatasetName != nil && *d.DatasetName != "":
		fmt.Fprintf(&b, "Scope: table %s (%d)\n", *d.DatasetName, *d.DatasetID)
	case d.DatasetID != nil:
		fmt.Fprintf(&b, "Scope: table %d\n", *d.DatasetID)
	default:
		b.WriteString("Scope: org-wide\n")
	}

	fmt.Fprintf(&b, "Status: %s | Approvals: %d/%d | Requestor: %s\n",
		d.Status, d.CurrentApprovals, d.RequiredApprovals, UserDisplay(d.Requestor, d.RequestorID))

	if d.AssignedReviewers != nil && len(*d.AssignedReviewers) > 0 {
		names := make([]string, 0, len(*d.AssignedReviewers))
		for i := range *d.AssignedReviewers {
			r := (*d.AssignedReviewers)[i]
			names = append(names, UserDisplay(&r, r.ID))
		}
		fmt.Fprintf(&b, "Reviewers: %s\n", strings.Join(names, ", "))
	} else {
		b.WriteString("Reviewers: (none assigned)\n")
	}

	b.WriteString(renderDryRunLine(d) + "\n")

	b.WriteString("\n## POLICY (effective, resolved now — may differ from the creation-time snapshot)\n")
	if p := d.EffectivePolicy; p != nil {
		fmt.Fprintf(&b, "requires_review: %t | min_reviewers: %d | require_dry_run: %t\n",
			p.RequiresReview, p.MinReviewers, p.RequireDryRun)
		fmt.Fprintf(&b, "require_dry_run_success: %t | auto_enable_on_approval: %t | source: %s\n",
			p.RequireDryRunSuccess, p.AutoEnableOnApproval, p.Source)
	} else {
		b.WriteString("(not returned by the backend)\n")
	}

	renderDecisions(&b, d)
	renderComments(&b, d)

	b.WriteString("\n## NEXT ACTIONS\n")
	renderNextActions(&b, d, ruleName, ruleRelease)

	return terminal.Sanitize(b.String())
}

// renderDecisions writes the DECISIONS section. The backend upserts one row
// per reviewer, so every row already is that reviewer's latest verdict.
func renderDecisions(b *strings.Builder, d *ReviewDetail) {
	var decisions []Decision
	if d.Decisions != nil {
		decisions = *d.Decisions
	}
	fmt.Fprintf(b, "\n## DECISIONS (%d)\n", len(decisions))
	for i := range decisions {
		dec := decisions[i]
		fmt.Fprintf(b, "- %s by %s (%s)",
			strings.ToUpper(string(dec.Decision)),
			UserDisplay(dec.Reviewer, dec.ReviewerID),
			timeutil.FormatRelative(dec.CreatedAt))
		if dec.Comment != nil && *dec.Comment != "" {
			fmt.Fprintf(b, ": %q", DeRenderMentions(*dec.Comment))
		}
		b.WriteString("\n")
	}
}

// renderComments writes the COMMENTS section: the nested thread (wire order,
// created_at ascending) with full-UUID tags so agents can reply/edit/delete
// without a second lookup.
func renderComments(b *strings.Builder, d *ReviewDetail) {
	var thread []Comment
	if d.Comments != nil {
		thread = *d.Comments
	}
	fmt.Fprintf(b, "\n## COMMENTS (%d top-level, %d total)\n", len(thread), comments.CountComments(thread))
	for i := range thread {
		comments.RenderComment(b, &thread[i], 0)
	}
}

// renderDryRunLine builds the gate-aware "Dry run:" header line.
func renderDryRunLine(d *ReviewDetail) string {
	var requireDryRun, requireSuccess bool
	if p := d.EffectivePolicy; p != nil {
		requireDryRun = p.RequireDryRun
		requireSuccess = p.RequireDryRunSuccess
	}

	if d.DryRunJobID == nil {
		base := "Dry run: (none linked)"
		if d.EffectivePolicy != nil && (requireDryRun || requireSuccess) {
			return base + fmt.Sprintf(
				" — gate UNMET: policy requires a linked dry run; link one: qctl review link-dry-run %s <job-id>", d.ID)
		}
		return base
	}

	job := *d.DryRunJobID
	head := fmt.Sprintf("Dry run: job %d", job)
	finished := false
	if d.DryRunJobStatus != nil {
		head += fmt.Sprintf(" (%s)", *d.DryRunJobStatus)
		finished = *d.DryRunJobStatus == api.IngestionJobStateFinished
	}
	verify := fmt.Sprintf(" — verify: qctl inspect dry-run-job %d", job)

	switch {
	case d.EffectivePolicy == nil:
		return head
	case requireSuccess:
		if finished {
			return head + " — gate: require_dry_run_success; job state satisfies it," +
				" success also needs no rule load errors, rule-bug or sandbox-failure findings" + verify
		}
		state := "unknown"
		if d.DryRunJobStatus != nil {
			state = string(*d.DryRunJobStatus)
		}
		return head + fmt.Sprintf(" — gate: require_dry_run_success; job state %s does NOT yet satisfy it"+
			" (needs state=finished with no rule load errors, rule-bug or sandbox-failure findings)", state) + verify
	case requireDryRun:
		return head + " — gate: require_dry_run; a linked dry run satisfies it"
	default:
		return head + " — policy does not require a dry run"
	}
}

// renderNextActions writes status-aware copyable commands. Only reference
// commands that already exist (later phases add their own lines).
func renderNextActions(b *strings.Builder, d *ReviewDetail, ruleName, ruleRelease string) {
	id := d.ID.String()
	switch d.Status {
	case api.ReviewRequestStatusOpen, api.ReviewRequestStatusChangesRequested:
		fmt.Fprintf(b, "- comment:            qctl review comment %s --body \"...\"\n", id)
		fmt.Fprintf(b, "- approve (human):    qctl review approve %s\n", id)
		fmt.Fprintf(b, "- request changes:    qctl review request-changes %s --comment \"...\"\n", id)
		fmt.Fprintf(b, "- assign reviewers:   qctl review assign %s --reviewer <email>\n", id)
		fmt.Fprintf(b, "- nudge reviewers:    qctl review re-request %s\n", id)
		fmt.Fprintf(b, "- close the review:   qctl review close %s\n", id)
	case api.ReviewRequestStatusRejected:
		fmt.Fprintf(b, "- reopen:             qctl review reopen %s\n", id)
		fmt.Fprintf(b, "- withdraw (as the rejecting reviewer): qctl review withdraw %s\n", id)
	case api.ReviewRequestStatusApproved:
		if ruleName != "" {
			fmt.Fprintf(b, "- enable the rule (if not auto-enabled): qctl enable rule %s --release %s\n", ruleName, ruleRelease)
		} else {
			fmt.Fprintf(b, "- enable the rule (if not auto-enabled): qctl enable rule <name> --release <release>\n")
		}
	default: // closed
		b.WriteString("(review is closed — no further actions)\n")
	}
	fmt.Fprintf(b, "- audit trail:        qctl review audit %s\n", id)
}
