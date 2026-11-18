package duplicate_groups //nolint:revive // Matches the backend resource name.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// Ref is a group's kind/name reference.
func Ref(id openapi_types.UUID) string { return "duplicate-group/" + id.String() }

// KindLabel names a decision by what it decides.
func KindLabel(kind DecisionKind, count, keptMember *int) string {
	switch kind {
	case KindAllow:
		if count != nil {
			return fmt.Sprintf("allow %d", *count)
		}
		return "allow"
	case KindKeepOne:
		if keptMember != nil {
			return fmt.Sprintf("keep-one member %d", *keptMember)
		}
		return "keep-one"
	case KindNotDuplicates:
		return "not-duplicates"
	case KindStopSeries:
		return "stop-series"
	}
	return terminal.Sanitize(string(kind))
}

// DecisionLabel is KindLabel of d.
func DecisionLabel(d *Decision) string {
	return KindLabel(d.Kind, d.AllowedCount, d.KeptMemberID)
}

// CountedApprovals counts the approvals that count toward d.
func CountedApprovals(d *Decision) int {
	n := 0
	for _, a := range d.Approvals {
		if a.Counted {
			n++
		}
	}
	return n
}

// CountedDisagreements counts the disagreements that hold d.
func CountedDisagreements(d *Decision) int {
	n := 0
	for _, x := range d.Disagreements {
		if x.Counted {
			n++
		}
	}
	return n
}

// DecisionColumn is the one-cell summary of the decision in effect: the
// group's own, else the one it carries, else "-".
func DecisionColumn(own, carried *Decision) string {
	switch {
	case own != nil:
		return DecisionLabel(own)
	case carried != nil:
		s := "carried " + DecisionLabel(carried)
		if carried.PeriodKey != nil && *carried.PeriodKey != "" {
			s += " (" + terminal.Sanitize(*carried.PeriodKey) + ")"
		}
		return s
	}
	return "-"
}

// PendingColumn is the one-cell summary of a group's pending decision with
// its counted approvals, or "-".
func PendingColumn(p *Decision) string {
	if p == nil {
		return "-"
	}
	s := fmt.Sprintf("%s %d/%d", DecisionLabel(p), CountedApprovals(p), p.RequiredApprovals)
	if p.ReplacesDecisionID != nil && p.Kind != KindStopSeries {
		s += " change"
	}
	if k := CountedDisagreements(p); k > 0 {
		s += fmt.Sprintf(" %dx disagree", k)
	}
	return s
}

// CanDecideColumn is "yes", or "no (<reason>)".
func CanDecideColumn(c *CallerPermissions) string {
	reason := ""
	if c.Reason != nil {
		reason = terminal.Sanitize(string(*c.Reason))
	}
	return abilityText(c.CanDecide, reason)
}

// ResponsesColumn lists the kinds of a group's live sender responses, or "-".
func ResponsesColumn(responses []SenderResponse) string {
	if len(responses) == 0 {
		return "-"
	}
	kinds := make([]string, 0, len(responses))
	seen := map[string]bool{}
	for _, r := range responses {
		k := terminal.Sanitize(string(r.Kind))
		if !seen[k] {
			seen[k] = true
			kinds = append(kinds, k)
		}
	}
	return strings.Join(kinds, ",")
}

// KeyColumn is a repeated_key group's key as col=value pairs in column
// order, a person_marked group's hold mode, or "-".
func KeyColumn(origin Origin, keyValues *map[string]interface{}, holdMode *HoldMode) string {
	if keyValues != nil && len(*keyValues) > 0 {
		cols := make([]string, 0, len(*keyValues))
		for col := range *keyValues {
			cols = append(cols, col)
		}
		sort.Strings(cols)
		pairs := make([]string, 0, len(cols))
		for _, col := range cols {
			pairs = append(pairs, terminal.Sanitize(col)+"="+cellText((*keyValues)[col]))
		}
		return strings.Join(pairs, ", ")
	}
	if origin == OriginPersonMarked && holdMode != nil {
		return "marked (" + terminal.Sanitize(string(*holdMode)) + ")"
	}
	return "-"
}

