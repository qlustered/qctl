package deliverables

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
)

const (
	pathCellBindings    = "/cell-bindings"
	copiedCellBindingID = "<copied-cell-binding-id>"
	wireSameRowsAs      = "same_rows_as_sheet_binding_id"
	wireCellRef         = "cell_ref"
	wireLabelCellRef    = "label_cell_ref"
	wireLiteralValue    = "literal_value"
	wireAggregateTable  = "aggregate_member_dataset_id"
	wireAggregateSeries = "aggregate_member_series_id"
	wireAggregateFunc   = "aggregate_func"
	wireAggregateColumn = "aggregate_column"
)

type plannedCell struct {
	sheet, ref string
	spec       ManifestCell
	table      *int
	series     *uuid.UUID
}

func (c *Client) resolveCells(token string, m *Manifest) ([]plannedCell, error) {
	tables := datasets.NewClient(c.baseURL, c.orgID.String(), c.verbosity)
	series := table_series.NewClient(c.baseURL, c.orgID.String(), c.verbosity)
	tableIDs, seriesIDs := map[string]int{}, map[string]uuid.UUID{}
	cells := make([]plannedCell, 0, len(m.Spec.Cells))
	for _, key := range slices.Sorted(maps.Keys(m.Spec.Cells)) {
		sheet, ref, err := splitCellKey(key)
		if err != nil {
			return nil, err
		}
		cell := plannedCell{sheet: sheet, ref: ref, spec: m.Spec.Cells[key]}
		if total := cell.spec.Total; total != nil && total.SameRowsAs == "" {
			id, ok := tableIDs[total.Table]
			if !ok {
				if id, err = tables.ResolveID(token, total.Table); err != nil {
					return nil, fmt.Errorf("spec.cells.%s.total.table: %w", cellText(key), err)
				}
				tableIDs[total.Table] = id
			}
			cell.table = &id
			if total.Series != nil {
				id, ok := seriesIDs[*total.Series]
				if !ok {
					if id, err = series.ResolveID(token, *total.Series); err != nil {
						return nil, fmt.Errorf("spec.cells.%s.total.series: %w", cellText(key), err)
					}
					seriesIDs[*total.Series] = id
				}
				cell.series = &id
			}
		}
		cells = append(cells, cell)
	}
	return cells, nil
}

// cellRequests returns cell DELETEs before sheet writes and cell saves after
// them. A mirror of a removed sheet is deleted and, if still wanted, recreated.
func (p *Plan) cellRequests(tree *api.DeliverableRevisionResponse) ([]Request, []Request, error) {
	if !p.managesCells {
		return nil, nil, nil
	}
	stored, err := cellsByKey(tree)
	if err != nil {
		return nil, nil, err
	}
	deletes, recreated := p.cellDeletes(tree, stored)
	saves, err := p.cellSaves(tree, stored, recreated)
	return deletes, saves, err
}

func cellsByKey(tree *api.DeliverableRevisionResponse) (map[string]api.CellBindingResponse, error) {
	stored := map[string]api.CellBindingResponse{}
	for _, cell := range copied(tree.CellBindings) {
		ref, err := NormalizeCellRef(cell.CellRef)
		if err != nil {
			return nil, err
		}
		key := cell.SheetName + "!" + ref
		if _, exists := stored[key]; exists {
			return nil, qerrors.NewBadUsagef("revision has multiple bindings for cell %s; remove the duplicate before applying", cellText(key))
		}
		stored[key] = cell
	}
	return stored, nil
}

func (p *Plan) cellDeletes(tree *api.DeliverableRevisionResponse, stored map[string]api.CellBindingResponse) ([]Request, map[string]api.CellBindingResponse) {
	wanted := map[string]bool{}
	for _, cell := range p.cells {
		wanted[cell.sheet+"!"+cell.ref] = true
	}
	var deletes []Request
	recreated := map[string]api.CellBindingResponse{}
	for _, key := range slices.Sorted(maps.Keys(stored)) {
		binding := stored[key]
		if !wanted[key] || p.mirroredSheetRemoved(tree, binding) {
			sheet, ref, _ := splitCellKey(key)
			r := p.cellRequest(tree, sheet, ref, &binding)
			r.Method, r.step = http.MethodDelete, stepCellDelete
			r.Note = cellLabel(sheet, ref) + " is removed before sheet writes"
			deletes = append(deletes, r)
			recreated[key] = binding
			delete(stored, key)
		}
	}
	return deletes, recreated
}

