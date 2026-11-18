package deliverables

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/api"
)

// WriteCompleteness writes c's missed rows, destinations, limit and note.
func WriteCompleteness(w io.Writer, c *api.CompletenessResponse) error {
	var rows []api.CompletenessRowItem
	if c.Rows != nil {
		rows = *c.Rows
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Missed rows of deliverable %s\nRows shown: %d (limit %d)\n", c.DeliverableID, len(rows), c.Limit)
	if c.Truncated {
		b.WriteString("Limit reached: additional missed rows may exist")
		if c.Limit < MaxCompletenessLimit {
			fmt.Fprintf(&b, "; a larger --limit (at most %d) can show more", MaxCompletenessLimit)
		}
		b.WriteString(".\n")
	}
	if c.Note != nil && *c.Note != "" {
		fmt.Fprintf(&b, "Note: %s\n", cellText(*c.Note))
	}
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString("No missed rows found.\n")
	} else {
		writeCompletenessRows(&b, rows)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeCompletenessRows(b *strings.Builder, rows []api.CompletenessRowItem) {
	w := tabwriter.NewWriter(b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SHEET\tTABLE\tROW\tPROBLEM\tWHAT HAPPENS NEXT\tRE-ISSUE")
	for _, row := range rows {
		reissue := "-"
		if row.WaivedOnRunID == nil && row.CanReissue && row.ReissueRunID == nil {
			reissue = "eligible"
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\t%s\n", dash(cellText(row.SheetName)), row.MemberDatasetID, row.RowID,
			completenessProblem(row), completenessDestination(row), reissue)
	}
	_ = w.Flush()
}

func completenessProblem(row api.CompletenessRowItem) string {
	if row.WaivedOnRunID != nil {
		problem := "Left out: row filter cannot decide"
		if row.UndecidedReason != nil && *row.UndecidedReason != "" {
			problem += ": " + cellText(*row.UndecidedReason)
		}
		return problem
	}
	problem := "Missed from delivered report"
	if row.MatchedDeliveredRunID != nil {
		problem += fmt.Sprintf(" (run %d)", *row.MatchedDeliveredRunID)
	}
	return problem
}

func completenessDestination(row api.CompletenessRowItem) string {
	switch {
	case row.WaivedOnRunID != nil:
		return fmt.Sprintf("Left out in run %d", *row.WaivedOnRunID)
	case row.CarriedIntoRunID != nil:
		return fmt.Sprintf("Carried into run %d", *row.CarriedIntoRunID)
	case row.ReissueRunID != nil:
		return fmt.Sprintf("Will be re-issued in run %d", *row.ReissueRunID)
	default:
		return "Will be carried into the next period's run"
	}
}
