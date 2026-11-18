package row_filters //nolint:revive // Matches the resource domain.

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
)

// Report is a sheet's row filter and the live rows it keeps, leaves out and
// cannot decide, as the server sends it. RowFilter is nil when the sheet has
// no row filter.
type Report struct {
	RowFilter     *api.RowFilter            `json:"row_filter"`
	Kept          int                       `json:"kept"`
	LeftOut       int                       `json:"left_out"`
	Blocked       int                       `json:"blocked"`
	LeftOutSample []api.RowFilterLeftOutRow `json:"left_out_sample"`
	MaskedFields  *[]string                 `json:"masked_fields,omitempty"`
}

// WriteReport writes r's row filter, its kept / left out / blocked counts, its
// left-out sample and a "Masked fields:" line naming the columns whose sample
// values are masked.
func WriteReport(w io.Writer, r *Report) error {
	lines := []string{"Row filter:", strings.TrimSuffix(FormatText(FromAPI(r.RowFilter), "  "), "\n")}
	if r.Kept == 0 && r.LeftOut == 0 && r.Blocked == 0 {
		return writeLines(w, append(lines, "No live rows: the table is empty."))
	}
	lines = append(lines, fmt.Sprintf("%d kept / %d left out / %d blocked", r.Kept, r.LeftOut, r.Blocked))
	if r.Blocked > 0 {
		lines = append(lines, "Blocked: "+FormatRows(r.Blocked, false)+
			" the filter cannot decide, such as a quarantined value that does not parse or a predicate that raises an error.")
	}
	if n := len(r.LeftOutSample); n > 0 {
		heading := "Left out, lowest row ids first:"
		if n < r.LeftOut {
			heading = fmt.Sprintf("Left out, lowest row ids first (%d of %d):", n, r.LeftOut)
		}
		lines = append(lines, heading)
		for _, row := range r.LeftOutSample {
			lines = append(lines, leftOutRowText(row))
		}
	}
	if r.MaskedFields != nil && len(*r.MaskedFields) > 0 {
		names := make([]string, 0, len(*r.MaskedFields))
		for _, name := range *r.MaskedFields {
			names = append(names, oneLine(name))
		}
		lines = append(lines, "Masked fields: "+strings.Join(names, ", "))
	}
	return writeLines(w, lines)
}

// leftOutRowText renders row as "  row <id>", then its values by column name.
func leftOutRowText(row api.RowFilterLeftOutRow) string {
	text := "  row " + strconv.Itoa(row.RowID)
	if row.Quarantined {
		text += " (quarantined)"
	}
	if len(row.Values) == 0 {
		return text
	}
	values := make([]string, 0, len(row.Values))
	for _, column := range slices.Sorted(maps.Keys(row.Values)) {
		values = append(values, oneLine(column)+"="+leftOutValueText(row.Values[column]))
	}
	return text + ": " + strings.Join(values, ", ")
}

// leftOutValueText renders v as null, a quoted string, or its printed form
// kept to one line.
func leftOutValueText(v any) string {
	switch v := v.(type) {
	case nil:
		return "null" //nolint:goconst // A sample value's display text, not JSON.
	case string:
		return strconv.Quote(v)
	}
	return oneLine(fmt.Sprint(v))
}