func (p *Plan) cellSaves(tree *api.DeliverableRevisionResponse, stored, recreated map[string]api.CellBindingResponse) ([]Request, error) {
	var patches, creates []Request
	for _, cell := range p.cells {
		current, exists := stored[cell.sheet+"!"+cell.ref]
		var binding *api.CellBindingResponse
		if exists {
			binding = &current
		}
		seed := binding
		if previous, ok := recreated[cell.sheet+"!"+cell.ref]; ok {
			seed = &previous
		}
		body, mirror, err := p.cellBody(tree, cell, seed)
		if err != nil {
			return nil, err
		}
		if binding != nil && !cellChanged(tree, cell, *binding) {
			continue
		}
		if err := p.refuseCellInDataRegion(tree, cell); err != nil {
			return nil, err
		}
		r := p.cellRequest(tree, cell.sheet, cell.ref, binding)
		r.Body, r.mirrorSheet, r.Note = body, mirror, cellLabel(cell.sheet, cell.ref)
		if binding == nil {
			r.Method, r.step = http.MethodPost, stepCellCreate
			creates = append(creates, r)
		} else {
			r.Method, r.step = http.MethodPatch, stepCellPatch
			if binding.NeedsConfirmation {
				r.Note += "; " + confirmsNote
			}
			patches = append(patches, r)
		}
	}
	return slices.Concat(patches, creates), nil
}

func (p *Plan) mirroredSheetRemoved(tree *api.DeliverableRevisionResponse, cell api.CellBindingResponse) bool {
	if !p.managesSheets || cell.SameRowsAsSheetBindingID == nil {
		return false
	}
	for _, sheet := range copied(tree.SheetBindings) {
		if sheet.ID == *cell.SameRowsAsSheetBindingID {
			return p.desiredSheet(sheet.SheetName) == nil
		}
	}
	return false
}

func (p *Plan) cellRequest(tree *api.DeliverableRevisionResponse, sheet, ref string, stored *api.CellBindingResponse) Request {
	revision := createdRevisionID
	if p.isTarget(tree) {
		revision = tree.ID.String()
	}
	r := Request{Path: pathRevisions + revision + pathCellBindings, Sheet: sheet, Cell: ref, key: "spec.cells." + cellText(sheet+"!"+ref)}
	if stored != nil {
		r.stored = stored.ID
		r.Path += "/" + copiedCellBindingID
		if p.isTarget(tree) {
			r.Path = pathRevisions + revision + pathCellBindings + "/" + stored.ID.String()
			r.CellBindingID = stored.ID
		}
	}
	return r
}

func (p *Plan) cellBody(tree *api.DeliverableRevisionResponse, cell plannedCell, stored *api.CellBindingResponse) (map[string]any, string, error) {
	spec := cell.spec
	body := map[string]any{
		wireSheetName: cell.sheet, wireCellRef: cell.ref,
		wireRunParam: optional(spec.RunParam), wireLiteralValue: optional(spec.Literal), wireFormatOverride: optional(spec.Format),
		wireAggregateTable: nil, wireAggregateSeries: nil, wireAggregateFunc: nil, wireAggregateColumn: nil,
		wireSameRowsAs: nil, keyRowFilter: nil,
	}
	if spec.LabelCell != nil {
		body[wireLabelCellRef] = *spec.LabelCell
	} else if stored != nil && stored.LabelCellRef != nil {
		// Canonicalizing a stored cell_ref must retain its label too.
		body[wireLabelCellRef] = *stored.LabelCellRef
	}
	source := api.DeliverableCellSourceTypeLiteral
	mirror := ""
	if spec.RunParam != nil {
		source = api.DeliverableCellSourceTypeRunParam
	} else if total := spec.Total; total != nil {
		source = api.DeliverableCellSourceTypeAggregate
		body[wireAggregateFunc], body[wireAggregateColumn] = string(total.Func), optional(total.Column)
		if total.SameRowsAs != "" {
			mirror = total.SameRowsAs
			id, err := p.mirrorID(tree, mirror)
			if err != nil {
				return nil, "", fmt.Errorf("spec.cells.%s.total.same_rows_as: %w", cellText(cell.sheet+"!"+cell.ref), err)
			}
			body[wireSameRowsAs] = id
		} else {
			body[wireAggregateTable], body[wireAggregateSeries] = *cell.table, optionalID(cell.series)
			if total.Has(keyRowFilter) {
				body[keyRowFilter] = filterBody(total.RowFilter)
			} else if stored != nil && stored.SourceType == source && stored.SameRowsAsSheetBindingID == nil && stored.RowFilter != nil {
				body[keyRowFilter] = preservedCellFilter(stored.RowFilter)
			}
		}
	}
	body[wireSourceType] = string(source)
	return body, mirror, nil
}

func preservedCellFilter(filter *api.RowFilter) *api.DescribedRowFilter {
	description := "Only some rows"
	if filter.Description != nil && strings.TrimSpace(*filter.Description) != "" {
		description = *filter.Description
	}
	return &api.DescribedRowFilter{Description: description, Conditions: filter.Conditions, Predicate: filter.Predicate}
}

func (p *Plan) mirrorID(tree *api.DeliverableRevisionResponse, name string) (any, error) {
	if p.managesSheets && p.desiredSheet(name) == nil {
		return nil, qerrors.NewBadUsagef("sheet %q is not in spec.sheets", cellText(name))
	}
	for _, sheet := range copied(tree.SheetBindings) {
		if sheet.SheetName == name {
			if p.isTarget(tree) {
				return sheet.ID.String(), nil
			}
			return copiedSheetBindingID, nil
		}
	}
	if p.desiredSheet(name) != nil {
		return createdSheetBindingID, nil
	}
	return nil, qerrors.NewBadUsagef("sheet %q has no binding on this revision; add it to spec.sheets", cellText(name))
}

