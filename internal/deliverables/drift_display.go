package deliverables

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/api"
)

// MaxDriftLimit is the largest limit the drift endpoint accepts.
const MaxDriftLimit = 10000

// WriteDrift writes d's remedy, reissue and limit fields, its note, and its
// rows as a grid, and returns the write error.
func WriteDrift(w io.Writer, d *api.DeliveredRunDriftResponse) error {
	var b strings.Builder
	line := func(label, value string) { fmt.Fprintf(&b, "%-14s %s\n", label+":", value) }
	fmt.Fprintf(&b, "Drift of run %d\n", d.RunID)
	line("Remedy", dash(cellText(string(d.Remedy))))
	line("Can reissue", yesNo(d.CanReissue))
	line("Reissue run", pointerText(d.ReissueRunID))
	line("Superseded by", pointerText(d.SupersededByRunID))
	line("Rows shown", fmt.Sprintf("%d (limit %d)", len(d.Rows), d.Limit))
	if d.Truncated {
		fmt.Fprintf(&b, "Truncated: more rows drifted than the limit of %d", d.Limit)
		if d.Limit < MaxDriftLimit {
			fmt.Fprintf(&b, "; a larger --limit (at most %d) shows more", MaxDriftLimit)
		}
		b.WriteString(".\n")
	}
	if d.Note != nil && *d.Note != "" {
		fmt.Fprintf(&b, "Note: %s\n", cellText(*d.Note))
	}
	b.WriteString("\n")
	if len(d.Rows) == 0 {
		b.WriteString("No changes found in the delivered rows.\n")
	} else {
		writeDriftRows(&b, d.Rows)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeDriftRows(b *strings.Builder, rows []api.DeliveredDriftRowItem) {
	w := tabwriter.NewWriter(b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SHEET\tTABLE\tROW\tREASONS\tDELIVERED VER\tLIVE VER\tCARRIED FROM\tFILTER REASON")
	for _, row := range rows {
		reasons := make([]string, len(row.Reasons))
		for i, reason := range row.Reasons {
			reasons[i] = cellText(string(reason))
		}
		filterReason := ""
		if row.FilterReason != nil {
			filterReason = cellText(*row.FilterReason)
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%d\t%s\t%s\t%s\n", dash(cellText(row.SheetName)), row.MemberDatasetID, row.RowID,
			dash(strings.Join(reasons, ", ")), row.DeliveredRowVersionID, pointerText(row.LiveRowVersionID),
			pointerText(row.CarriedFromRunID), dash(filterReason))
	}
	_ = w.Flush()
}