// cellText renders a cell value as one sanitized line; null is "null".
func cellText(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return terminal.Sanitize(strings.Join(strings.Fields(x), " "))
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return terminal.Sanitize(fmt.Sprint(v))
	}
	return terminal.Sanitize(string(raw))
}

// RenderDetail is the describe duplicate-group text payload for group g of
// table tableID.
func RenderDetail(tableID int, g *Group) string {
	var b strings.Builder
	renderHeader(&b, tableID, g)
	renderUnappliedPeriods(&b, g)
	renderKey(&b, g)
	renderDecision(&b, "DECISION", g.Decision)
	renderDecision(&b, "PENDING DECISION", g.PendingDecision)
	renderDecision(&b, "CARRIED DECISION", g.CarriedDecision)
	renderMembers(&b, g)
	renderMemberValues(&b, g.Members)
	renderDiffering(&b, g.DifferingCells)
	renderResponses(&b, "RESPONSES", g.Responses)
	renderEarlier(&b, g)
	renderResponses(&b, "EARLIER RESPONSES", g.EarlierResponses)
	renderDiscussion(&b, tableID, g)
	renderCaller(&b, tableID, g)
	b.WriteString(NextActions(tableID, g))
	return b.String()
}

func stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339) + " (" + timeutil.FormatRelative(t) + ")"
}

func uuidText(id *openapi_types.UUID) string {
	if id == nil {
		return "-"
	}
	return id.String()
}

func renderHeader(b *strings.Builder, tableID int, g *Group) {
	fmt.Fprintf(b, "%s (table/%d)\n", Ref(g.ID), tableID)
	fmt.Fprintf(b, "Origin:    %s\n", terminal.Sanitize(string(g.Origin)))
	state := terminal.Sanitize(string(g.State))
	if g.AwaitingApproval {
		state += " (awaiting approval)"
	}
	fmt.Fprintf(b, "State:     %s\n", state)
	fmt.Fprintf(b, "Held:      %d row(s)\n", g.HeldCount)
	if g.StoredItemID != nil {
		fmt.Fprintf(b, "File:      file/%d\n", *g.StoredItemID)
	}
	if g.DataSourceModelID != nil {
		fmt.Fprintf(b, "Source:    cloud-source/%d\n", *g.DataSourceModelID)
	}
	fmt.Fprintf(b, "Created:   %s\n", stamp(g.CreatedAt))
	if g.EndedAt != nil {
		reason := "-"
		if g.EndReason != nil {
			reason = terminal.Sanitize(string(*g.EndReason))
		}
		fmt.Fprintf(b, "Ended:     %s, %s, by %s\n", stamp(*g.EndedAt), reason, uuidText(g.EndedByID))
	}
	if g.Unapplied {
		b.WriteString("Unapplied: yes — the decision has not been fully applied\n")
	}
	if g.AwaitingCorrectedFile {
		b.WriteString("A sender said a corrected file will follow.\n")
	}
}

func renderUnappliedPeriods(b *strings.Builder, g *Group) {
	if len(g.UnappliedPeriods) == 0 {
		return
	}
	b.WriteString("\nUNAPPLIED PERIODS\n")
	for _, p := range g.UnappliedPeriods {
		fmt.Fprintf(b, "  table/%d %s", p.DatasetID, terminal.Sanitize(string(p.Outcome)))
		writeQuoted(b, p.Reason)
	}
}

