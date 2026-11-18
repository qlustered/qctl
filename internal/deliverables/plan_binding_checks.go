package deliverables

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

type bindingColumnRef struct {
	key, name string
	table     int
}

// loadBindingColumns reads each referenced table's applied columns once.
func (c *Client) loadBindingColumns(token string, p *Plan, requests []Request) error {
	p.tableColumns = map[int]map[string]bool{}
	for _, ref := range p.bindingColumnRefs(p.Tree, requests) {
		if _, loaded := p.tableColumns[ref.table]; loaded {
			continue
		}
		columns, err := c.appliedColumns(token, ref.table)
		if err != nil {
			return fmt.Errorf("%s: reading table %d's applied columns: %w", ref.key, ref.table, err)
		}
		p.tableColumns[ref.table] = columns
	}
	return nil
}

// appliedColumns reads the names of table's applied, non-deleted columns.
func (c *Client) appliedColumns(token string, table int) (map[string]bool, error) {
	detail, err := datasets.NewClient(c.baseURL, c.orgID.String(), c.verbosity).GetSchemaDetails(token, table)
	if err != nil {
		return nil, err
	}
	columns := map[string]bool{}
	for name, field := range detail.Fields {
		if field.Current != nil && (field.Current.IsDeleted == nil || !*field.Current.IsDeleted) {
			columns[name] = true
		}
	}
	return columns, nil
}

// bindingColumnRefs lists the table columns sent by binding saves.
func (p *Plan) bindingColumnRefs(tree *api.DeliverableRevisionResponse, requests []Request) []bindingColumnRef {
	var refs []bindingColumnRef
	for _, r := range requests {
		if r.step != stepSheetCreate && r.step != stepSheetPatch {
			continue
		}
		keys, _ := r.Body[wireSortKeys].([]map[string]any)
		for i, key := range keys {
			name, _ := key["column_name"].(string)
			refs = append(refs, bindingColumnRef{key: fmt.Sprintf("%s.%s[%d].column", sheetKey(r.Sheet), keyRowOrder, i),
				name: name, table: p.desiredSheet(r.Sheet).table})
		}
	}
	checks := p.columnsToCheck(tree, requests)
	for _, sheet := range p.sheets {
		for _, index := range checks[sheet.name] {
			column := sheet.spec.ColumnsByIndex()[index]
			if column.Column != "" {
				refs = append(refs, bindingColumnRef{key: columnKey(sheet.name, index) + ".column", name: column.Column, table: sheet.table})
			}
		}
	}
	return refs
}

func (p *Plan) refuseBindingColumns(tree *api.DeliverableRevisionResponse, requests []Request) error {
	for _, ref := range p.bindingColumnRefs(tree, requests) {
		if columns, loaded := p.tableColumns[ref.table]; loaded && !columns[ref.name] {
			return qerrors.NewBadUsagef("%s: column %q does not exist in table %d's non-deleted applied schema",
				ref.key, cellText(ref.name), ref.table)
		}
	}
	if _, available := p.templateSheets(tree); !available {
		return nil
	}
	stored := sheetsByName(tree)
	checks := p.columnsToCheck(tree, requests)
	for _, sheet := range p.sheets {
		count := stored[sheet.name].InstructionRowCount
		if sheet.spec.InstructionRowCount != nil {
			count = *sheet.spec.InstructionRowCount
		}
		for _, inventory := range tree.WorkbookInventory.Sheets {
			if inventory.Name != sheet.name {
				continue
			}
			for _, index := range checks[sheet.name] {
				hits, err := mergedRangeHits(inventory.MergedRanges, sheet.spec.DataStartRow, count, index)
				if err != nil {
					return fmt.Errorf("template sheet %q: %w", cellText(sheet.name), err)
				}
				if len(hits) > 0 {
					return qerrors.NewBadUsagef("%s: target column %s intersects merged ranges %s in the data region",
						columnKey(sheet.name, index), ColumnLetter(index), cellText(strings.Join(hits, ", ")))
				}
			}
		}
	}
	return nil
}

// columnsToCheck includes saves a sheet re-anchor may add to the plan.
func (p *Plan) columnsToCheck(tree *api.DeliverableRevisionResponse, requests []Request) map[string][]int {
	checks := map[string]map[int]bool{}
	stored := sheetsByName(tree)
	for _, sheet := range p.sheets {
		indexes := map[int]bool{}
		if p.TemplatePath != "" && !p.isTarget(tree) || sheet.relayouts(stored[sheet.name]) {
			for index := range sheet.spec.ColumnsByIndex() {
				indexes[index] = true
			}
		}
		checks[sheet.name] = indexes
	}
	for _, r := range requests {
		if r.step == stepColumnCreate || r.step == stepColumnPatch {
			checks[r.Sheet][r.Column] = true
		}
	}
	result := map[string][]int{}
	for sheet, indexes := range checks {
		result[sheet] = slices.Sorted(maps.Keys(indexes))
	}
	return result
}

// mergedRangeHits returns ranges intersecting a column outside instruction rows.
func mergedRangeHits(ranges []string, dataStart, instructionCount, column int) ([]string, error) {
	var hits []string
	for _, ref := range ranges {
		start, end, pair := strings.Cut(ref, ":")
		if !pair {
			end = start
		}
		minColumn, minRow, err := rangeCell(start)
		if err != nil {
			return nil, err
		}
		maxColumn, maxRow, err := rangeCell(end)
		if err != nil {
			return nil, err
		}
		instructions := minRow >= dataStart && maxRow < dataStart+instructionCount
		if maxRow >= dataStart && minColumn <= column && column <= maxColumn && !instructions {
			hits = append(hits, ref)
		}
	}
	return hits, nil
}

func rangeCell(ref string) (int, int, error) {
	parts := a1Pattern.FindStringSubmatch(ref)
	if parts != nil {
		column, ok := columnIndex(parts[1])
		row, err := strconv.Atoi(parts[2])
		if ok && err == nil && row > 0 {
			return column, row, nil
		}
	}
	return 0, 0, fmt.Errorf("invalid merged range cell %q in template inventory", cellText(ref))
}
