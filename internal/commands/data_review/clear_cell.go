package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"github.com/qlustered/qctl/internal/api"
	"github.com/spf13/cobra"
)

func newClearCellCommand() *cobra.Command {
	return newCellVerdictCommand(cellVerdict{
		verb:     "clear-cell",
		decision: api.RecordActionSchemaDecisionCommentOnly,
		done:     "cleared",
		forbidden: "clearing a cell that carries a stored reject or request_changes verdict requires " +
			"DATA_REVIEW_APPROVE, whoever left that verdict, which the agent role does not hold; every clear " +
			"also requires DATA_REVIEW_EDIT, which the editor role does not hold, and access to this table's data",
	},
		"Clear the verdict on one cell change of a data review",
		`Replace the verdict on one cell change of a data review with comment_only,
which decides nothing.

A cell can carry a stored reject or request_changes verdict, which describe
data-review prints as the cell's verdict and which holds the row. Clearing it
lifts that veto, but comment_only is not an approval: the cell becomes
undecided, so its row stays held (status open, once no other veto stands) until
someone accepts the cell or a reviewer who neither submitted nor edited the row
approves the whole row. Every row carrying the same edit sees the same change.
--comment replaces the cell's comment.

Name the cell with --field <name>, an exact, case-sensitive field name looked
up in the review's changes (one extra request), or with --history-id <id>, the
[h: <id>] that describe data-review prints. Give exactly one.

Requires the DATA_REVIEW_EDIT permission, which the editor role does not hold,
and access to the table's data. Clearing a stored reject or request_changes
verdict, whoever left it, also requires DATA_REVIEW_APPROVE, which the agent
role does not hold.

Examples:
  qctl data-review clear-cell 0190bbbb-1111-4222-8333-444444444444 --field amount --comment "fixed upstream"`)
}
