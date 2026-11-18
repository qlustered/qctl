package duplicate_groups //nolint:revive // Matches the backend resource name.

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/terminal"
)

// The sender states' held reasons.
const (
	HeldUndecided       HeldReason = "undecided"
	HeldMoreThanAllowed HeldReason = "more_than_allowed"
	HeldKeptLineChanged HeldReason = "kept_line_changed"
)

// SenderOutcomeHeld is a member whose row is held.
const SenderOutcomeHeld SenderOutcome = "held"

// SenderStateColumn is a sender state, with its held reason when held.
func SenderStateColumn(state SenderState, reason *HeldReason) string {
	s := terminal.Sanitize(string(state))
	if reason != nil && *reason != "" {
		s += " (" + terminal.Sanitize(string(*reason)) + ")"
	}
	return s
}

// SourceDecisionLabel names the decision governing a group as its sender
// sees it.
func SourceDecisionLabel(d *SourceDecision) string {
	switch {
	case d.KeptElsewhere:
		return "keep-one (a copy of another source)"
	case d.KeptMemberID != nil:
		return KindLabel(d.Kind, d.AllowedCount, d.KeptMemberID) + " (your copy)"
	}
	return KindLabel(d.Kind, d.AllowedCount, d.KeptMemberID)
}

// SourceDecisionColumn is the one-cell summary of the decision governing a
// group, or "-".
func SourceDecisionColumn(d *SourceDecision) string {
	if d == nil {
		return "-"
	}
	if !d.Carried {
		return SourceDecisionLabel(d)
	}
	s := "carried " + SourceDecisionLabel(d)
	if d.PeriodKey != nil && *d.PeriodKey != "" {
		s += " (" + terminal.Sanitize(*d.PeriodKey) + ")"
	}
	return s
}

// SourceResponsesColumn lists the kinds of a group's live responses, or "-".
func SourceResponsesColumn(responses []SourceResponse) string {
	kinds := make([]string, 0, len(responses))
	seen := map[string]bool{}
	for _, r := range responses {
		k := terminal.Sanitize(string(r.Kind))
		if !seen[k] {
			seen[k] = true
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return "-"
	}
	return strings.Join(kinds, ",")
}

// CanRespondColumn is "yes", or "no (<reason>)".
func CanRespondColumn(c *SourceCaller) string {
	s := yesNo(c.CanRespond)
	if !c.CanRespond && c.Reason != nil {
		s += " (" + terminal.Sanitize(string(*c.Reason)) + ")"
	}
	return s
}

// HeldCopies counts the members whose row is held.
func HeldCopies(members []SourceMember) int {
	n := 0
	for _, m := range members {
		if m.Outcome != nil && *m.Outcome == SenderOutcomeHeld {
			n++
		}
	}
	return n
}

// MemberSources is the sorted distinct cloud sources of members.
func MemberSources(members []SourceMember) []int {
	seen := map[int]bool{}
	var ids []int
	for _, m := range members {
		if !seen[m.DataSourceModelID] {
			seen[m.DataSourceModelID] = true
			ids = append(ids, m.DataSourceModelID)
		}
	}
	sort.Ints(ids)
	return ids
}

// RenderSourceDetail is the describe duplicate-group --sender text payload for
// group g of table tableID.
func RenderSourceDetail(tableID int, g *SourceGroup) string {
	var b strings.Builder
	renderSourceHeader(&b, tableID, g)
	renderSourceKey(&b, g)
	renderSourceDecision(&b, g)
	renderSourceMembers(&b, g.Members)
	renderDiffering(&b, g.DifferingCells)
	renderSourceResponses(&b, "YOUR RESPONSES", g.Responses)
	renderSourceResponses(&b, "EARLIER RESPONSES", g.EarlierResponses)
	renderSourceDiscussion(&b, tableID, g)
	b.WriteString("\nYOU\n")
	fmt.Fprintf(&b, "  can respond: %s\n", CanRespondColumn(&g.Caller))
	b.WriteString(SourceNextActions(tableID, g))
	return b.String()
}

func renderSourceHeader(b *strings.Builder, tableID int, g *SourceGroup) {
	fmt.Fprintf(b, "%s (table/%d, as your cloud sources see it)\n", Ref(g.ID), tableID)
	fmt.Fprintf(b, "Origin:    %s\n", terminal.Sanitize(string(g.Origin)))
	fmt.Fprintf(b, "State:     %s — %s\n", SenderStateColumn(g.State, g.HeldReason), senderStateText(g.State, g.HeldReason))
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
		fmt.Fprintf(b, "Ended:     %s, %s\n", stamp(*g.EndedAt), reason)
	}
	if g.AwaitingCorrectedFile {
		b.WriteString("Awaiting a corrected file: your cloud source's live response says one follows\n")
	}
}

