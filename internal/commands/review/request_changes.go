package review

import (
	"github.com/qlustered/qctl/internal/api"
	"github.com/spf13/cobra"
)

func newRequestChangesCommand() *cobra.Command {
	return newDecisionCommand("request-changes", api.ReviewDecisionEnumRequestChanges, "changes requested",
		"Request changes on a rule review",
		`Request changes on a rule review.

Moves the review to changes_requested and blocks approval until the request
is withdrawn ('qctl review withdraw') or superseded by your new decision.
Submitting a decision again replaces your previous verdict — this is also how
you change your decision.

Requires the REVIEW_APPROVE permission, which excludes the agent role by
design: an AI agent gets a 403 here and a human submits the verdict.

--comment (max 4000 chars) is materialized as a real thread comment linked to
the decision.`)
}
