package review

import (
	"github.com/qlustered/qctl/internal/api"
	"github.com/spf13/cobra"
)

func newApproveCommand() *cobra.Command {
	return newDecisionCommand("approve", api.ReviewDecisionEnumApprove, "approved",
		"Approve a rule review",
		`Approve a rule review.

Counts toward required_approvals (your own approve never counts on a review
you requested). When the threshold is met and no request-changes is standing,
the review completes as approved.

Submitting a decision again replaces your previous verdict — this is also how
you change your decision. Requires the REVIEW_APPROVE permission, which
excludes the agent role by design: an AI agent gets a 403 here and a human
submits the verdict.

--comment (max 4000 chars) is materialized as a real thread comment linked to
the decision.`)
}
