package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newWithdrawCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "withdraw <group-id>",
		Short: "Withdraw your pending decision on a duplicate group",
		Long: `Withdraw your own pending decision on the group and apply the withdrawal:
the group keeps the decision that governed it before, or is undecided again.
Only its proposer may withdraw a pending decision. An effective decision
cannot be withdrawn: propose a different one with allow, keep-one or
not-duplicates, or end a series allow with stop-series.

qctl reads the group first; --decision works as on approve.

` + voteOutputNote,
		Example:     `  qctl duplicate-group withdraw 0190aaaa-1111-4222-8333-444444444444 --table orders --comment "wrong key" --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE:        runWithdraw,
	}
	addCommonFlags(cmd, "Comment recorded with the withdrawal")
	addDecisionFlag(cmd, "withdraw")
	return cmd
}

func runWithdraw(cmd *cobra.Command, args []string) error {
	comment, err := commentFlag(cmd)
	if err != nil {
		return err
	}
	v, err := readPending(cmd, args, "withdraw")
	if err != nil {
		return err
	}
	question := fmt.Sprintf("Withdraw %s on table/%d (%s)?%s", v.pendingRef(), v.t.tableID, v.approvals(),
		settleNote(v.group, v.pending))
	if err := confirm(cmd, "withdrawing a duplicate group decision", question); err != nil {
		return err
	}
	result, err := v.t.client.Withdraw(v.t.token(), v.t.tableID, v.groupID, v.pending.ID, comment)
	if err != nil {
		return mapVoteError(err, v.t.tableID, v.groupID, false)
	}
	return writeVote(cmd, v.t.tableID, v.groupID, "withdrew", result, "")
}
