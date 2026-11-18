package deliverables

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/row_filters"
)

// RowFilterCount is the server's count of the rows a planned binding save's
// row filter keeps.
type RowFilterCount struct {
	// Key is the filter's manifest key, such as spec.sheets.Data.row_filter.
	Key string
	// Table is the table counted on.
	Table int
	// Series reports that the binding follows a table series, so Table is
	// the series' reference table.
	Series bool
	// Kept reports that the save keeps the stored filter instead of sending
	// one.
	Kept  bool
	Count *api.RowFilterCountSchema
}

// filterSave is the row filter a planned binding save sends or keeps, and the
// table the server compiles it against.
type filterSave struct {
	key          string
	table        int
	series, kept bool
	filter       *row_filters.Filter
}

// tableText names the table s is counted on.
func (s filterSave) tableText() string {
	text := "table " + strconv.Itoa(s.table)
	if s.series {
		text += ", the series' reference table"
	}
	return text
}

// countRowFilters counts each distinct table and filter rows that a sheet or
// cell save in requests sends or keeps, once, and records the counts on p in
// manifest key order. A 422 is a bad-usage error naming the manifest key; any
// other failure is recorded as a warning.
func (c *Client) countRowFilters(token string, p *Plan, requests []Request) error {
	saves := p.filterSaves(requests)
	if len(saves) == 0 {
		return nil
	}
	counter, err := row_filters.NewClient(c.baseURL, c.orgID.String(), c.verbosity)
	if err != nil {
		return err
	}
	type result struct {
		count *api.RowFilterCountSchema
		err   error
	}
	results := map[string]result{}
	for _, save := range saves {
		id := strconv.Itoa(save.table) + " " + jsonText(row_filters.Draft(save.filter))
		r, counted := results[id]
		if !counted {
			r.count, r.err = counter.CountKept(token, save.table, save.filter)
			results[id] = r
		}
		if r.err == nil {
			p.RowFilterCounts = append(p.RowFilterCounts, RowFilterCount{Key: save.key, Table: save.table, Series: save.series,
				Kept: save.kept, Count: r.count})
			continue
		}
		var apiErr *apierror.OperationalError
		if errors.As(r.err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity {
			subject := save.key
			if save.kept {
				subject = save.key + ": the stored row filter, which this save keeps,"
			}
			hint := fmt.Sprintf("Set %s to a row filter that fits the table, or to null to clear it.", save.key)
			return fmt.Errorf("%s does not fit %s: %w\n\n%s", subject, save.tableText(), r.err, hint)
		}
		p.RowFilterWarnings = append(p.RowFilterWarnings, fmt.Sprintf("could not count rows for %s: %s", save.key, firstLine(r.err)))
	}
	return nil
}

// filterSaves returns the row filter each sheet and cell save in requests
// sends or keeps, sheets by name and then cells by key.
func (p *Plan) filterSaves(requests []Request) []filterSave {
	sheetSaves, cellSaves := map[string]Request{}, map[string]Request{}
	for _, r := range requests {
		switch r.step {
		case stepSheetCreate, stepSheetPatch:
			sheetSaves[r.Sheet] = r
		case stepCellCreate, stepCellPatch:
			cellSaves[r.Sheet+"!"+r.Cell] = r
		}
	}
	stored := sheetsByName(p.Tree)
	var saves []filterSave
	for _, sheet := range p.sheets {
		r, ok := sheetSaves[sheet.name]
		if !ok {
			continue
		}
		save := filterSave{key: SheetFilterKey(sheet.name)}
		save.table, _ = r.Body[wireMemberDatasetID].(int)
		save.series = r.Body[wireMemberSeriesID] != nil
		if sent, written := r.Body[keyRowFilter]; written {
			save.filter = sentFilter(sent)
		} else if r.step == stepSheetPatch {
			save.filter, save.kept = row_filters.FromAPI(stored[sheet.name].RowFilter), true
		}
		if save.filter != nil {
			saves = append(saves, save)
		}
	}
	for _, cell := range p.cells {
		r, ok := cellSaves[cell.sheet+"!"+cell.ref]
		if !ok {
			continue
		}
		save := filterSave{key: cellFilterKey(cell.sheet, cell.ref), filter: sentFilter(r.Body[keyRowFilter])}
		save.table, _ = r.Body[wireAggregateTable].(int)
		save.series = r.Body[wireAggregateSeries] != nil
		save.kept = cell.spec.Total == nil || !cell.spec.Total.Has(keyRowFilter)
		if save.filter != nil {
			saves = append(saves, save)
		}
	}
	return saves
}

// sentFilter returns the row filter a save body's row_filter value sends, or
// nil when it sends none.
func sentFilter(value any) *row_filters.Filter {
	sent, _ := value.(*api.DescribedRowFilter)
	if sent == nil {
		return nil
	}
	return row_filters.FromAPI(&api.RowFilter{Conditions: sent.Conditions, Predicate: sent.Predicate})
}

// storedFilter is a stored row filter and the table its binding reads.
type storedFilter struct {
	key    string
	table  int
	filter *row_filters.Filter
}

// warnMissingFilterColumns records a warning on p for each of
// untouchedFilters that reads a column its table's applied, non-deleted
// columns lack, or whose table's columns cannot be read. Each table is read
// at most once.
func (c *Client) warnMissingFilterColumns(token string, p *Plan, requests []Request) {
	failed := map[int]error{}
	for _, stored := range p.untouchedFilters(requests) {
		columns, loaded := p.tableColumns[stored.table]
		if !loaded && failed[stored.table] == nil {
			var err error
			if columns, err = c.appliedColumns(token, stored.table); err != nil {
				failed[stored.table] = err
			} else {
				if p.tableColumns == nil {
					p.tableColumns = map[int]map[string]bool{}
				}
				p.tableColumns[stored.table] = columns
			}
		}
		if err := failed[stored.table]; err != nil {
			p.RowFilterWarnings = append(p.RowFilterWarnings, fmt.Sprintf("could not check the columns %s reads: %s", stored.key, firstLine(err)))
		} else if warning := missingColumnsWarning(stored, columns); warning != "" {
			p.RowFilterWarnings = append(p.RowFilterWarnings, warning)
		}
	}
}

// untouchedFilters returns the stored row filter of each sheet in spec.sheets,
// and of each total in spec.cells with its own table, that no save in
// requests sends or keeps, sheets by name and then cells by key.
func (p *Plan) untouchedFilters(requests []Request) []storedFilter {
	saved := map[string]bool{}
	for _, r := range requests {
		switch r.step {
		case stepSheetCreate, stepSheetPatch, stepCellCreate, stepCellPatch:
			saved[r.Sheet+"!"+r.Cell] = true
		}
	}
	var filters []storedFilter
	stored := sheetsByName(p.Tree)
	for _, sheet := range p.sheets {
		if current, ok := stored[sheet.name]; ok && current.RowFilter != nil && !saved[sheet.name+"!"] {
			filters = append(filters, storedFilter{SheetFilterKey(sheet.name), current.MemberDatasetID, row_filters.FromAPI(current.RowFilter)})
		}
	}
	cells, _ := cellsByKey(p.Tree)
	for _, cell := range p.cells {
		current, ok := cells[cell.sheet+"!"+cell.ref]
		if cell.spec.Total == nil || !ok || saved[cell.sheet+"!"+cell.ref] || current.RowFilter == nil ||
			current.SameRowsAsSheetBindingID != nil || current.AggregateMemberDatasetID == nil {
			continue
		}
		filters = append(filters, storedFilter{cellFilterKey(cell.sheet, cell.ref), *current.AggregateMemberDatasetID, row_filters.FromAPI(current.RowFilter)})
	}
	return filters
}

// missingColumnsWarning names the columns stored's filter reads that columns
// lacks, or returns "" when it lacks none.
func missingColumnsWarning(stored storedFilter, columns map[string]bool) string {
	var missing []string
	for _, name := range stored.filter.Columns() {
		if !columns[name] {
			missing = append(missing, strconv.Quote(cellText(name)))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	reads := "column " + missing[0] + ", which is"
	if len(missing) > 1 {
		reads = "columns " + strings.Join(missing, ", ") + ", which are"
	}
	return fmt.Sprintf("%s: the stored row filter reads %s not in table %d's non-deleted applied schema; "+
		"revision validation reports it as an error, and enable and preview refuse the release, until %s is set to a row filter "+
		"that fits the table, or to null to clear it", stored.key, reads, stored.table, stored.key)
}

// WriteRowFilterCount writes a "Row filter" line naming count's key and table,
// then row_filters.WriteCount's "Keeps" lines for count.Count indented two
// spaces.
func WriteRowFilterCount(out io.Writer, count RowFilterCount) error {
	save := filterSave{table: count.Table, series: count.Series}
	subject := count.Key
	if count.Kept {
		subject += " (the stored filter, which the save keeps)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Row filter %s on %s:\n", subject, save.tableText())
	var lines strings.Builder
	if err := row_filters.WriteCount(&lines, "Keeps", count.Count); err != nil {
		return err
	}
	for _, line := range strings.SplitAfter(strings.TrimSuffix(lines.String(), "\n"), "\n") {
		b.WriteString("  " + line)
	}
	b.WriteString("\n")
	_, err := io.WriteString(out, b.String())
	return err
}

// WriteRowFilterCounts writes each row filter count as WriteRowFilterCount
// does, then each row filter warning as a "Warning: " line.
func (p *Plan) WriteRowFilterCounts(out io.Writer) error {
	var b strings.Builder
	for _, count := range p.RowFilterCounts {
		if err := WriteRowFilterCount(&b, count); err != nil {
			return err
		}
	}
	for _, warning := range p.RowFilterWarnings {
		fmt.Fprintf(&b, "Warning: %s\n", cellText(warning))
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// firstLine returns err's message up to its first line break.
func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}

// SheetFilterKey returns the manifest key of sheet's row filter, such as
// spec.sheets.Data.row_filter.
func SheetFilterKey(sheet string) string {
	return sheetKey(sheet) + "." + keyRowFilter
}

// SheetTableKey returns the manifest key of sheet's table, such as
// spec.sheets.Data.table.
func SheetTableKey(sheet string) string {
	return sheetKey(sheet) + "." + keyTable
}

func cellFilterKey(sheet, ref string) string {
	return "spec.cells." + cellText(sheet+"!"+ref) + ".total." + keyRowFilter
}
