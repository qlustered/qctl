package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"github.com/qlustered/qctl/internal/api"
	"github.com/spf13/cobra"
)

func newAcceptCellCommand() *cobra.Command {
	return newCellVerdictCommand(cellVerdict{
		verb:     "accept-cell",
		decision: api.RecordActionSchemaDecisionApprove,
		done:     "accepted",
		forbidden: "accepting a cell requires DATA_REVIEW_APPROVE even though the route only asks for " +
			"DATA_REVIEW_EDIT at the door, so the editor and agent roles cannot accept and a member, admin or " +
			"ops user must; it also requires access to this table's data",
	},
		"Accept one cell change on a data review",
		`Record an approve verdict on one cell change of a data review.

Name the cell with --field <name>, an exact, case-sensitive field name looked
up in the review's changes (one extra request), or with --history-id <id>, the
[h: <id>] that describe data-review prints. Give exactly one.

A cell verdict is stored once per change, not once per review: every row still
carrying the same edit shares it, so accepting it can move and release other
rows too. The result line counts the reviews and rows it touched, and a stderr
line says when other reviews were affected.

Requires the DATA_REVIEW_APPROVE permission, which the editor and agent roles
do not hold, and access to the table's data.

Examples:
  qctl data-review accept-cell 0190bbbb-1111-4222-8333-444444444444 --field amount
  qctl data-review accept-cell 0190bbbb-1111-4222-8333-444444444444 --history-id 811 --comment "matches the invoice"`)
}
