package deliverables

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/terminal"
)

// RevisionColumns are the default revision listing columns.
const RevisionColumns = "release,state,template,enabled_at,created_at"

// RevisionDisplay is a revision's table row.
type RevisionDisplay struct {
	ID          string `json:"id"`
	Release     string `json:"release"`
	State       string `json:"state"`
	Description string `json:"description"`
	Template    string `json:"template"`
	EnabledBy   string `json:"enabled_by"`
	EnabledAt   string `json:"enabled_at"`
	CreatedAt   string `json:"created_at"`
}

// RevisionRows returns one table row per revision.
func RevisionRows(items []Revision) []RevisionDisplay {
	rows := make([]RevisionDisplay, len(items))
	for i, item := range items {
		rows[i] = RevisionDisplay{ID: item.ID.String(), Release: item.Release, State: string(item.State),
			Description: pointerText(item.Description), Template: pointerText(item.SkeletonID),
			EnabledBy: pointerText(item.EnabledByID), EnabledAt: timestamp(item.EnabledAt), CreatedAt: timestamp(item.CreatedAt)}
	}
	return rows
}

func pointerText[T any](value *T) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprint(*value)
}

func copied[T any](items *[]T) []T {
	if items == nil {
		return nil
	}
	return slices.Clone(*items)
}

func cellText(text string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(terminal.Sanitize(text))
}

// FormatRevision renders the revision's template and binding tree.
func FormatRevision(detail *RevisionDetail) string {
	r := detail.DeliverableRevisionResponse
	var b strings.Builder
	line := func(label, value string) { fmt.Fprintf(&b, "%-19s %s\n", label+":", cellText(value)) }
	line("Release", r.Release)
	line("ID", r.ID.String())
	line("Deliverable", r.DeliverableID.String())
	line("State", string(r.State))
	line("Description", pointerText(r.Description))
	line("Enabled by", pointerText(r.EnabledByID))
	line("Enabled at", timestamp(r.EnabledAt))
	line("Template", pointerText(r.SkeletonID))
	acknowledged := r.SkeletonLossyAcknowledged != nil && *r.SkeletonLossyAcknowledged
	line("Lossy acknowledged", strconv.FormatBool(acknowledged))
	formatInventory(&b, r.WorkbookInventory)
	formatFidelity(&b, r.FidelityReport)
	sheets := copied(r.SheetBindings)
	slices.SortFunc(sheets, func(a, b api.SheetBindingResponse) int { return cmp.Compare(a.SheetName, b.SheetName) })
	fmt.Fprintf(&b, "\nSheet bindings (%d):\n", len(sheets))
	for _, sheet := range sheets {
		fmt.Fprintf(&b, "  %s [%s]\n    Table: %d; series: %s\n", cellText(sheet.SheetName), sheet.ID, sheet.MemberDatasetID, pointerText(sheet.MemberSeriesID))
		fmt.Fprintf(&b, "    Data start row: %d; header rows: %v; instruction rows: %d\n", sheet.DataStartRow, sheet.HeaderRows, sheet.InstructionRowCount)
		writeRowFilter(&b, sheet.RowFilter)
		keys := copied(sheet.SortKeys)
		slices.SortFunc(keys, func(a, b api.SortKeySchema) int { return cmp.Compare(a.Position, b.Position) })
		for _, key := range keys {
			order := "ascending"
			if key.Descending != nil && *key.Descending {
				order = "descending"
			}
			fmt.Fprintf(&b, "    Sort %d: %s %s\n", key.Position, cellText(key.ColumnName), order)
		}
		columns := copied(sheet.ColumnBindings)
		slices.SortFunc(columns, func(a, b api.ColumnBindingResponse) int { return cmp.Compare(a.TargetColumnIndex, b.TargetColumnIndex) })
		for _, column := range columns {
			source := sourceText(column.SourceType, column.SourceColumn, column.RunParam, column.LiteralValue)
			fmt.Fprintf(&b, "    Column %d (%s) <- %s [%s]\n", column.TargetColumnIndex, ColumnLetter(column.TargetColumnIndex), cellText(source), column.ID)
			fmt.Fprintf(&b, "      Label: %s; origin: %s; needs confirmation: %t; format: %s\n", cellText(pointerText(column.TargetLabel)), cellText(string(column.Origin)), column.NeedsConfirmation, cellText(pointerText(column.FormatOverride)))
		}
	}
	cells := copied(r.CellBindings)
	slices.SortFunc(cells, func(a, b api.CellBindingResponse) int {
		if n := cmp.Compare(a.SheetName, b.SheetName); n != 0 {
			return n
		}
		return cmp.Compare(a.CellRef, b.CellRef)
	})
	fmt.Fprintf(&b, "\nCell bindings (%d):\n", len(cells))
	for _, cell := range cells {
		formatCell(&b, cell)
	}
	return b.String()
}