func renderKey(b *strings.Builder, g *Group) {
	switch g.Origin {
	case OriginRepeatedKey:
		b.WriteString("\nKEY\n")
		if g.KeyValues == nil || len(*g.KeyValues) == 0 {
			b.WriteString("  (no live row to read the key from)\n")
		} else {
			fmt.Fprintf(b, "  %s\n", KeyColumn(g.Origin, g.KeyValues, nil))
		}
	case OriginPersonMarked:
		b.WriteString("\nMARKED\n")
		mode := "-"
		if g.HoldMode != nil {
			mode = terminal.Sanitize(string(*g.HoldMode))
		}
		fmt.Fprintf(b, "  by %s, mode %s\n", uuidText(g.CreatedByID), mode)
		if g.Comment != nil && *g.Comment != "" {
			fmt.Fprintf(b, "  %q\n", terminal.Sanitize(*g.Comment))
		}
	}
	if g.Identity != nil && *g.Identity != "" {
		fmt.Fprintf(b, "  identity: %s\n", terminal.Sanitize(*g.Identity))
	}
	if g.FileKey != "" {
		fmt.Fprintf(b, "  file key: %s\n", terminal.Sanitize(g.FileKey))
	}
	if g.MaskedFields != nil && len(*g.MaskedFields) > 0 {
		masked := make([]string, 0, len(*g.MaskedFields))
		for _, f := range *g.MaskedFields {
			masked = append(masked, terminal.Sanitize(f))
		}
		fmt.Fprintf(b, "  masked for you: %s\n", strings.Join(masked, ", "))
	}
}

func renderDecision(b *strings.Builder, title string, d *Decision) {
	if d == nil {
		return
	}
	fmt.Fprintf(b, "\n%s\n", title)
	writeDecision(b, d)
}

func writeDecision(b *strings.Builder, d *Decision) {
	fmt.Fprintf(b, "  %s [d: %s] %s, approvals %d/%d, scope %s%s\n", DecisionLabel(d), d.ID,
		terminal.Sanitize(string(d.State)), CountedApprovals(d), d.RequiredApprovals,
		terminal.Sanitize(string(d.Scope)), periodText(d.PeriodKey))
	fmt.Fprintf(b, "    proposed by %s at %s\n", d.ProposedByID, stamp(d.CreatedAt))
	if d.ReplacesDecisionID != nil {
		what := "replaces"
		if d.Kind == KindStopSeries {
			what = "stops the series allow"
		}
		fmt.Fprintf(b, "    %s [d: %s]\n", what, d.ReplacesDecisionID)
	}
	if d.Reason != nil && *d.Reason != "" {
		fmt.Fprintf(b, "    reason: %q\n", terminal.Sanitize(*d.Reason))
	}
	if d.Comment != nil && *d.Comment != "" {
		fmt.Fprintf(b, "    comment: %q\n", terminal.Sanitize(*d.Comment))
	}
	if d.EffectiveAt != nil {
		fmt.Fprintf(b, "    effective at %s\n", stamp(*d.EffectiveAt))
	}
	writeDecisionEnd(b, d)
	writeVotes(b, d)
	if len(d.DesignatedReviewerIds) > 0 {
		ids := make([]string, 0, len(d.DesignatedReviewerIds))
		for _, id := range d.DesignatedReviewerIds {
			ids = append(ids, id.String())
		}
		fmt.Fprintf(b, "    designated reviewers: %s\n", strings.Join(ids, ", "))
	}
}

// writeDecisionEnd writes how d stopped being live: superseded, withdrawn or
// dismissed.
func writeDecisionEnd(b *strings.Builder, d *Decision) {
	if d.SupersededAt != nil {
		fmt.Fprintf(b, "    superseded at %s", stamp(*d.SupersededAt))
		if d.SupersededByDecisionID != nil {
			fmt.Fprintf(b, " by [d: %s]", d.SupersededByDecisionID)
		}
		if d.SupersededByID != nil {
			fmt.Fprintf(b, " (proposed by %s)", d.SupersededByID)
		}
		b.WriteString("\n")
	}
	if d.WithdrawnAt != nil {
		if d.WithdrawReason != nil {
			fmt.Fprintf(b, "    withdrawn at %s by the system (%s)", stamp(*d.WithdrawnAt),
				terminal.Sanitize(string(*d.WithdrawReason)))
		} else {
			fmt.Fprintf(b, "    withdrawn at %s by %s", stamp(*d.WithdrawnAt), uuidText(d.WithdrawnByID))
		}
		writeQuoted(b, d.WithdrawComment)
	}
	if d.DismissedAt != nil {
		fmt.Fprintf(b, "    dismissed at %s by %s", stamp(*d.DismissedAt), uuidText(d.DismissedByID))
		writeQuoted(b, d.DismissReason)
	}
}

