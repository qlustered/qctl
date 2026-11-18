package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

const voteOutputNote = `Prompts unless --yes. -o json|yaml prints the server's {decision, state,
apply}; -o name prints duplicate-group/<uuid>.`

func newApproveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <group-id>",
		Short: "Approve a duplicate group's pending decision",
		Long: `Approve the group's pending decision. qctl reads the group and approves its
pending decision, or exits 2 without a request when nothing is pending.
--decision <uuid> names the decision you mean to approve: when the group's
pending decision is another one, qctl exits 2 naming it.

Approving again replaces your earlier vote on the decision, a disagreement
included, and its comment. When you already approved it and give no
--comment, qctl sends nothing and exits 0. A decision takes effect once its
counted approvals reach the policy's minimum while no counted reviewer
disagrees with it.

` + voteOutputNote,
		Example:     `  qctl duplicate-group approve 0190aaaa-1111-4222-8333-444444444444 --table orders --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE:        runApprove,
	}
	addCommonFlags(cmd, "Comment recorded with your approval")
	addDecisionFlag(cmd, "approve")
	return cmd
}

func newDisagreeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disagree <group-id>",
		Short: "Disagree with a duplicate group's pending decision",
		Long: `Disagree with the group's pending decision and say why: --reason is required
and must not be blank. The decision cannot take effect while your
disagreement stands; approving it later replaces the disagreement. A
decision you proposed cannot be disagreed with: withdraw it instead.

qctl reads the group first; --decision works as on approve.

` + voteOutputNote,
		Example:     `  qctl duplicate-group disagree 0190aaaa-1111-4222-8333-444444444444 --table orders --reason "the rows are two orders" --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE:        runDisagree,
	}
	addCommonFlags(cmd, "")
	cmd.Flags().String("reason", "", "Why you disagree (required)")
	addDecisionFlag(cmd, "disagree with")
	return cmd
}

func newDismissCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dismiss <group-id>",
		Short: "Dismiss another reviewer's pending decision on a duplicate group",
		Long: `Dismiss another reviewer's pending decision on the group and say why:
--reason is required and must not be blank. Only an admin or ops user may
dismiss, and not a decision they proposed: withdraw that instead. The group
keeps the decision that governed it before, or is undecided again.

qctl reads the group first; --decision works as on approve.

` + voteOutputNote,
		Example:     `  qctl duplicate-group dismiss 0190aaaa-1111-4222-8333-444444444444 --table orders --reason "proposed on the wrong key" --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE:        runDismiss,
	}
	addCommonFlags(cmd, "")
	cmd.Flags().String("reason", "", "Why the decision is dismissed (required)")
	addDecisionFlag(cmd, "dismiss")
	return cmd
}

func addDecisionFlag(cmd *cobra.Command, verb string) {
	cmd.Flags().String("decision", "",
		fmt.Sprintf("Pending decision to %s (full UUID); exits 2 when the group's pending decision is another one", verb))
}

// decisionFlag returns --decision, nil when it was not given; one that is
// not a full UUID exits 2.
func decisionFlag(cmd *cobra.Command) (*openapi_types.UUID, error) {
	if !cmd.Flags().Changed("decision") {
		return nil, nil
	}
	raw, _ := cmd.Flags().GetString("decision")
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return nil, qerrors.NewBadUsagef("invalid --decision %q: expected a full UUID", raw)
	}
	return &parsed, nil
}

// reasonFlag returns --reason; a missing or blank one exits 2.
func reasonFlag(cmd *cobra.Command) (string, error) {
	reason, _ := cmd.Flags().GetString("reason")
	if strings.TrimSpace(reason) == "" {
		return "", qerrors.NewBadUsage("--reason is required and must not be blank")
	}
	return reason, nil
}

// vote is a pending-decision verb's target, read before its request.
type vote struct {
	t       target
	groupID openapi_types.UUID
	group   *duplicate_groups.Group
	pending *duplicate_groups.Decision
}

// readPending checks the group argument, the table selector and --decision,
// reads the group and returns its pending decision for verb.
func readPending(cmd *cobra.Command, args []string, verb string) (vote, error) {
	var v vote
	groupID, err := preflight(cmd, args)
	if err != nil {
		return v, err
	}
	wanted, err := decisionFlag(cmd)
	if err != nil {
		return v, err
	}
	t, err := resolveTarget(cmd)
	if err != nil {
		return v, err
	}
	group, err := t.client.Get(t.token(), t.tableID, groupID)
	if err != nil {
		return v, mapError(err, t.tableID, groupID)
	}
	pending, err := pendingTarget(group, wanted, verb, t.tableID)
	if err != nil {
		return v, err
	}
	return vote{t: t, groupID: groupID, group: group, pending: pending}, nil
}

// pendingTarget returns the group's pending decision, which wanted, when
// given, must be; none, or another one, exits 2.
func pendingTarget(group *duplicate_groups.Group, wanted *openapi_types.UUID, verb string, tableID int) (*duplicate_groups.Decision, error) {
	p := group.PendingDecision
	if p == nil {
		msg := fmt.Sprintf("%s has no pending decision to %s", groupRef(group.ID), verb)
		if eff := governing(group); eff != nil {
			msg += fmt.Sprintf("; its effective %s stays until a change takes effect: propose one with allow, keep-one or not-duplicates",
				labelOf(eff))
		}
		if id := group.Caller.StopForSeriesDecisionID; id != nil {
			msg += fmt.Sprintf("; 'qctl duplicate-group stop-series %s --table-id %d' proposes to stop the series allow [d: %s] in every period",
				group.ID, tableID, id)
		}
		return nil, qerrors.NewBadUsage(msg)
	}
	if wanted != nil && *wanted != p.ID {
		return nil, qerrors.NewBadUsagef("decision %s is not %s's pending decision; its pending decision is %s [d: %s]",
			wanted, groupRef(group.ID), labelOf(p), p.ID)
	}
	return p, nil
}

// governing is the group's own effective decision, else the one it carries.
func governing(group *duplicate_groups.Group) *duplicate_groups.Decision {
	if group.Decision != nil {
		return group.Decision
	}
	return group.CarriedDecision
}

// findDecision returns the decision id among the group's own, carried,
// pending and earlier decisions, nil when none is.
func findDecision(group *duplicate_groups.Group, id openapi_types.UUID) *duplicate_groups.Decision {
	for _, d := range []*duplicate_groups.Decision{group.Decision, group.CarriedDecision, group.PendingDecision} {
		if d != nil && d.ID == id {
			return d
		}
	}
	for i := range group.EarlierDecisions {
		if group.EarlierDecisions[i].ID == id {
			return &group.EarlierDecisions[i]
		}
	}
	return nil
}

// settleNote says what governs the group once its pending decision p is
// withdrawn or dismissed.
func settleNote(group *duplicate_groups.Group, p *duplicate_groups.Decision) string {
	if p.ReplacesDecisionID != nil {
		label := "decision"
		if d := findDecision(group, *p.ReplacesDecisionID); d != nil {
			label = labelOf(d)
		}
		return fmt.Sprintf(" The group keeps its effective %s [d: %s].", label, p.ReplacesDecisionID)
	}
	if eff := governing(group); eff != nil {
		return fmt.Sprintf(" The group keeps its effective %s [d: %s].", labelOf(eff), eff.ID)
	}
	return " The group returns to undecided."
}

// pendingRef is "<ref>'s pending <label> [d: <id>]".
func (v vote) pendingRef() string {
	return fmt.Sprintf("%s's pending %s [d: %s]", groupRef(v.groupID), labelOf(v.pending), v.pending.ID)
}

// approvals is "approvals c/r" of the pending decision.
func (v vote) approvals() string {
	return fmt.Sprintf("approvals %d/%d", countedApprovals(v.pending), v.pending.RequiredApprovals)
}

func runApprove(cmd *cobra.Command, args []string) error {
	comment, err := commentFlag(cmd)
	if err != nil {
		return err
	}
	v, err := readPending(cmd, args, "approve")
	if err != nil {
		return err
	}
	question := fmt.Sprintf("Approve %s on table/%d (%s)?", v.pendingRef(), v.t.tableID, v.approvals())
	if v.group.Caller.CallerApproved {
		if comment == nil {
			return writeAlreadyApproved(cmd, v)
		}
		question += " This replaces your approval's comment."
	}
	if err := confirm(cmd, "approving a duplicate group decision", question); err != nil {
		return err
	}
	result, err := v.t.client.Approve(v.t.token(), v.t.tableID, v.groupID, v.pending.ID, comment)
	if err != nil {
		return mapVoteError(err, v.t.tableID, v.groupID, false)
	}
	return writeDecide(cmd, v.t.tableID, v.groupID, "approved", result)
}

// writeAlreadyApproved reports an approval that needs no request.
func writeAlreadyApproved(cmd *cobra.Command, v vote) error {
	out := cmdutil.NewResultWriter(cmd)
	fmt.Fprintf(out.Human, "%s already has your approval on the pending %s [d: %s] (%s)\n",
		groupRef(v.groupID), labelOf(v.pending), v.pending.ID, v.approvals())
	if format, _ := cmd.Flags().GetString("output"); output.Format(format) == output.FormatName {
		return out.Write(nil, groupRef(v.groupID))
	}
	return nil
}

func runDisagree(cmd *cobra.Command, args []string) error {
	reason, err := reasonFlag(cmd)
	if err != nil {
		return err
	}
	v, err := readPending(cmd, args, "disagree with")
	if err != nil {
		return err
	}
	question := fmt.Sprintf("Disagree with %s on table/%d (%s)? It cannot take effect while your disagreement stands.",
		v.pendingRef(), v.t.tableID, v.approvals())
	if err := confirm(cmd, "disagreeing with a duplicate group decision", question); err != nil {
		return err
	}
	result, err := v.t.client.Disagree(v.t.token(), v.t.tableID, v.groupID, v.pending.ID, reason)
	if err != nil {
		return mapVoteError(err, v.t.tableID, v.groupID, false)
	}
	return writeVote(cmd, v.t.tableID, v.groupID, "disagreed with", result,
		fmt.Sprintf("It cannot take effect while your disagreement stands; "+
			"'qctl duplicate-group approve %s --table-id %d' replaces it with your approval.", v.groupID, v.t.tableID))
}

func runDismiss(cmd *cobra.Command, args []string) error {
	reason, err := reasonFlag(cmd)
	if err != nil {
		return err
	}
	v, err := readPending(cmd, args, "dismiss")
	if err != nil {
		return err
	}
	question := fmt.Sprintf("Dismiss %s proposed by %s on table/%d (%s)?%s",
		v.pendingRef(), v.pending.ProposedByID, v.t.tableID, v.approvals(), settleNote(v.group, v.pending))
	if err := confirm(cmd, "dismissing a duplicate group decision", question); err != nil {
		return err
	}
	result, err := v.t.client.Dismiss(v.t.token(), v.t.tableID, v.groupID, v.pending.ID, reason)
	if err != nil {
		return mapVoteError(err, v.t.tableID, v.groupID, true)
	}
	return writeVote(cmd, v.t.tableID, v.groupID, "dismissed", result, "")
}

// writeVote prints a vote's result, then extra when it is not "".
func writeVote(cmd *cobra.Command, tableID int, groupID openapi_types.UUID, verb string,
	result *duplicate_groups.DecisionResult, extra string) error {
	out := cmdutil.NewResultWriter(cmd)
	writeDecisionResult(out.Human, groupID, verb, result)
	if extra != "" {
		fmt.Fprintln(out.Human, extra)
	}
	writeNext(out.Human, tableID, groupID)
	warnUnapplied(cmd, &result.Apply)
	return out.Write(result, groupRef(groupID))
}