func formatCell(b *strings.Builder, cell api.CellBindingResponse) {
	source := string(cell.SourceType)
	switch source {
	case "literal":
		source += " " + pointerText(cell.LiteralValue)
	case "run_param":
		source = "run parameter " + pointerText(cell.RunParam)
	case "aggregate":
		source = fmt.Sprintf("%s(%s); table: %s; series: %s; same rows as sheet binding: %s", pointerText(cell.AggregateFunc), pointerText(cell.AggregateColumn), pointerText(cell.AggregateMemberDatasetID), pointerText(cell.AggregateMemberSeriesID), pointerText(cell.SameRowsAsSheetBindingID))
	}
	fmt.Fprintf(b, "  %s!%s <- %s [%s]\n", cellText(cell.SheetName), cellText(cell.CellRef), cellText(source), cell.ID)
	fmt.Fprintf(b, "    Label cell: %s; label: %s; needs confirmation: %t; format: %s\n", cellText(pointerText(cell.LabelCellRef)), cellText(pointerText(cell.TargetLabel)), cell.NeedsConfirmation, cellText(pointerText(cell.FormatOverride)))
	if cell.RowFilter != nil || cell.SourceType == api.DeliverableCellSourceTypeAggregate && cell.SameRowsAsSheetBindingID == nil {
		writeRowFilter(b, cell.RowFilter)
	}
}

// writeRowFilter writes filter as a "Row filter:" line followed by its
// row_filters.FormatText block.
func writeRowFilter(b *strings.Builder, filter *api.RowFilter) {
	b.WriteString("    Row filter:\n")
	b.WriteString(row_filters.FormatText(row_filters.FromAPI(filter), "      "))
}

func formatInventory(b *strings.Builder, inventory *api.WorkbookInventory) {
	if inventory == nil {
		return
	}
	fmt.Fprintf(b, "\nTemplate inventory (VBA: %t):\n", inventory.HasVba)
	sheets := slices.Clone(inventory.Sheets)
	slices.SortFunc(sheets, func(a, b api.WorkbookSheetInventory) int { return cmp.Compare(a.Name, b.Name) })
	for _, sheet := range sheets {
		fmt.Fprintf(b, "  %s: %d rows x %d columns; state: %s; empty: %s; used: %s x %s\n", cellText(sheet.Name), sheet.MaxRow, sheet.MaxColumn, cellText(pointerText(sheet.State)), pointerText(sheet.IsEmpty), pointerText(sheet.UsedMaxRow), pointerText(sheet.UsedMaxColumn))
		fmt.Fprintf(b, "    Merged ranges: %s\n", cellText(dash(strings.Join(sheet.MergedRanges, ", "))))
		keys := make([]string, 0, len(sheet.HeaderRows))
		for key := range sheet.HeaderRows {
			keys = append(keys, key)
		}
		slices.SortFunc(keys, func(a, b string) int { x, _ := strconv.Atoi(a); y, _ := strconv.Atoi(b); return cmp.Compare(x, y) })
		for _, key := range keys {
			fmt.Fprintf(b, "    Header row %s: %s\n", cellText(key), cellText(strings.Join(sheet.HeaderRows[key], " | ")))
		}
	}
}

func formatFidelity(b *strings.Builder, report *api.FidelityReport) {
	if report == nil {
		return
	}
	version := 1
	if report.Version != nil {
		version = *report.Version
	}
	fmt.Fprintf(b, "\nFidelity (version %d, lossy: %t):\n", version, report.IsLossy)
	label := "Lost part"
	if itemizedFidelity(*report) {
		label = "Loss"
	}
	for _, loss := range FidelityLossLines(*report) {
		fmt.Fprintf(b, "  %s: %s\n", label, cellText(loss))
	}
	for _, note := range copied(report.Notes) {
		fmt.Fprintf(b, "  Note: %s\n", cellText(note))
	}
}

// FormatTemplateRows renders template rows with spreadsheet coordinates.
func FormatTemplateRows(rows *TemplateRows) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Template sheet: %s\nDimensions: %d rows x %d columns\nMerged ranges: %s\n\n", cellText(rows.SheetName), rows.MaxRow, rows.MaxColumn, cellText(dash(strings.Join(copied(rows.MergedRanges), ", "))))
	width := 0
	for _, row := range rows.Rows {
		width = max(width, len(row))
	}
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprint(w, "ROW")
	for col := 1; col <= width; col++ {
		fmt.Fprintf(w, "\t%s", ColumnLetter(col))
	}
	fmt.Fprintln(w)
	for i, row := range rows.Rows {
		fmt.Fprint(w, i+1)
		for col := 0; col < width; col++ {
			value := ""
			if col < len(row) {
				value = cellText(row[col])
			}
			fmt.Fprintf(w, "\t%s", value)
		}
		fmt.Fprintln(w)
	}
	_ = w.Flush()
	return b.String()
}
