package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/spf13/cobra"
)

var withdrawDecision = bulkDecision{
	verb: "withdraw",
	done: "decision withdrawn",
	ask:  "Withdraw your decision on",
	send: func(c *data_reviews.Client, token string, sel data_reviews.BatchSelector, _ string) (*data_reviews.BatchResult, error) {
		return c.Withdraw(token, sel)
	},
}

func newWithdrawCommand() *cobra.Command {
	return newBulkDecisionCommand(withdrawDecision,
		"Withdraw your own whole-row decision on data reviews",
		`Delete your own whole-row decision (approve or reject) on one or more data
reviews. Other reviewers' decisions and every per-cell decision stay.
To remove your own per-cell decision instead, use qctl data-review withdraw-cell.

The status is recomputed. Withdrawing never puts a released row back in
quarantine. If it leaves no veto, no undecided cell and enough approvals, the
review becomes approved, and the row is released when the table's policy
releases on approval. A closed review, or one on which you have no decision,
is skipped with REVIEW_NOT_OPEN, the backend's catch-all code, so read the
reason.

Name the reviews either as ids (up to 500, duplicates dropped) or with
--group <uuid>, which covers every open or changes_requested review in that
submission. An id list also reaches an approved review; --group does not. An id
list never prompts; --group asks first, and needs --yes when stdin is not a
terminal. Withdraw takes no comment.

Each processed review prints one line; each skipped review prints the backend's
reason and error code. Any skip exits non-zero: when nothing was processed and
every skip has the same code, NOT_FOUND exits 3, VIEW_ONLY 4 and
REVIEW_NOT_OPEN 2; any other outcome with a skip exits 1.

-o json|yaml prints the raw {processed, skipped} result.

Requires the DATA_REVIEW_APPROVE permission, like approve; the editor and
agent roles do not hold it.

Examples:
  qctl data-review withdraw 0190bbbb-1111-4222-8333-444444444444`)
}
