package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"sort"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const decisionCommentUsage = "Comment recorded with the decision"

// refusalNotProposer is the withdraw refusal of a pending decision another
// reviewer proposed.
const refusalNotProposer duplicate_groups.DecideRefusal = "not_proposer"

const decideRulesNote = `Sending the decision the group's pending decision already is approves it. A
different one replaces your own pending decision and restarts approvals;
while another reviewer's decision is pending, the server refuses it (409
DUPLICATE_DECISION_PENDING): approve, disagree with or (admin, ops) dismiss
that one instead.

On a group a decision already governs, its own effective decision or one it
carries from another group, the decision proposes a change: it replaces the
governing decision once it takes effect, and the governing decision stays in
force until then. The prompt says which case applies, and the result line
says whether the decision was decided (effective), proposed (pending) or
approved (the pending one).`

// decisionAnnotations annotates a verb that honors every -o value.
func decisionAnnotations() map[string]string {
	return catalog.For(resource)
}

func newAllowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "allow <group-id>",
		Short: "Allow a duplicate group's copies",
		Long: `Propose, or approve, an allow on a duplicate group: its copies load.

  repeated_key    --count is how many copies the key may have (at least 2);
                  without it, every current copy. On a table in a series, an
                  allow on a group no decision governs also covers the key in
                  later uploads and periods.
  identical_rows  --count is required: how many identical copies may load
                  (at least 1).
  person_marked   takes no --count: every marked row stays.

` + decideRulesNote + `

qctl reads the group first to check --count against its origin. Prompts
unless --yes. -o json|yaml prints the server's {decision, state, apply} and
moves the result lines to stderr; -o name prints duplicate-group/<uuid>.`,
		Example: `  qctl duplicate-group allow 0190aaaa-1111-4222-8333-444444444444 --table orders --yes
  qctl duplicate-group allow 0190aaaa-1111-4222-8333-444444444444 --table-id 42 --count 2 --comment "two shipments" --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDecide(cmd, args, duplicate_groups.KindAllow)
		},
	}
	addCommonFlags(cmd, decisionCommentUsage)
	cmd.Flags().Int("count", 0, "Copies to allow (repeated_key: at least 2, default every current copy; identical_rows: required, at least 1)")
	return cmd
}

func newKeepOneCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keep-one <group-id>",
		Short: "Keep one member of a duplicate group and reject the rest",
		Long: `Propose, or approve, a keep-one decision: one member's row loads and the
others are rejected.

repeated_key and person_marked groups name the member to keep with --member
<member-id> (a member id, not a row id); it must be an active member of the
group. An identical_rows group takes no --member: its copies are identical,
so the server records keep-one there as an allow of 1.

` + decideRulesNote + `

Prompts unless --yes. -o json|yaml prints the server's {decision, state,
apply}; -o name prints duplicate-group/<uuid>.`,
		Example:     `  qctl duplicate-group keep-one 0190aaaa-1111-4222-8333-444444444444 --table orders --member 812 --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDecide(cmd, args, duplicate_groups.KindKeepOne)
		},
	}
	addCommonFlags(cmd, decisionCommentUsage)
	cmd.Flags().Int("member", 0, "Member id to keep (repeated_key and person_marked groups)")
	return cmd
}

func newNotDuplicatesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "not-duplicates <group-id>",
		Short: "Say a person-marked group's rows are not duplicates",
		Long: `Propose, or approve, a not-duplicates decision on a person_marked group:
its rows are not duplicates of one another and all of them load. --reason is
required and must not be blank. Other origins take allow or keep-one.

` + decideRulesNote + `

Prompts unless --yes. -o json|yaml prints the server's {decision, state,
apply}; -o name prints duplicate-group/<uuid>.`,
		Example:     `  qctl duplicate-group not-duplicates 0190aaaa-1111-4222-8333-444444444444 --table orders --reason "different customers" --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDecide(cmd, args, duplicate_groups.KindNotDuplicates)
		},
	}
	addCommonFlags(cmd, decisionCommentUsage)
	cmd.Flags().String("reason", "", "Why the rows are not duplicates (required)")
	return cmd
}

// decideFlags are a decision verb's flags, checked before any request.
type decideFlags struct {
	count   *int
	member  *int
	reason  *string
	comment *string
}

func readDecideFlags(cmd *cobra.Command, kind duplicate_groups.DecisionKind) (decideFlags, error) {
	var f decideFlags
	comment, err := commentFlag(cmd)
	if err != nil {
		return f, err
	}
	f.comment = comment
	switch kind {
	case duplicate_groups.KindAllow:
		if cmd.Flags().Changed("count") {
			n, _ := cmd.Flags().GetInt("count")
			if n < 1 {
				return f, qerrors.NewBadUsage("--count must be at least 1")
			}
			f.count = &n
		}
	case duplicate_groups.KindKeepOne:
		if cmd.Flags().Changed("member") {
			n, _ := cmd.Flags().GetInt("member")
			if n < 1 {
				return f, qerrors.NewBadUsage("--member must be a positive member id")
			}
			f.member = &n
		}
	case duplicate_groups.KindNotDuplicates:
		reason, _ := cmd.Flags().GetString("reason")
		if strings.TrimSpace(reason) == "" {
			return f, qerrors.NewBadUsage("--reason is required and must not be blank")
		}
		f.reason = &reason
	}
	return f, nil
}

func runDecide(cmd *cobra.Command, args []string, kind duplicate_groups.DecisionKind) error {
	groupID, err := preflight(cmd, args)
	if err != nil {
		return err
	}
	flags, err := readDecideFlags(cmd, kind)
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
	if err := checkOrigin(group, kind, flags); err != nil {
		return err
	}
	body := duplicate_groups.DecisionRequest{
		Kind: kind, AllowedCount: flags.count, KeptMemberID: flags.member, Reason: flags.reason, Comment: flags.comment,
	}
	question := fmt.Sprintf("Decide %s %s on table/%d?%s", groupRef(groupID),
		decisionLabel(kind, flags.count, flags.member), t.tableID, pendingNote(group, body))
	if err := confirm(cmd, "deciding a duplicate group", question); err != nil {
		return err
	}
	result, err := t.client.Decide(t.token(), t.tableID, groupID, body)
	if err != nil {
		return mapError(err, t.tableID, groupID)
	}
	return writeDecide(cmd, t.tableID, groupID, decideVerb(group, &result.Decision), result,
		replacesLine(group, &result.Decision)...)
}

// decideVerb names what a decision verb did: approved the pending decision
// read before it, decided (effective) or proposed (pending).
func decideVerb(group *duplicate_groups.Group, d *duplicate_groups.Decision) string {
	switch {
	case group.PendingDecision != nil && group.PendingDecision.ID == d.ID:
		return "approved"
	case d.State == duplicate_groups.StateEffective:
		return "decided"
	}
	return "proposed"
}

// replacesLine says what d does to the decision it replaces, labelled from
// the group read before it; none when d replaces none or is neither pending
// nor effective.
func replacesLine(group *duplicate_groups.Group, d *duplicate_groups.Decision) []string {
	id := d.ReplacesDecisionID
	if id == nil {
		return nil
	}
	label := "decision"
	replaced := findDecision(group, *id)
	if replaced != nil {
		label = labelOf(replaced)
	}
	switch d.State {
	case duplicate_groups.StatePending:
		return []string{fmt.Sprintf("replaces [d: %s] (%s), which stays in force until this takes effect", id, label)}
	case duplicate_groups.StateEffective:
		if own := group.Decision; own != nil && own.ID == *id && own.Scope != duplicate_groups.ScopeSeries {
			return []string{fmt.Sprintf("superseded [d: %s] (%s)", id, label)}
		}
		return []string{fmt.Sprintf("replaces [d: %s] (%s) on this group; it stays in force wherever else it governs", id, label)}
	}
	return nil
}

// checkOrigin applies the origin's rules for kind to the flags; a breach
// exits 2.
func checkOrigin(group *duplicate_groups.Group, kind duplicate_groups.DecisionKind, f decideFlags) error {
	origin := group.Origin
	switch kind {
	case duplicate_groups.KindAllow:
		switch origin {
		case duplicate_groups.OriginRepeatedKey:
			if f.count != nil && *f.count < 2 {
				return qerrors.NewBadUsage("a repeated_key allow needs --count of at least 2 (omit it to allow every current copy)")
			}
		case duplicate_groups.OriginIdenticalRows:
			if f.count == nil {
				return qerrors.NewBadUsage("an identical_rows allow needs --count: how many identical copies may load (at least 1)")
			}
		case duplicate_groups.OriginPersonMarked:
			if f.count != nil {
				return qerrors.NewBadUsage("a person_marked allow takes no --count: every marked row stays")
			}
		}
	case duplicate_groups.KindKeepOne:
		if origin == duplicate_groups.OriginIdenticalRows {
			if f.member != nil {
				return qerrors.NewBadUsage("an identical_rows group takes no --member: its copies are identical, so keep-one keeps any one")
			}
			return nil
		}
		return checkMember(group, f.member)
	case duplicate_groups.KindNotDuplicates:
		if origin != duplicate_groups.OriginPersonMarked {
			return qerrors.NewBadUsagef("not-duplicates decides only a person_marked group; this one is %s (use allow or keep-one)",
				terminal.Sanitize(string(origin)))
		}
	}
	return nil
}

// checkMember requires member to be one of the group's active members: one
// with an outcome, which the server computes only for those.
func checkMember(group *duplicate_groups.Group, member *int) error {
	var active []string
	found := false
	for _, m := range group.Members {
		if m.Outcome == nil || m.DeletedAt != nil {
			continue
		}
		active = append(active, fmt.Sprintf("%d (row %d)", m.ID, m.RowID))
		if member != nil && m.ID == *member {
			found = true
		}
	}
	sort.Strings(active)
	list := strings.Join(active, ", ")
	if list == "" {
		list = "none"
	}
	switch {
	case member == nil:
		return qerrors.NewBadUsagef("keep-one needs --member <member-id>; active members: %s", list)
	case !found:
		return qerrors.NewBadUsagef("member %d is not an active member of the group; active members: %s", *member, list)
	}
	return nil
}

// pendingNote says what sending body does on the group: approve or replace
// its pending decision, or propose a change to the decision governing it. It
// compares as the server does, after its normalization: an identical_rows
// keep-one is an allow of 1, and a repeated_key allow without a count allows
// the current copies, which only the server counts.
func pendingNote(group *duplicate_groups.Group, body duplicate_groups.DecisionRequest) string {
	p := group.PendingDecision
	if p == nil {
		return changeNote(group)
	}
	kind, count, member := body.Kind, body.AllowedCount, body.KeptMemberID
	if group.Origin == duplicate_groups.OriginIdenticalRows && kind == duplicate_groups.KindKeepOne {
		one := 1
		kind, count, member = duplicate_groups.KindAllow, &one, nil
	}
	approvals := fmt.Sprintf("approvals %d/%d", countedApprovals(p), p.RequiredApprovals)
	switch {
	case kind != p.Kind || !sameInt(member, p.KeptMemberID):
	case group.Origin == duplicate_groups.OriginRepeatedKey && kind == duplicate_groups.KindAllow && count == nil:
		return fmt.Sprintf(" If the group's current copies number the pending %s, this approves it; otherwise %s",
			labelOf(p), differsNote(group, p, approvals))
	case sameInt(count, p.AllowedCount):
		return fmt.Sprintf(" This approves the pending %s (%s).", labelOf(p), approvals)
	}
	note := differsNote(group, p, approvals)
	return " " + strings.ToUpper(note[:1]) + note[1:]
}

// differsNote says what a decision other than the pending p does, by who
// proposed p as the caller's withdraw ability reads.
func differsNote(group *duplicate_groups.Group, p *duplicate_groups.Decision, approvals string) string {
	c := group.Caller
	switch {
	case c.CanWithdraw:
		return fmt.Sprintf("this replaces your pending %s (%s) and restarts approvals.", labelOf(p), approvals)
	case c.WithdrawReason != nil && *c.WithdrawReason == refusalNotProposer:
		return fmt.Sprintf("another reviewer's %s is pending (%s), and the server refuses a different decision "+
			"(409 DUPLICATE_DECISION_PENDING): approve, disagree with or (admin, ops) dismiss it instead.",
			labelOf(p), approvals)
	}
	return fmt.Sprintf("this differs from the pending %s (%s): it replaces it when you proposed it, "+
		"and the server refuses it otherwise.", labelOf(p), approvals)
}

// changeNote says that a decision on a group a decision governs proposes a
// change to it; "" when none governs.
func changeNote(group *duplicate_groups.Group) string {
	if d := group.Decision; d != nil {
		return fmt.Sprintf(" This proposes a change to the group's effective %s [d: %s]; it stays in force "+
			"until the change takes effect.", labelOf(d), d.ID)
	}
	if d := group.CarriedDecision; d != nil {
		return fmt.Sprintf(" This proposes a change to the effective %s [d: %s] carried from another group; "+
			"it stays in force until the change takes effect.", labelOf(d), d.ID)
	}
	return ""
}

func sameInt(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// writeDecide prints a decision's result, then each of extra, its progress
// and the describe line.
func writeDecide(cmd *cobra.Command, tableID int, groupID openapi_types.UUID, verb string,
	result *duplicate_groups.DecisionResult, extra ...string) error {
	out := cmdutil.NewResultWriter(cmd)
	writeDecisionResult(out.Human, groupID, verb, result)
	for _, line := range extra {
		fmt.Fprintln(out.Human, line)
	}
	writeProgress(out.Human, tableID, groupID, &result.Decision)
	writeNext(out.Human, tableID, groupID)
	warnUnapplied(cmd, &result.Apply)
	return out.Write(result, groupRef(groupID))
}
