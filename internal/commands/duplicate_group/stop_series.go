package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"

	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func newStopSeriesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop-series <group-id>",
		Short: "Propose to stop the series allow governing a duplicate group",
		Long: `Propose to stop, in every period of its series, the effective series allow
the group may stop: one that governs it, or its own series allow when a
table-scoped decision of its own governs it instead. Once the proposal has
enough approvals it is enacted: the allow no longer applies anywhere, and the
groups it governed need a decision again unless another decision governs
them. Withdrawing takes back only a pending decision, so this is how an
effective series allow ends.

qctl reads the group first and exits 2 without a request when it has no
series allow to stop, or has a pending decision, which must take effect or be
withdrawn or dismissed first. Approving the proposal is 'qctl duplicate-group
approve'.

` + voteOutputNote,
		Example:     `  qctl duplicate-group stop-series 0190aaaa-1111-4222-8333-444444444444 --table orders --comment "the key repeats only this month" --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE:        runStopSeries,
	}
	addCommonFlags(cmd, "Comment recorded with the proposal")
	return cmd
}

func runStopSeries(cmd *cobra.Command, args []string) error {
	comment, err := commentFlag(cmd)
	if err != nil {
		return err
	}
	groupID, err := preflight(cmd, args)
	if err != nil {
		return err
	}
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	group, err := t.client.Get(t.token(), t.tableID, groupID)
	if err != nil {
		return mapError(err, t.tableID, groupID)
	}
	allowID := group.Caller.StopForSeriesDecisionID
	switch {
	case allowID == nil:
		return qerrors.NewBadUsagef("%s has no effective series allow to stop", groupRef(groupID))
	case group.PendingDecision != nil:
		p := group.PendingDecision
		return qerrors.NewBadUsagef("%s has a pending %s [d: %s]; it must take effect or be withdrawn or dismissed "+
			"before a stop-series proposal", groupRef(groupID), labelOf(p), p.ID)
	}
	label := duplicate_groups.KindLabel(duplicate_groups.KindAllow, nil, nil)
	var period *string
	if allow := findDecision(group, *allowID); allow != nil {
		label, period = labelOf(allow), allow.PeriodKey
	}
	question := fmt.Sprintf("Propose to stop the series-scoped %s [d: %s]%s in every period of its series? "+
		"Once approved it no longer applies anywhere, and the groups it governed need a decision again "+
		"unless another decision governs them.", label, allowID, periodSuffix(period))
	if err := confirm(cmd, "stopping a series allow", question); err != nil {
		return err
	}
	result, err := t.client.StopSeries(t.token(), t.tableID, groupID, *allowID, comment)
	if err != nil {
		return mapStopSeriesError(err, t.tableID, groupID)
	}
	return writeDecide(cmd, t.tableID, groupID, "proposed", result)
}