// cellValues are the fields compared when deciding whether to save a cell.
type cellValues struct {
	source                           api.DeliverableCellSourceType
	runParam, literal, format, label *string
	table                            *int
	series                           *uuid.UUID
	function                         *api.DeliverableAggregateFunc
	column                           *string
	mirror                           string
}

func cellChanged(tree *api.DeliverableRevisionResponse, cell plannedCell, stored api.CellBindingResponse) bool {
	want := cellValues{
		source: api.DeliverableCellSourceTypeLiteral, runParam: cell.spec.RunParam,
		literal: cell.spec.Literal, format: cell.spec.Format, label: stored.LabelCellRef,
	}
	if cell.spec.LabelCell != nil {
		want.label = cell.spec.LabelCell
	}
	if cell.spec.RunParam != nil {
		want.source = api.DeliverableCellSourceTypeRunParam
	}
	if total := cell.spec.Total; total != nil {
		want.source = api.DeliverableCellSourceTypeAggregate
		want.table, want.series, want.function, want.column = cell.table, cell.series, &total.Func, total.Column
		want.mirror = total.SameRowsAs
	}
	have := cellValues{source: stored.SourceType, runParam: stored.RunParam, literal: stored.LiteralValue,
		format: stored.FormatOverride, label: stored.LabelCellRef, table: stored.AggregateMemberDatasetID,
		series: stored.AggregateMemberSeriesID, function: stored.AggregateFunc, column: stored.AggregateColumn}
	if stored.SameRowsAsSheetBindingID != nil {
		// An unknown id must not compare equal to a non-mirror source.
		have.mirror = stored.SameRowsAsSheetBindingID.String()
		for _, sheet := range copied(tree.SheetBindings) {
			if sheet.ID == *stored.SameRowsAsSheetBindingID {
				have.mirror = sheet.SheetName
			}
		}
	}
	if total := cell.spec.Total; total != nil && total.SameRowsAs == "" && total.Has(keyRowFilter) && filterChanged(total.RowFilter, stored.RowFilter) {
		return true
	}
	return cell.ref != stored.CellRef || !reflect.DeepEqual(want, have)
}

func (p *Plan) refuseCellInDataRegion(tree *api.DeliverableRevisionResponse, cell plannedCell) error {
	rowStart := 0
	for _, sheet := range copied(tree.SheetBindings) {
		if sheet.SheetName == cell.sheet && (!p.managesSheets || p.desiredSheet(cell.sheet) != nil) {
			rowStart = sheet.DataStartRow
		}
	}
	if sheet := p.desiredSheet(cell.sheet); sheet != nil {
		rowStart = sheet.spec.DataStartRow
	}
	parts := a1Pattern.FindStringSubmatch(cell.ref)
	row, _ := strconv.Atoi(parts[2])
	if rowStart != 0 && row >= rowStart {
		return qerrors.NewBadUsagef("spec.cells.%s: cell is inside the data region starting at row %d", cellText(cell.sheet+"!"+cell.ref), rowStart)
	}
	return nil
}

func cellLabel(sheet, ref string) string {
	return fmt.Sprintf("cell %q", cellText(sheet+"!"+ref))
}

// cellConfirmationNotes names flagged cells not saved or deleted by the plan.
func cellConfirmationNotes(slug, release string, tree *api.DeliverableRevisionResponse, cleared map[uuid.UUID]bool) []string {
	bySheet := map[string][]string{}
	for _, cell := range copied(tree.CellBindings) {
		if cell.NeedsConfirmation && !cleared[cell.ID] {
			bySheet[cell.SheetName] = append(bySheet[cell.SheetName], cell.CellRef)
		}
	}
	revision := cmdutil.ShellArg(cellText(slug)) + " --release " + cmdutil.ShellArg(cellText(release))
	var notes []string
	for _, sheet := range slices.Sorted(maps.Keys(bySheet)) {
		refs := slices.Sorted(slices.Values(bySheet[sheet]))
		flagged := "cells " + strings.Join(refs, ", ") + " need"
		if len(refs) == 1 {
			flagged = "cell " + refs[0] + " needs"
		}
		notes = append(notes, fmt.Sprintf("sheet %q %s confirmation: a template change left the bindings in place "+
			"with no label to re-anchor by; check: qctl describe deliverable-revision %s; "+
			"confirm: qctl deliverable confirm-cells %s --sheet %s --cell %s",
			cellText(sheet), cellText(flagged), revision, revision, cmdutil.ShellArg(cellText(sheet)), cmdutil.ShellArg(strings.Join(refs, ","))))
	}
	return notes
}

func (p *Plan) cellSeriesNotes(m *Manifest) []string {
	period := m.Spec.PeriodParam
	if !m.Has(keyPeriodParam) && p.Current != nil {
		period = p.Current.PeriodParam
	}
	if period != nil {
		return nil
	}
	var notes []string
	for _, cell := range p.cells {
		if cell.series != nil {
			notes = append(notes, cellLabel(cell.sheet, cell.ref)+" follows a series but spec.period_param is unset; validation requires a period parameter")
		}
	}
	return notes
}
