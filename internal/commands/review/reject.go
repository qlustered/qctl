package review

import (
	"github.com/qlustered/qctl/internal/api"
	"github.com/spf13/cobra"
)

func newRejectCommand() *cobra.Command {
	return newDecisionCommand("reject", api.ReviewDecisionEnumReject, "rejected",
		"Reject a rule review",
		`Reject a rule review.

A single reject terminates the review (status: rejected); it is recoverable
only via 'qctl review reopen'. Submitting a decision again replaces your
previous verdict — this is also how you change your decision.

Requires the REVIEW_APPROVE permission, which excludes the agent role by
design: an AI agent gets a 403 here and a human submits the verdict.

--comment (max 4000 chars) is materialized as a real thread comment linked to
the decision.`)
}
