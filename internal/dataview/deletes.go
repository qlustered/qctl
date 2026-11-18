package dataview

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
)

// The two deletion columns folded into the Row State column.
const (
	deletedKindField = "deleted_kind_"
	rowDeleteIDField = "row_delete_id_"
)

// Row State token prefixes for a deleted row.
const (
	deletedPrefix   = "deleted="
	rowDeletePrefix = "row-delete="
)

// deletionKindNames mirrors DeletionKind in atlas common/common/types.py, the
// smallint deleted_kind_ stores. The spec does not publish it.
var deletionKindNames = map[int64]string{
	1: "person",
	2: "excluded_line",
	3: "absent_from_file",
	4: "delta",
	5: "composite",
	6: "erased",
}

// RowDeleteLine is one row_deletes entry, display-ready.
type RowDeleteLine struct {
	ID        int
	DeletedAt string // RFC 3339, UTC
	DeletedBy string // escaped
	Comment   string // escaped
	Undone    bool
}

// deletionTokens returns the Row State tokens for row's deletion columns:
// deleted=<kind> when deleted_kind_ is folded and set, then row-delete=<id>
// when row_delete_id_ is folded and set.
func deletionTokens(layout columnLayout, origRow []interface{}) []string {
	var tokens []string
	if layout.deletedKind >= 0 {
		if v := origRow[layout.deletedKind]; v != nil {
			tokens = append(tokens, deletedPrefix+deletionKindName(v))
		}
	}
	if layout.rowDelete >= 0 {
		if s := formatValue(origRow[layout.rowDelete]); s != "" {
			tokens = append(tokens, rowDeletePrefix+escapeCell(s))
		}
	}
	return tokens
}

// deletionKindName is v's DeletionKind name, or v as sent when it names none.
func deletionKindName(v interface{}) string {
	if n, ok := wholeNumber(v); ok {
		if name, known := deletionKindNames[n]; known {
			return name
		}
	}
	return escapeCell(formatValue(v))
}

// anyDeletedRow reports whether a folded deletion column is set on a row of
// schema.
func anyDeletedRow(layout columnLayout, schema *api.DataWithCellHistorySchema) bool {
	for _, row := range schema.Results {
		if len(deletionTokens(layout, row)) > 0 {
			return true
		}
	}
	return false
}

func rowDeleteLines(deletes *[]api.RowDeleteSummarySchema) []RowDeleteLine {
	if deletes == nil {
		return nil
	}
	lines := make([]RowDeleteLine, len(*deletes))
	for i, d := range *deletes {
		by := unattributed
		if d.DeletedBy != nil {
			by = historyUser(d.DeletedBy)
		}
		lines[i] = RowDeleteLine{
			ID:        d.ID,
			DeletedAt: d.DeletedAt.UTC().Format(time.RFC3339),
			DeletedBy: by,
			Comment:   historyText(d.Comment),
			Undone:    d.Undone,
		}
	}
	return lines
}

// writeRowDeletes writes one line per row delete the page's rows name.
func writeRowDeletes(b *strings.Builder, page *Page) {
	if len(page.RowDeletes) == 0 {
		return
	}
	b.WriteString("\n## ROW DELETES\n")
	for _, d := range page.RowDeletes {
		undone := ""
		if d.Undone {
			undone = " (undone)"
		}
		fmt.Fprintf(b, "- %d: %s by %s%s — %s\n", d.ID, d.DeletedAt, d.DeletedBy, undone, d.Comment)
	}
}

// matchingRowsSuffix splits Matching Rows into live and deleted when the page
// says so.
func matchingRowsSuffix(page *Page) string {
	if !page.ShowDeletedCounts {
		return ""
	}
	return " (" + strconv.Itoa(page.LiveRows) + " live, " + strconv.Itoa(page.DeletedRows) + " deleted)"
}
