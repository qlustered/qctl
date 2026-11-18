package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/spf13/cobra"
)

var approveDecision = bulkDecision{
	verb:       "approve",
	done:       "approved",
	ask:        "Approve",
	comment:    true,
	warnIfHeld: true,
	send: func(c *data_reviews.Client, token string, sel data_reviews.BatchSelector, comment string) (*data_reviews.BatchResult, error) {
		return c.Approve(token, sel, comment)
	},
}

func newApproveCommand() *cobra.Command {
	return newBulkDecisionCommand(approveDecision,
		"Approve whole rows under data review, by review id or by submission",
		`Record your whole-row approval on one or more data reviews.

Name the reviews either as ids (up to 500, duplicates dropped) or with
--group <uuid>, which approves every open or changes_requested review in
that submission. An id list never prompts; --group asks first, and needs --yes
when stdin is not a terminal. An id list does not filter by status, so an
approved or closed id is skipped with REVIEW_NOT_OPEN.

An approval by anyone who submitted the row in any submission or ever edited
it, including undo and redo actors, is recorded but never counts toward the
approvals the row needs. An approval never overrides a standing veto: when a
processed review is still changes_requested, a warning on stderr says the row
is held.

Each processed review prints one line with its status, approvals and released
row count; each skipped review prints one line with the backend's reason and
error code. Any skip exits non-zero: when nothing was processed and every skip
has the same code, NOT_FOUND exits 3, VIEW_ONLY and APPROVER_NOT_DESIGNATED
4, and REVIEW_NOT_OPEN 2; any other outcome with a skip exits 1.

-o json|yaml prints the raw {processed, skipped} result.

Requires the DATA_REVIEW_APPROVE permission, which the editor and agent roles
do not hold.

Examples:
  qctl data-review approve 0190bbbb-1111-4222-8333-444444444444
  qctl data-review approve --group 11111111-2222-4333-8444-555555555555 --comment "spot-checked" --yes`)
}
