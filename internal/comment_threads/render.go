package comment_threads //nolint:revive // Matches the backend resource name.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// wholeRowMarker is how a whole-row target is spelled in every rendering.
// whole_row and field_names are independent on the wire, so this stands
// alongside the named fields of the same row, never instead of them.
const wholeRowMarker = "whole-row"

// RenderTargets formats a thread's targets for display. whole_row and
// field_names are INDEPENDENT on the wire — a thread may target the row as a
// whole and some of its cells at once — so a whole-row target is printed as the
// literal marker "whole-row" alongside, never instead of, the named fields.
func RenderTargets(t *Thread) string {
	if t.Targets == nil || len(*t.Targets) == 0 {
		return "(none)" //nolint:goconst // Keep the no-targets marker beside the check that returns it.
	}
	byRow := map[int][]string{}
	var order []int
	for _, target := range *t.Targets {
		if _, seen := byRow[target.RowID]; !seen {
			order = append(order, target.RowID)
		}
		if target.FieldName == nil {
			byRow[target.RowID] = append(byRow[target.RowID], wholeRowMarker)
			continue
		}
		byRow[target.RowID] = append(byRow[target.RowID], *target.FieldName)
	}
	sort.Ints(order)
	parts := make([]string, 0, len(order))
	for _, rowID := range order {
		parts = append(parts, fmt.Sprintf("%d=%s", rowID, strings.Join(byRow[rowID], ",")))
	}
	return strings.Join(parts, " ")
}

// RenderThread is the agent-oriented transcript payload, mirroring
// rule_reviews.RenderDetail: a header block, then the comment tree with
// [c: <uuid>] tags the reply/edit/delete verbs take verbatim.
func RenderThread(t *Thread) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# COMMENT THREAD %s\n", t.ID)
	if t.Title != nil && *t.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", *t.Title)
	}
	fmt.Fprintf(&b, "Targets: %s\n", RenderTargets(t))
	if t.DuplicateGroupID != nil {
		fmt.Fprintf(&b, "Duplicate group: duplicate-group/%s\n", t.DuplicateGroupID)
	}
	fmt.Fprintf(&b, "Opened by: %s (%s)\n",
		comments.UserDisplayWithRole(t.CreatedBy, t.CreatedByID), timeutil.FormatRelative(t.CreatedAt))
	b.WriteString("Status: " + resolvedLine(t) + "\n")

	var cs []Comment
	if t.Comments != nil {
		cs = *t.Comments
	}
	fmt.Fprintf(&b, "\n## COMMENTS (%d top-level, %d total)\n", len(cs), comments.CountComments(cs))
	for i := range cs {
		comments.RenderComment(&b, &cs[i], 0)
	}
	return terminal.Sanitize(b.String())
}

// resolvedLine reports WHY a thread is resolved, because the backend resolves
// threads on its own: the sweep stamps resolved_reason "row_gone" when every
// target row is gone (keyless sync re-mints row ids, so this is not rare).
func resolvedLine(t *Thread) string {
	if t.ResolvedAt == nil {
		return "open"
	}
	line := "resolved " + timeutil.FormatRelative(*t.ResolvedAt)
	if t.ResolvedReason != nil && *t.ResolvedReason != "" {
		line += " (" + *t.ResolvedReason + ")"
	}
	return line
}

// SummaryRow is the table-display projection of a thread, one row per thread.
// The json tags are the column names `get comment-threads` renders.
type SummaryRow struct {
	ID             string `json:"id"`
	Rows           string `json:"rows"`
	Fields         string `json:"fields"`
	Comments       int    `json:"comments"`
	Resolved       string `json:"resolved"`
	Title          string `json:"title"`
	DuplicateGroup string `json:"duplicate_group"`
}

// SummaryRows projects threads for the table view. Rows lists each targeted row
// id once, ascending; Fields lists each distinct field once in the order the
// backend sent them, with "whole-row" standing for a target that names no field.
func SummaryRows(threads []Thread) []SummaryRow {
	rows := make([]SummaryRow, 0, len(threads))
	for i := range threads {
		t := &threads[i]
		rowIDs, fields := summarizeTargets(t)
		group := "-"
		if t.DuplicateGroupID != nil {
			group = t.DuplicateGroupID.String()
		}
		rows = append(rows, SummaryRow{
			ID:             t.ID.String(),
			Rows:           strings.Join(rowIDs, ","),
			Fields:         strings.Join(fields, ","),
			Comments:       commentTotal(t),
			Resolved:       yesNo(t.ResolvedAt != nil),
			Title:          stringOrEmpty(t.Title),
			DuplicateGroup: group,
		})
	}
	return rows
}

// summarizeTargets splits the targets into the row-id list and the field list
// the table's two columns show. Both are deduplicated: a row targeted as a
// whole and by three of its cells is still one row.
func summarizeTargets(t *Thread) (rowIDs, fields []string) {
	if t.Targets == nil {
		return nil, nil
	}
	seenRow := map[int]bool{}
	var ids []int
	seenField := map[string]bool{}
	for _, target := range *t.Targets {
		if !seenRow[target.RowID] {
			seenRow[target.RowID] = true
			ids = append(ids, target.RowID)
		}
		name := wholeRowMarker
		if target.FieldName != nil {
			name = *target.FieldName
		}
		if !seenField[name] {
			seenField[name] = true
			fields = append(fields, name)
		}
	}
	sort.Ints(ids)
	for _, id := range ids {
		rowIDs = append(rowIDs, strconv.Itoa(id))
	}
	return rowIDs, fields
}

// commentTotal prefers the backend's own count — comments is omitempty, and a
// response that withholds the transcript still carries a real comment_count.
func commentTotal(t *Thread) int {
	if t.CommentCount != nil {
		return *t.CommentCount
	}
	if t.Comments == nil {
		return 0
	}
	return comments.CountComments(*t.Comments)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
