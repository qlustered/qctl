package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"github.com/qlustered/qctl/internal/api"
	"github.com/spf13/cobra"
)

func newWithdrawCellCommand() *cobra.Command {
	return newCellVerdictCommand(cellVerdict{
		verb:     "withdraw-cell",
		decision: api.RecordActionSchemaDecisionWithdraw,
		done:     "withdrawn",
		forbidden: "you can only withdraw your own cell decision; withdrawing a stored reject or " +
			"request_changes verdict also requires DATA_REVIEW_APPROVE; every withdrawal requires " +
			"DATA_REVIEW_EDIT and access to this table's data",
	},
		"Withdraw your own decision on one cell change of a data review",
		`Delete your own decision on one cell change of a data review.

Name the cell with --field <name>, an exact, case-sensitive field name looked
up in the review's changes (one extra request), or with --history-id <id>, the
[h: <id>] that describe data-review prints. Give exactly one.

Only the current decision's author can withdraw it. Another reviewer's
decision is refused with exit 4; no standing decision is refused with exit 2.
The removed decision's id and prior verdict remain in the audit. Withdrawal
takes no comment.

Every row carrying the same edit shares its decision, so withdrawing it
recomputes other affected reviews too. The result reports their statuses and
counts; a released row is never put back in quarantine.

Requires DATA_REVIEW_EDIT and access to the table's data. Withdrawing a stored
reject or request_changes verdict also requires DATA_REVIEW_APPROVE.

To remove your whole-row decision instead, use qctl data-review withdraw.
To replace a cell verdict with comment_only, use qctl data-review clear-cell.

Examples:
  qctl data-review withdraw-cell 0190bbbb-1111-4222-8333-444444444444 --field amount
  qctl data-review withdraw-cell 0190bbbb-1111-4222-8333-444444444444 --history-id 811`)
}