// writeQuoted ends a line, with ": <quoted s>" when s is set.
func writeQuoted(b *strings.Builder, s *string) {
	if s != nil && *s != "" {
		fmt.Fprintf(b, ": %q", terminal.Sanitize(*s))
	}
	b.WriteString("\n")
}

// writeVotes writes d's approvals and disagreements.
func writeVotes(b *strings.Builder, d *Decision) {
	for _, a := range d.Approvals {
		fmt.Fprintf(b, "    approved by %s at %s (%s)", a.ReviewerID, stamp(a.ApprovedAt), countedText(a.Counted))
		writeQuoted(b, a.Comment)
	}
	for _, x := range d.Disagreements {
		fmt.Fprintf(b, "    disagreed by %s at %s (%s)", x.ReviewerID, stamp(x.DisagreedAt),
			countedText(x.Counted))
		writeQuoted(b, x.Reason)
	}
}

func countedText(counted bool) string {
	if counted {
		return "counted"
	}
	return "not counted"
}

func periodText(p *string) string {
	if p == nil || *p == "" {
		return ""
	}
	return " (from period " + terminal.Sanitize(*p) + ")"
}

func renderMembers(b *strings.Builder, g *Group) {
	fmt.Fprintf(b, "\nMEMBERS (%d)\n", len(g.Members))
	if len(g.Members) == 0 {
		return
	}
	w := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  MEMBER\tROW\tSOURCE\tLOCATION\tRANK\tOUTCOME\tIDENTICAL-TO\tDELETED")
	for _, m := range g.Members {
		rank, outcome, identical, deleted := "-", "-", "-", "-"
		if m.Rank != nil {
			rank = fmt.Sprint(*m.Rank)
		}
		if m.Outcome != nil {
			outcome = terminal.Sanitize(string(*m.Outcome))
		}
		if len(m.IdenticalAtLoadTo) > 0 {
			ids := make([]string, 0, len(m.IdenticalAtLoadTo))
			for _, id := range m.IdenticalAtLoadTo {
				ids = append(ids, fmt.Sprint(id))
			}
			identical = strings.Join(ids, ",")
		}
		if m.DeletedAt != nil {
			deleted = timeutil.FormatRelative(*m.DeletedAt)
		}
		fmt.Fprintf(w, "  %d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.RowID, m.DataSourceModelID,
			terminal.Sanitize(string(m.Location)), rank, outcome, identical, deleted)
	}
	_ = w.Flush()
}

func renderMemberValues(b *strings.Builder, members []GroupMember) {
	if len(members) == 0 {
		return
	}
	b.WriteString("\nMEMBER VALUES\n")
	for _, m := range members {
		fmt.Fprintf(b, "  member %d (row %d):\n", m.ID, m.RowID)
		if m.Values == nil {
			b.WriteString("    (unavailable)\n")
			continue
		}
		if len(*m.Values) == 0 {
			b.WriteString("    (no values)\n")
			continue
		}
		fields := make([]string, 0, len(*m.Values))
		for field := range *m.Values {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			fmt.Fprintf(b, "    %s: %s\n", cellText(field), cellText((*m.Values)[field]))
		}
	}
}

func renderDiffering(b *strings.Builder, cells []DifferingCell) {
	if len(cells) == 0 {
		return
	}
	fmt.Fprintf(b, "\nDIFFERING CELLS (%d)\n", len(cells))
	for _, c := range cells {
		parts := make([]string, 0, len(c.Values))
		for _, v := range c.Values {
			parts = append(parts, fmt.Sprintf("member %d=%s", v.MemberID, cellText(v.Value)))
		}
		fmt.Fprintf(b, "  %s: %s\n", terminal.Sanitize(c.Column), strings.Join(parts, "; "))
	}
}

