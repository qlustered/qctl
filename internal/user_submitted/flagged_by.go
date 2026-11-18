package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

// MaxFlaggedByLines is how many group lines RenderFlaggedByPlain prints before
// it folds the rest into one "+N more" line.
const MaxFlaggedByLines = 10

// flaggedByGroup is one FLAGGED BY line: one flagger's records in one thread
// and one state.
type flaggedByGroup struct {
	label    string
	threadID string
	marker   string
	rows     []int
	seenRow  map[int]bool
	fields   []string
	seenFld  map[string]bool
}

// RenderFlaggedByPlain renders the FLAGGED BY footer section for an alert or
// warning's user_submitted_issues, as plain lines.
//
// Records are grouped by flagger, thread and state ("[retracted <date>]",
// "[detached <date>]" or live), live groups first, each set in
// first-appearance order. Each line lists the
// group's rows ascending and the union of its field names, then
// " — comment-thread/<uuid>" when the group has a thread. At most
// MaxFlaggedByLines groups are printed. The closing "Next:" line names the
// first live group's thread, else the first thread of any group, and is left
// out when no group has one; datasetID is its --table-id.
//
// Returns "" when issues is empty.
func RenderFlaggedByPlain(issues []api.UserSubmittedIssueReadSchema, datasetID int) string {
	if len(issues) == 0 {
		return ""
	}

	groups := groupFlags(issues)
	printed := groups
	if len(printed) > MaxFlaggedByLines {
		printed = printed[:MaxFlaggedByLines]
	}

	var b strings.Builder
	b.WriteString("\nFLAGGED BY\n")
	for _, g := range printed {
		sort.Ints(g.rows)
		fmt.Fprintf(&b, "  %s: %s", g.label, rowList(g.rows))
		if len(g.fields) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(g.fields, ", "))
		}
		if g.threadID != "" {
			fmt.Fprintf(&b, " — comment-thread/%s", g.threadID)
		}
		if g.marker != "" {
			fmt.Fprintf(&b, " %s", g.marker)
		}
		b.WriteString("\n")
	}
	if extra := len(groups) - len(printed); extra > 0 {
		fmt.Fprintf(&b, "  +%d more (see -o json)\n", extra)
	}
	if thread := nextThread(groups); thread != "" {
		fmt.Fprintf(&b, "Next: qctl describe comment-thread %s --table-id %d\n", thread, datasetID)
	}
	return b.String()
}

// groupFlags groups issues by flagger, thread and state, live groups first,
// each set in first-appearance order.
func groupFlags(issues []api.UserSubmittedIssueReadSchema) []*flaggedByGroup {
	var groups []*flaggedByGroup
	byKey := map[string]*flaggedByGroup{}
	for _, issue := range issues {
		threadID := ""
		if issue.ThreadID != nil {
			threadID = issue.ThreadID.String()
		}
		marker := stateMarker(issue)
		key := issue.SubmittedBy.ID.String() + "|" + threadID + "|" + marker
		g, ok := byKey[key]
		if !ok {
			g = &flaggedByGroup{
				label:    oneLine(FlaggerLabel(issue.SubmittedBy)),
				threadID: threadID,
				marker:   marker,
				seenRow:  map[int]bool{},
				seenFld:  map[string]bool{},
			}
			byKey[key] = g
			groups = append(groups, g)
		}
		if !g.seenRow[issue.RowID] {
			g.seenRow[issue.RowID] = true
			g.rows = append(g.rows, issue.RowID)
		}
		for _, f := range issue.FieldNames {
			f = oneLine(terminal.Sanitize(f))
			if !g.seenFld[f] {
				g.seenFld[f] = true
				g.fields = append(g.fields, f)
			}
		}
	}

	sort.SliceStable(groups, func(i, j int) bool { return groups[i].marker == "" && groups[j].marker != "" })
	return groups
}

// stateMarker returns "[retracted <YYYY-MM-DD>]" when issue is retracted, else
// "[detached <YYYY-MM-DD>]" when it is detached, else "". Dates are UTC.
func stateMarker(issue api.UserSubmittedIssueReadSchema) string {
	if issue.RetractedAt != nil {
		return "[retracted " + issue.RetractedAt.UTC().Format("2006-01-02") + "]"
	}
	if issue.DetachedAt != nil {
		return "[detached " + issue.DetachedAt.UTC().Format("2006-01-02") + "]"
	}
	return ""
}

// nextThread returns the thread of the first live group that has one, else
// the first group's thread that has one, else "".
func nextThread(groups []*flaggedByGroup) string {
	first := ""
	for _, g := range groups {
		if g.threadID == "" {
			continue
		}
		if g.marker == "" {
			return g.threadID
		}
		if first == "" {
			first = g.threadID
		}
	}
	return first
}

// oneLine escapes the LF and TAB that terminal.Sanitize keeps, so s cannot
// break a footer line.
func oneLine(s string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(s)
}