// senderStateText explains a sender state.
func senderStateText(state SenderState, reason *HeldReason) string {
	switch state {
	case SenderHeld:
		if reason != nil {
			switch *reason {
			case HeldUndecided:
				return "the table's reviewers have not decided it"
			case HeldMoreThanAllowed:
				return "there are more copies than the decision allows"
			case HeldKeptLineChanged:
				return "the copy the decision kept is no longer found"
			}
		}
		return "copies are held"
	case SenderDecided:
		return "a decision in effect governs it and no copy is held"
	case SenderLoaded:
		return "no copy is held and no decision is needed"
	case SenderEnded:
		return "the group has ended"
	}
	return "-"
}

func renderSourceKey(b *strings.Builder, g *SourceGroup) {
	var lines []string
	if g.KeyValues != nil && len(*g.KeyValues) > 0 {
		lines = append(lines, "  "+KeyColumn(g.Origin, g.KeyValues, nil))
	}
	if g.FileKey != "" {
		lines = append(lines, "  file key: "+terminal.Sanitize(g.FileKey))
	}
	if g.MaskedFields != nil && len(*g.MaskedFields) > 0 {
		masked := make([]string, 0, len(*g.MaskedFields))
		for _, f := range *g.MaskedFields {
			masked = append(masked, terminal.Sanitize(f))
		}
		lines = append(lines, "  masked for you: "+strings.Join(masked, ", "))
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("\nKEY\n" + strings.Join(lines, "\n") + "\n")
}

func renderSourceDecision(b *strings.Builder, g *SourceGroup) {
	b.WriteString("\nDECISION\n")
	d := g.Decision
	if d == nil {
		switch g.State {
		case SenderEnded:
			b.WriteString("  none\n")
		case SenderLoaded:
			b.WriteString("  none needed: no copy is held\n")
		default:
			b.WriteString("  none in effect: the table's reviewers decide\n")
		}
		return
	}
	line := "  " + SourceDecisionLabel(d)
	if d.EffectiveAt != nil {
		line += ", effective at " + stamp(*d.EffectiveAt)
	}
	b.WriteString(line + "\n")
	if d.Carried {
		fmt.Fprintf(b, "  carried from another group of the same key%s\n", periodText(d.PeriodKey))
	}
	if len(d.Deciders) > 0 {
		names := make([]string, 0, len(d.Deciders))
		for _, n := range d.Deciders {
			names = append(names, terminal.Sanitize(n))
		}
		fmt.Fprintf(b, "  approved by %s\n", strings.Join(names, ", "))
	}
}

func renderSourceMembers(b *strings.Builder, members []SourceMember) {
	fmt.Fprintf(b, "\nYOUR COPIES (%d)\n", len(members))
	if len(members) == 0 {
		return
	}
	w := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  MEMBER\tROW\tSOURCE\tFILE\tFILE-ROW\tLOCATION\tOUTCOME")
	for _, m := range members {
		file, fileRow, outcome := "-", "-", "-"
		if m.StoredItemID != nil {
			file = fmt.Sprintf("file/%d", *m.StoredItemID)
		}
		if m.FileRowNumber != nil {
			fileRow = fmt.Sprint(*m.FileRowNumber)
		}
		if m.Outcome != nil {
			outcome = terminal.Sanitize(string(*m.Outcome))
		}
		fmt.Fprintf(w, "  %d\t%d\t%d\t%s\t%s\t%s\t%s\n", m.ID, m.RowID, m.DataSourceModelID, file, fileRow,
			terminal.Sanitize(string(m.Location)), outcome)
	}
	_ = w.Flush()
}

func renderSourceResponses(b *strings.Builder, title string, responses []SourceResponse) {
	if len(responses) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s (%d)\n", title, len(responses))
	for _, r := range responses {
		fmt.Fprintf(b, "  %s [r: %s] from cloud-source/%d by %s at %s\n", terminal.Sanitize(string(r.Kind)), r.ID,
			r.DataSourceModelID, terminal.Sanitize(r.AuthorName), stamp(r.CreatedAt))
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

func renderSourceDiscussion(b *strings.Builder, tableID int, g *SourceGroup) {
	b.WriteString("\nDISCUSSION\n")
	if g.CommentThreadID == nil {
		b.WriteString("  none shared with your cloud sources\n")
		return
	}
	fmt.Fprintf(b, "  thread %s, shared with your cloud source\n", g.CommentThreadID)
	fmt.Fprintf(b, "  read it: qctl describe comment-thread %s --table-id %d\n", g.CommentThreadID, tableID)
}

// SourceNextActions is the NEXT ACTIONS block for g as its sender sees it:
// the respond and withdraw-response commands, or why the caller may not
// respond.
func SourceNextActions(tableID int, g *SourceGroup) string {
	c := &g.Caller
	var lines []string
	if !c.CanRespond {
		lines = append(lines, "  "+respondRefusalText(c.Reason))
	} else {
		source := ""
		if ids := MemberSources(g.Members); len(ids) > 1 {
			source = " --cloud-source-id <id>"
			names := make([]string, len(ids))
			for i, id := range ids {
				names[i] = fmt.Sprint(id)
			}
			lines = append(lines, "  # your copies are on cloud sources "+strings.Join(names, ", ")+
				": name the one you answer for")
		}
		sel := fmt.Sprintf("%s --kind %%s --comment <text>%s --table-id %d", g.ID, source, tableID)
		lines = append(lines,
			"  qctl duplicate-group respond "+fmt.Sprintf(sel, "separate-rows")+
				"   # ask that the copies load as separate rows: a request, not a decision; the table's reviewers decide",
			"  qctl duplicate-group respond "+fmt.Sprintf(sel, "corrected-file-follows")+
				"   # say a corrected file will follow")
		if len(g.Responses) > 0 {
			lines = append(lines, "  # a new response replaces its cloud source's live one")
		}
		for _, r := range g.Responses {
			lines = append(lines, fmt.Sprintf("  qctl duplicate-group withdraw-response %s %s --table-id %d   # withdraw the live %s",
				g.ID, r.ID, tableID, terminal.Sanitize(string(r.Kind))))
		}
	}
	return "\nNEXT ACTIONS\n" + strings.Join(lines, "\n") + "\n"
}

// respondRefusalText explains why the caller may not respond.
func respondRefusalText(reason *RespondRefusal) string {
	if reason == nil {
		return "you may not respond on this group"
	}
	switch *reason {
	case "not_permitted":
		return "you may not respond: responding needs DATASET_INGEST_UPLOAD, which every role but read_only has " +
			"and a read_only grant on the table withholds"
	case "ended":
		return "the group has ended; it takes no responses"
	case "period_locked":
		return "you may not respond: the table's period is locked"
	}
	return "you may not respond on this group (" + terminal.Sanitize(string(*reason)) + ")"
}