func renderResponses(b *strings.Builder, title string, responses []SenderResponse) {
	if len(responses) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s (%d)\n", title, len(responses))
	for _, r := range responses {
		who := terminal.Sanitize(r.AuthorName)
		if r.AuthorIsExternal {
			who += " (external)"
		}
		fmt.Fprintf(b, "  %s from cloud-source/%d by %s at %s\n", terminal.Sanitize(string(r.Kind)),
			r.DataSourceModelID, who, stamp(r.CreatedAt))
		if r.Comment != "" {
			fmt.Fprintf(b, "    %q\n", terminal.Sanitize(r.Comment))
		}
		if r.EndedAt != nil {
			reason := "-"
			if r.EndReason != nil {
				reason = terminal.Sanitize(string(*r.EndReason))
			}
			fmt.Fprintf(b, "    ended at %s (%s)\n", stamp(*r.EndedAt), reason)
		}
	}
}

func renderEarlier(b *strings.Builder, g *Group) {
	if len(g.EarlierDecisions) == 0 {
		return
	}
	fmt.Fprintf(b, "\nEARLIER DECISIONS (%d)\n", len(g.EarlierDecisions))
	for i := range g.EarlierDecisions {
		writeDecision(b, &g.EarlierDecisions[i])
	}
}

func renderDiscussion(b *strings.Builder, tableID int, g *Group) {
	b.WriteString("\nDISCUSSION\n")
	if g.CommentThreadID == nil {
		b.WriteString("  none yet\n")
		return
	}
	switch {
	case g.SharedAt != nil:
		by := "-"
		if g.SharedByName != nil && *g.SharedByName != "" {
			by = terminal.Sanitize(*g.SharedByName)
		}
		fmt.Fprintf(b, "  thread %s, shared with the data source by %s at %s\n", g.CommentThreadID, by, stamp(*g.SharedAt))
	case g.CommentThreadIsInternal != nil && *g.CommentThreadIsInternal:
		fmt.Fprintf(b, "  thread %s, internal\n", g.CommentThreadID)
	default:
		fmt.Fprintf(b, "  thread %s\n", g.CommentThreadID)
	}
	fmt.Fprintf(b, "  read it: qctl describe comment-thread %s --table-id %d\n", g.CommentThreadID, tableID)
}

func renderCaller(b *strings.Builder, tableID int, g *Group) {
	c := &g.Caller
	b.WriteString("\nYOU\n")
	ability := func(what string, ok bool, reason string) {
		fmt.Fprintf(b, "  %s: %s\n", what, abilityText(ok, reason))
	}
	ability("can decide", c.CanDecide, refusalText(c.Reason, tableID))
	if g.PendingDecision != nil {
		ability("can approve the pending decision", c.CanApprove, refusalText(c.ApproveReason, tableID))
		fmt.Fprintf(b, "  approved it: %s\n", yesNo(c.CallerApproved))
		ability("can disagree", c.CanDisagree, refusalText(c.DisagreeReason, tableID))
		ability("can withdraw it", c.CanWithdraw, refusalText(c.WithdrawReason, tableID))
		ability("can dismiss it", c.CanDismiss, refusalText(c.DismissReason, tableID))
	}
	if g.Decision != nil || g.CarriedDecision != nil {
		ability("can propose a change", c.CanChange, refusalText(c.ChangeReason, tableID))
	}
	if c.StopForSeriesDecisionID != nil {
		ability(fmt.Sprintf("can stop the series allow [d: %s]", c.StopForSeriesDecisionID),
			c.CanStopForSeries, stopRefusalText(c.StopForSeriesReason, tableID))
	}
	if g.Origin == OriginPersonMarked {
		ability("can retract the mark", c.CanRetract, retractRefusalText(c.RetractReason))
	}
}

