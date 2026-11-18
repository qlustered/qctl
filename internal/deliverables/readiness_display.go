package deliverables

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/row_filters"
)

// readinessDefaultLimit is the rows page size the server uses when the
// request names none.
const readinessDefaultLimit = 50

// FormatReadinessSummary renders a run's readiness status, note, per-sheet
// counts, errors, warnings, carried rows of sheets the revision does not
// bind, and waivers on no sheet.
func FormatReadinessSummary(s *ReadinessSummary) string {
	var b strings.Builder
	status := "no errors or blocked rows"
	switch {
	case !s.IsLive:
		status = "as of sign-off"
	case s.IsSignoffBlocked:
		status = "sign-off blocked"
	}
	fmt.Fprintf(&b, "Readiness of run %d (%s): %s\n", s.RunID, cellText(string(s.State)), status)
	if s.Note != "" {
		fmt.Fprintf(&b, "Note: %s\n", cellText(s.Note))
	}
	b.WriteString("\n")
	if len(s.Sheets) == 0 {
		b.WriteString("No sheet counts.\n")
	} else {
		writeReadinessSheetCounts(&b, s.Sheets)
	}
	if len(s.Errors) > 0 {
		b.WriteString("\nErrors (render and sign-off refuse the run while any exists):\n")
		for _, issue := range s.Errors {
			b.WriteString("  " + cellText(FormatIssue(issue.Issue)) + "\n")
			if row := issue.waivableCarriedRow(); row != nil && s.canChangeWaivers() && s.RunID > 0 {
				fmt.Fprintf(&b, "  Waivable carried row: table %d; row %d\n  Leave out: qctl deliverable-run waive %d --table-id %d --row-id %d --reason \"<reason>\"\n",
					row.MemberDatasetID, row.RowID, s.RunID, row.MemberDatasetID, row.RowID)
			}
		}
	}
	writeReadinessIssues(&b, "Warnings:", s.Warnings)
	if len(s.UnboundCarried) > 0 {
		b.WriteString("\nCarried rows for sheets this revision does not bind:\n")
		for _, carried := range s.UnboundCarried {
			fmt.Fprintf(&b, "  %s: %d not waived, %d waived\n", readinessSheetName(carried.SheetName), carried.Unwaived, carried.Waived)
		}
	}
	if s.WaivedWithoutSheet > 0 {
		fmt.Fprintf(&b, "\nWaivers on no sheet: %d\n", s.WaivedWithoutSheet)
	}
	return b.String()
}

func writeReadinessSheetCounts(b *strings.Builder, sheets []SheetReadiness) {
	w := tabwriter.NewWriter(b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SHEET\tTABLE\tINCLUDED\tCARRIED FORWARD\tAWAITING REVIEW\tFAILING RULES\tINDETERMINATE\tWAIVED\tOUT OF SCOPE")
	for _, s := range sheets {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n", dash(cellText(s.SheetName)), s.MemberDatasetID,
			s.Included, s.CarriedForward, s.BlockedAwaitingReview, s.BlockedFailingRules, s.BlockedIndeterminate, s.Waived, s.OutOfScope)
	}
	_ = w.Flush()
}

func writeReadinessIssues(b *strings.Builder, heading string, issues []Issue) {
	if len(issues) == 0 {
		return
	}
	b.WriteString("\n" + heading + "\n")
	for _, issue := range issues {
		b.WriteString("  " + cellText(FormatIssue(issue)) + "\n")
	}
}

func readinessSheetName(name string) string {
	return `"` + cellText(name) + `"`
}

// WriteReadiness writes r's summary, each sheet's row filter report and r's
// page of rows, and returns the first write error.
func WriteReadiness(w io.Writer, r *Readiness) error {
	var b strings.Builder
	b.WriteString(FormatReadinessSummary(&r.ReadinessSummary))
	for _, sheet := range r.Sheets {
		if err := writeReadinessRowFilter(&b, sheet); err != nil {
			return err
		}
	}
	writeReadinessRows(&b, r)
	_, err := io.WriteString(w, b.String())
	return err
}

// writeReadinessRowFilter writes sheet's row filter report indented under a
// heading naming the sheet.
func writeReadinessRowFilter(b *strings.Builder, sheet SheetReadiness) error {
	fmt.Fprintf(b, "\nSheet %s row filter:", readinessSheetName(sheet.SheetName))
	if sheet.RowFilterReport == nil {
		b.WriteString(" none recorded by the last render\n")
		return nil
	}
	b.WriteString("\n")
	var report bytes.Buffer
	if err := row_filters.WriteReport(&report, sheet.RowFilterReport); err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSuffix(report.String(), "\n"), "\n") {
		if line != "" {
			b.WriteString("  ")
		}
		b.WriteString(line + "\n")
	}
	return nil
}

func writeReadinessRows(b *strings.Builder, r *Readiness) {
	page := max(r.RowsPage, 1)
	limit := r.RowsLimit
	if limit < 1 {
		limit = readinessDefaultLimit
	}
	bucket, scope := "", "(all buckets)"
	if r.RowsBucket != nil && *r.RowsBucket != "" {
		bucket = cellText(*r.RowsBucket)
		scope = "in bucket " + bucket
	}
	b.WriteString("\n")
	switch {
	case len(r.Rows) == 0 && r.RowsTotal == 0:
		fmt.Fprintf(b, "No rows %s.\n", scope)
		return
	case len(r.Rows) == 0:
		fmt.Fprintf(b, "No rows on page %d (%d in total).\n", page, r.RowsTotal)
		return
	}
	from := (page-1)*limit + 1
	to := from + len(r.Rows) - 1
	fmt.Fprintf(b, "Rows %d-%d of %d %s:\n", from, to, r.RowsTotal, scope)
	w := tabwriter.NewWriter(b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SHEET\tTABLE\tROW\tBUCKET\tQUARANTINED\tFILTER UNDECIDED\tCARRIED FROM\tWAIVER REASON\tREASON")
	for _, row := range r.Rows {
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n", dash(cellText(pointerText(row.SheetName))), row.MemberDatasetID, row.RowID,
			dash(cellText(row.Bucket)), readinessBoolText(row.Quarantined), readinessBoolText(row.RowFilterUndecided),
			pointerText(row.CarriedFromRunID), dash(cellText(pointerText(row.WaiverReason))), dash(cellText(row.Reason)))
	}
	_ = w.Flush()
	if to < r.RowsTotal {
		next := fmt.Sprintf("Next: qctl deliverable-run readiness %d --page %d", r.RunID, page+1)
		if bucket != "" {
			next += " --bucket " + bucket
		}
		if limit != readinessDefaultLimit {
			next += " --limit " + strconv.Itoa(limit)
		}
		b.WriteString(next + "\n")
	}
}

func readinessBoolText(value *bool) string {
	if value == nil {
		return "-"
	}
	return yesNo(*value)
}