// abilityText is "yes", or "no" with the refusal when there is one.
func abilityText(ok bool, refusal string) string {
	if ok || refusal == "" {
		return yesNo(ok)
	}
	return "no (" + refusal + ")"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// NextActions is the NEXT ACTIONS block for g, from its state and what the
// caller may do; "" for an ended group with no discussion.
func NextActions(tableID int, g *Group) string {
	var lines []string
	add := func(format string, args ...interface{}) { lines = append(lines, fmt.Sprintf(format, args...)) }
	sel := fmt.Sprintf("%s --table-id %d", g.ID, tableID)
	if g.State != GroupStateEnded {
		lines = append(lines, reviewLines(tableID, g, sel)...)
	}
	if g.Origin == OriginPersonMarked && g.Caller.CanRetract {
		add("  qctl duplicate-group retract %s   # take the mark back: the group ends", sel)
	}
	if g.Unapplied && g.Caller.CanDecide {
		add("  qctl duplicate-group reapply --table-id %d   # retry unapplied decisions", tableID)
	}
	if g.State != GroupStateEnded || g.CommentThreadID != nil {
		add("  qctl duplicate-group comment %s --body <text>", sel)
	}
	if g.CommentThreadID != nil && g.SharedAt == nil && g.Caller.CanDecide {
		add("  qctl duplicate-group share %s   # irreversible: the data source reads the discussion", sel)
	}
	if len(lines) == 0 {
		return ""
	}
	return "\nNEXT ACTIONS\n" + strings.Join(lines, "\n") + "\n"
}

// reviewLines are the reviewer's lines for a live group.
func reviewLines(tableID int, g *Group, sel string) []string {
	var lines []string
	if g.PendingDecision != nil {
		lines = pendingLines(tableID, g, sel)
	} else {
		lines = decideLines(tableID, g, sel)
	}
	c := &g.Caller
	if c.CanStopForSeries && c.StopForSeriesDecisionID != nil {
		lines = append(lines, fmt.Sprintf("  qctl duplicate-group stop-series %s   # stop the series allow [d: %s] in every period",
			sel, c.StopForSeriesDecisionID))
	}
	return lines
}

// pendingLines are the lines for g's pending decision.
func pendingLines(tableID int, g *Group, sel string) []string {
	c, p := &g.Caller, g.PendingDecision
	label := DecisionLabel(p)
	var lines []string
	switch {
	case c.CallerApproved:
		s := fmt.Sprintf("  you approved the pending %s; waiting for %d more approval(s)",
			label, max(p.RequiredApprovals-CountedApprovals(p), 0))
		if k := CountedDisagreements(p); k > 0 {
			s += fmt.Sprintf("; held by %d disagreement(s)", k)
		}
		lines = append(lines, s)
	case c.CanApprove:
		lines = append(lines, fmt.Sprintf("  qctl duplicate-group approve %s   # approve the pending %s (%d/%d)",
			sel, label, CountedApprovals(p), p.RequiredApprovals))
	default:
		lines = append(lines, fmt.Sprintf("  you may not approve the pending %s: %s", label,
			refusalText(c.ApproveReason, tableID)))
	}
	if c.CanDisagree {
		lines = append(lines, fmt.Sprintf("  qctl duplicate-group disagree %s --reason <text>   # hold the pending %s",
			sel, label))
	}
	if c.CanWithdraw {
		lines = append(lines, fmt.Sprintf("  qctl duplicate-group withdraw %s   # take back your pending %s", sel, label))
		lines = append(lines, decisionVerbLines(g.Origin, sel, "or replace it")...)
	}
	if c.CanDismiss {
		lines = append(lines, fmt.Sprintf("  qctl duplicate-group dismiss %s --reason <text>   # admin/ops: drop the pending %s",
			sel, label))
	}
	return lines
}

// decideLines are the decide lines for a live group with nothing pending:
// a decision, or a change to the one in effect.
func decideLines(tableID int, g *Group, sel string) []string {
	c := &g.Caller
	if g.Decision == nil && g.CarriedDecision == nil {
		if !c.CanDecide {
			return []string{"  you may not decide this group: " + refusalText(c.Reason, tableID)}
		}
		return decisionVerbLines(g.Origin, sel, "decide")
	}
	if !c.CanChange {
		return []string{fmt.Sprintf("  you may not propose a change to the effective %s: %s",
			effectiveLabel(g), refusalText(c.ChangeReason, tableID))}
	}
	return decisionVerbLines(g.Origin, sel, "propose a change")
}

// effectiveLabel names the decision in effect on g.
func effectiveLabel(g *Group) string {
	if g.Decision != nil {
		return DecisionLabel(g.Decision)
	}
	return "carried " + DecisionLabel(g.CarriedDecision)
}

// decisionVerbLines are the decide lines an origin offers.
func decisionVerbLines(origin Origin, sel, verb string) []string {
	switch origin {
	case OriginRepeatedKey:
		return []string{fmt.Sprintf("  qctl duplicate-group allow %s [--count N]   # %s: allow the copies", sel, verb),
			fmt.Sprintf("  qctl duplicate-group keep-one %s --member <member-id>", sel)}
	case OriginIdenticalRows:
		return []string{fmt.Sprintf("  qctl duplicate-group allow %s --count N   # %s: allow N identical copies", sel, verb),
			fmt.Sprintf("  qctl duplicate-group keep-one %s", sel)}
	case OriginPersonMarked:
		return []string{fmt.Sprintf("  qctl duplicate-group allow %s   # %s: keep every marked row", sel, verb),
			fmt.Sprintf("  qctl duplicate-group keep-one %s --member <member-id>", sel),
			fmt.Sprintf("  qctl duplicate-group not-duplicates %s --reason <text>", sel)}
	}
	return nil
}

// refusals explains each refusal but not_designated, which names the table.
var refusals = map[DecideRefusal]string{
	"not_permitted":    "it needs DATA_REVIEW_APPROVE (admin, member, ops) on the table",
	"policy_denied":    "the table's change review policy refuses data_review_approve to you",
	"excluded":         "you edited or requested review of its rows, or marked it; another reviewer must",
	"period_locked":    "the table's period is locked",
	"ended":            "the group has ended",
	"not_pending":      "the group has no pending decision",
	"proposer":         "you proposed it; withdraw it instead",
	"not_proposer":     "another reviewer proposed it; only they may withdraw it",
	"not_admin":        "only an admin or ops user may dismiss",
	"not_series_allow": "no effective series allow it may stop governs the group",
	"pending_exists":   "another reviewer's decision is pending; approve or disagree with it",
	"no_decision":      "no decision governs the group",
}

// refusalText explains a refusal of the caller's; "" for none.
func refusalText(reason *DecideRefusal, tableID int) string {
	if reason == nil {
		return ""
	}
	if *reason == "not_designated" {
		return fmt.Sprintf("the data review policy names who may; see qctl get data-review-policy --table-id %d", tableID)
	}
	if text, ok := refusals[*reason]; ok {
		return text
	}
	return terminal.Sanitize(string(*reason))
}

// stopRefusalText is refusalText for stopping a series allow, which any
// pending decision on the group refuses.
func stopRefusalText(reason *DecideRefusal, tableID int) string {
	if reason != nil && *reason == "pending_exists" {
		return "the group has a pending decision; it must take effect or be withdrawn or dismissed first"
	}
	return refusalText(reason, tableID)
}

// retractRefusals explains each retract refusal.
var retractRefusals = map[RetractRefusal]string{
	"not_permitted":     "retracting needs SUBMIT_ALERT, an internal role and access to the table",
	"policy_denied":     "the table's change review policy refuses user_alert_retract to you",
	"period_locked":     "the table's period is locked",
	"ended":             "the group has ended",
	"decided":           "its own decision is effective",
	"not_marker":        "only the user who marked the group may retract it",
	"not_person_marked": "only a person_marked group can be retracted",
}

// retractRefusalText explains a retract refusal; "" for none.
func retractRefusalText(reason *RetractRefusal) string {
	if reason == nil {
		return ""
	}
	if text, ok := retractRefusals[*reason]; ok {
		return text
	}
	return terminal.Sanitize(string(*reason))
}
