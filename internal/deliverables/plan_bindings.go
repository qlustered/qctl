package deliverables

import (
	"cmp"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
)

// Placeholders for the binding ids apply learns as it goes.
const (
	createdRevisionID     = "<created-revision-id>"
	createdSheetBindingID = "<created-sheet-binding-id>"
	copiedSheetBindingID  = "<copied-sheet-binding-id>"
	copiedColumnBindingID = "<copied-column-binding-id>"
)

const (
	pathRevisions      = "/deliverable-revisions/"
	pathSheetBindings  = "/sheet-bindings"
	pathSheetBinding   = "/deliverable-sheet-bindings/"
	pathColumnBindings = "/column-bindings"
)

// Keys of a sheet or column binding request body that no manifest key shares.
const (
	wireMemberDatasetID   = "member_dataset_id"
	wireMemberSeriesID    = "member_series_id"
	wireSheetName         = "sheet_name"
	wireSortKeys          = "sort_keys"
	wireTargetColumnIndex = "target_column_index"
	wireSourceType        = "source_type"
	wireSourceColumn      = "source_column"
	wireRunParam          = "run_param"
	wireFormatOverride    = "format_override"
)

const confirmsNote = "also confirms it: the binding needs confirmation, which any save clears"

// plannedSheet is a spec.sheets entry with its table and series resolved.
type plannedSheet struct {
	name string
	spec ManifestSheet
	// table is the resolved table id.
	table int
	// series is the resolved series; nil when spec.series is absent or null.
	series *uuid.UUID
}

// sheetRef is the sheet binding a write names: its id, or uuid.Nil and the
// placeholder its path carries instead.
type sheetRef struct {
	id   uuid.UUID
	path string
}

// planSheets resolves and plans the manifest's sheet and cell bindings.
func (c *Client) planSheets(token string, p *Plan, m *Manifest) ([]Request, error) {
	sheets, err := c.resolveSheets(token, m)
	if err != nil {
		return nil, err
	}
	p.sheets, p.managesSheets = sheets, m.Has(keySheets)
	if p.cells, err = c.resolveCells(token, m); err != nil {
		return nil, err
	}
	p.managesCells = m.Has(keyCells)
	if p.Tree, err = c.baseTree(token, p); err != nil {
		return nil, err
	}
	requests, err := p.bindingRequests(p.Tree)
	if err != nil {
		return nil, err
	}
	if err := c.loadBindingColumns(token, p, requests); err != nil {
		return nil, err
	}
	if err := p.refuseBindingRequests(p.Tree, requests); err != nil {
		return nil, err
	}
	if err := c.countRowFilters(token, p, requests); err != nil {
		return nil, err
	}
	c.warnMissingFilterColumns(token, p, requests)
	p.Removals = removalLabels(requests)
	p.MayRemove = p.mayRemove(p.Tree)
	p.MayRemoveCells = p.managesCells && p.CopyFrom != nil && p.TemplatePath != "" && len(copied(p.Tree.CellBindings)) > 0
	p.Notes = append(p.Notes, p.sheetNotes(m, p.Tree)...)
	p.Notes = append(p.Notes, p.cellSeriesNotes(m)...)
	p.confirmations = p.draftConfirmations(p.Tree, requests)
	return requests, nil
}

// resolveSheets resolves each sheet's table, and its series when written and
// non-null, once per distinct reference, and returns the sheets sorted by
// name.
func (c *Client) resolveSheets(token string, m *Manifest) ([]plannedSheet, error) {
	tables := datasets.NewClient(c.baseURL, c.orgID.String(), c.verbosity)
	series := table_series.NewClient(c.baseURL, c.orgID.String(), c.verbosity)
	tableIDs, seriesIDs := map[string]int{}, map[string]uuid.UUID{}
	sheets := make([]plannedSheet, 0, len(m.Spec.Sheets))
	for _, name := range slices.Sorted(maps.Keys(m.Spec.Sheets)) {
		spec := m.Spec.Sheets[name]
		table, ok := tableIDs[spec.Table]
		if !ok {
			id, err := tables.ResolveID(token, spec.Table)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", SheetTableKey(name), err)
			}
			table, tableIDs[spec.Table] = id, id
		}
		sheet := plannedSheet{name: name, spec: spec, table: table}
		if spec.Has(keySeries) && spec.Series != nil {
			id, ok := seriesIDs[*spec.Series]
			if !ok {
				var err error
				if id, err = series.ResolveID(token, *spec.Series); err != nil {
					return nil, fmt.Errorf("spec.sheets.%s.%s: %w", cellText(name), keySeries, err)
				}
				seriesIDs[*spec.Series] = id
			}
			sheet.series = &id
		}
		sheets = append(sheets, sheet)
	}
	return sheets, nil
}

// baseTree reads p.Revision's binding tree, else p.CopyFrom's, and returns an
// empty tree when there is neither.
func (c *Client) baseTree(token string, p *Plan) (*api.DeliverableRevisionResponse, error) {
	from := p.Revision
	if from == nil {
		from = p.CopyFrom
	}
	if from == nil {
		return &api.DeliverableRevisionResponse{}, nil
	}
	detail, err := c.GetRevision(token, from.ID)
	if err != nil {
		return nil, err
	}
	return detail.DeliverableRevisionResponse, nil
}

// bindingRequests plans cell removals, sheet and column writes, then cell saves.
func (p *Plan) bindingRequests(tree *api.DeliverableRevisionResponse) ([]Request, error) {
	if !p.managesSheets && !p.managesCells {
		return nil, nil
	}
	known := p.isTarget(tree)
	revision := createdRevisionID
	if known {
		revision = tree.ID.String()
	}
	stored := sheetsByName(tree)
	var requests []Request
	if p.managesSheets {
		for _, name := range slices.Sorted(maps.Keys(stored)) {
			if p.desiredSheet(name) == nil {
				requests = append(requests, sheetDelete(revision, treeSheet(known, stored[name]), stored[name]))
			}
		}
	}
	for _, sheet := range p.sheets {
		requests = append(requests, p.sheetRequests(sheet, revision, known, stored)...)
	}
	deletes, cells, err := p.cellRequests(tree)
	if err != nil {
		return nil, err
	}
	requests = slices.Concat(deletes, requests, cells)
	if err := p.refuseBindingRequests(tree, requests); err != nil {
		return nil, err
	}
	return requests, nil
}

// columnRequests returns the column binding writes that make current, the
// column bindings of sheet binding sheetBindingID, match the columns
// spec.sheets lists for sheet: DELETEs, then PATCHes in place, then POSTs,
// each ascending by index. It returns none when spec.sheets does not list
// sheet's columns. A uuid.Nil sheetBindingID puts the created-sheet
// placeholder in the paths.
func (p *Plan) columnRequests(sheet string, sheetBindingID uuid.UUID, current []api.ColumnBindingResponse) []Request {
	ref := sheetRef{id: sheetBindingID, path: createdSheetBindingID}
	if sheetBindingID != uuid.Nil {
		ref.path = sheetBindingID.String()
	}
	return p.sheetColumns(sheet, ref, current)
}

// sheetRequests returns sheet's POST, or its PATCH when it differs from the
// stored sheet of its name, followed by its column writes.
func (p *Plan) sheetRequests(sheet plannedSheet, revision string, known bool, stored map[string]api.SheetBindingResponse) []Request {
	current, ok := stored[sheet.name]
	if !ok {
		return append([]Request{sheet.create(revision)}, p.columnRequests(sheet.name, uuid.Nil, nil)...)
	}
	var requests []Request
	ref := treeSheet(known, current)
	if patch := sheet.patch(revision, ref, current); patch != nil {
		requests = append(requests, *patch)
	}
	if known {
		return append(requests, p.columnRequests(sheet.name, current.ID, copied(current.ColumnBindings))...)
	}
	return append(requests, p.sheetColumns(sheet.name, ref, copied(current.ColumnBindings))...)
}

// sheetColumns is columnRequests for the sheet binding ref names. The ids in
// current are written only when ref has an id.
func (p *Plan) sheetColumns(name string, ref sheetRef, current []api.ColumnBindingResponse) []Request {
	sheet := p.desiredSheet(name)
	if sheet == nil || sheet.spec.ColumnsByIndex() == nil {
		return nil
	}
	desired := sheet.spec.ColumnsByIndex()
	route := pathSheetBinding + ref.path + pathColumnBindings
	stored := make(map[int]api.ColumnBindingResponse, len(current))
	for _, binding := range current {
		stored[binding.TargetColumnIndex] = binding
	}
	var deletes, patches, posts []Request
	for _, index := range slices.Sorted(maps.Keys(stored)) {
		r, ok := columnWrite(name, ref, route, stored[index], desired)
		if !ok {
			continue
		}
		if r.step == stepColumnDelete {
			deletes = append(deletes, r)
		} else {
			patches = append(patches, r)
		}
	}
	for _, index := range slices.Sorted(maps.Keys(desired)) {
		if _, ok := stored[index]; !ok {
			posts = append(posts, Request{Method: http.MethodPost, Path: route, Body: manifestColumnBody(index, desired[index]),
				Note: columnLabel(name, index), Sheet: name, Column: index, SheetBindingID: ref.id, step: stepColumnCreate,
				key: columnKey(name, index)})
		}
	}
	return slices.Concat(deletes, patches, posts)
}

// columnWrite returns the DELETE of binding when desired lacks its index, or
// its PATCH when desired's column there differs, and false when neither.
func columnWrite(sheet string, ref sheetRef, route string, binding api.ColumnBindingResponse, desired map[int]ManifestColumn) (Request, bool) {
	index := binding.TargetColumnIndex
	column, wanted := desired[index]
	if wanted && !manifestColumnChanged(column, binding) {
		return Request{}, false
	}
	r := Request{Path: route + "/" + copiedColumnBindingID, Note: columnLabel(sheet, index), Sheet: sheet, Column: index,
		SheetBindingID: ref.id, key: columnKey(sheet, index), stored: binding.ID}
	if ref.id != uuid.Nil {
		r.Path, r.ColumnBindingID = route+"/"+binding.ID.String(), binding.ID
	}
	if !wanted {
		r.Method, r.step = http.MethodDelete, stepColumnDelete
		r.Note += fmt.Sprintf(" is not in spec.sheets.%s.%s", cellText(sheet), keyColumns)
		return r, true
	}
	r.Method, r.step, r.Body = http.MethodPatch, stepColumnPatch, columnUpsertBody(index, column, binding)
	if binding.NeedsConfirmation {
		r.Note += "; " + confirmsNote
	}
	return r, true
}

// create returns s's POST, which sends instruction_row_count, the series, the
// row order and the row filter only when spec.sheets writes them.
func (s plannedSheet) create(revision string) Request {
	body := map[string]any{wireMemberDatasetID: s.table, wireSheetName: s.name, keyDataStartRow: s.spec.DataStartRow,
		keyHeaderRows: append([]int{}, s.spec.HeaderRows...)}
	if s.spec.Has(keyInstructionRowCount) && s.spec.InstructionRowCount != nil {
		body[keyInstructionRowCount] = *s.spec.InstructionRowCount
	}
	if s.series != nil {
		body[wireMemberSeriesID] = *s.series
	}
	if s.spec.Has(keyRowOrder) {
		body[wireSortKeys] = sortKeysBody(s.spec.RowOrder)
	}
	if s.spec.RowFilter != nil {
		body[keyRowFilter] = row_filters.Saved(s.spec.RowFilter)
	}
	return Request{Method: http.MethodPost, Path: pathRevisions + revision + pathSheetBindings, Body: body,
		Sheet: s.name, step: stepSheetCreate, key: sheetKey(s.name)}
}

// patch returns s's PATCH of stored, or nil when s matches it. The body
// carries every sheet binding key but row_filter, with stored's value where
// spec.sheets does not write one, and row_filter only when spec.sheets writes
// it, null included.
func (s plannedSheet) patch(revision string, ref sheetRef, stored api.SheetBindingResponse) *Request {
	changed := s.changes(stored)
	if len(changed) == 0 {
		return nil
	}
	count := stored.InstructionRowCount
	if s.spec.Has(keyInstructionRowCount) && s.spec.InstructionRowCount != nil {
		count = *s.spec.InstructionRowCount
	}
	series := optionalID(stored.MemberSeriesID)
	if s.spec.Has(keySeries) {
		series = optionalID(s.series)
	}
	sortKeys := storedSortKeysBody(stored.SortKeys)
	if s.spec.Has(keyRowOrder) {
		sortKeys = sortKeysBody(s.spec.RowOrder)
	}
	note := "changes " + strings.Join(changed, ", ")
	if s.relayouts(stored) {
		note += "; the server re-anchors the sheet's column bindings by their header labels"
		if s.spec.ColumnsByIndex() != nil {
			note += ", and apply plans its column writes again from the response"
		}
	}
	body := map[string]any{
		wireMemberDatasetID: s.table, wireSheetName: s.name, keyDataStartRow: s.spec.DataStartRow,
		keyHeaderRows: append([]int{}, s.spec.HeaderRows...), keyInstructionRowCount: count,
		wireMemberSeriesID: series, wireSortKeys: sortKeys,
	}
	if s.spec.Has(keyRowFilter) {
		body[keyRowFilter] = filterBody(s.spec.RowFilter)
	}
	return &Request{Method: http.MethodPatch, Path: pathRevisions + revision + pathSheetBindings + "/" + ref.path, Note: note, Body: body,
		Sheet: s.name, SheetBindingID: ref.id, step: stepSheetPatch, key: sheetKey(s.name) + "." + changed[0], stored: stored.ID}
}

// changes returns the keys of s whose values stored does not have, in
// manifest order. A key spec.sheets does not write never differs.
func (s plannedSheet) changes(stored api.SheetBindingResponse) []string {
	var changed []string
	for _, key := range []struct {
		name    string
		differs bool
	}{
		{keyTable, s.table != stored.MemberDatasetID},
		{keySeries, s.spec.Has(keySeries) && !sameValue(s.series, stored.MemberSeriesID)},
		{keyHeaderRows, !slices.Equal(s.spec.HeaderRows, stored.HeaderRows)},
		{keyDataStartRow, s.spec.DataStartRow != stored.DataStartRow},
		{keyInstructionRowCount, s.spec.Has(keyInstructionRowCount) && !sameValue(s.spec.InstructionRowCount, &stored.InstructionRowCount)},
		{keyRowOrder, s.spec.Has(keyRowOrder) && !sameRowOrder(s.spec.RowOrder, stored.SortKeys)},
		{keyRowFilter, s.spec.Has(keyRowFilter) && filterChanged(s.spec.RowFilter, stored.RowFilter)},
	} {
		if key.differs {
			changed = append(changed, key.name)
		}
	}
	return changed
}

// relayouts reports whether s changes stored's header_rows or data_start_row.
func (s plannedSheet) relayouts(stored api.SheetBindingResponse) bool {
	return !slices.Equal(s.spec.HeaderRows, stored.HeaderRows) || s.spec.DataStartRow != stored.DataStartRow
}

// refuseBindingRequests validates planned binding writes.
func (p *Plan) refuseBindingRequests(tree *api.DeliverableRevisionResponse, requests []Request) error {
	if len(requests) == 0 {
		return p.refuseBindingColumns(tree, requests)
	}
	if rev := p.Revision; rev != nil && rev.State != api.DeliverableRevisionStateDraft {
		kind := "sheet"
		if requests[0].Cell != "" {
			kind = "cell"
		}
		return qerrors.NewBadUsagef("%s differs from the %s bindings of deliverable/%s release %q, which is %s and cannot change; bump spec.release",
			requests[0].key, kind, terminal.Sanitize(p.Slug), terminal.Sanitize(rev.Release), terminal.Sanitize(string(rev.State)))
	}
	if err := p.refuseRepeatedFilterDescriptions(tree); err != nil {
		return err
	}
	if tree.SkeletonID == nil && p.TemplatePath == "" && slices.ContainsFunc(requests, func(r Request) bool { return r.Method != http.MethodDelete }) {
		return p.noTemplate()
	}
	if names, ok := p.templateSheets(tree); ok {
		for _, r := range requests {
			if r.Method != http.MethodDelete && !slices.Contains(names, r.Sheet) {
				return p.missingSheet(r.Sheet, names)
			}
		}
	}
	if err := p.refuseBindingColumns(tree, requests); err != nil {
		return err
	}
	return refuseMirroredSheetRemovals(tree, requests)
}

// refuseRepeatedFilterDescriptions refuses a row filter spec.sheets or
// spec.cells writes that keeps other rows than the stored filter of its
// binding in tree under the same description.
func (p *Plan) refuseRepeatedFilterDescriptions(tree *api.DeliverableRevisionResponse) error {
	refusal := "%s keeps different rows from the stored filter but repeats its description %q; describe which rows the new filter keeps"
	stored := sheetsByName(tree)
	for _, sheet := range p.sheets {
		if current, ok := stored[sheet.name]; ok && sheet.spec.Has(keyRowFilter) && repeatsDescription(sheet.spec.RowFilter, current.RowFilter) {
			return qerrors.NewBadUsagef(refusal, SheetFilterKey(sheet.name), cellText(strings.TrimSpace(sheet.spec.RowFilter.DescriptionText())))
		}
	}
	if !p.managesCells {
		return nil
	}
	cells, err := cellsByKey(tree)
	if err != nil {
		return err
	}
	for _, cell := range p.cells {
		total := cell.spec.Total
		if current, ok := cells[cell.sheet+"!"+cell.ref]; ok && total != nil && total.Has(keyRowFilter) && repeatsDescription(total.RowFilter, current.RowFilter) {
			return qerrors.NewBadUsagef(refusal, "spec.cells."+cellText(cell.sheet+"!"+cell.ref)+".total."+keyRowFilter,
				cellText(strings.TrimSpace(total.RowFilter.DescriptionText())))
		}
	}
	return nil
}

func refuseMirroredSheetRemovals(tree *api.DeliverableRevisionResponse, requests []Request) error {
	remaining := *tree
	kept := slices.DeleteFunc(copied(tree.CellBindings), func(cell api.CellBindingResponse) bool {
		return slices.ContainsFunc(requests, func(request Request) bool { return request.step == stepCellDelete && request.stored == cell.ID })
	})
	remaining.CellBindings = &kept
	for _, r := range requests {
		if r.step != stepSheetDelete {
			continue
		}
		if refs := mirrorRefs(&remaining, r.stored); len(refs) > 0 {
			return qerrors.NewBadUsagef("spec.sheets has no sheet %q, but cell bindings %s total its rows; the server refuses to delete a sheet "+
				"a cell binding mirrors: keep the sheet in spec.sheets, or bind those cells elsewhere first", terminal.Sanitize(r.Sheet), strings.Join(refs, ", "))
		}
	}
	return nil
}

// noTemplate reports that binding saves require a template.
func (p *Plan) noTemplate() error {
	deliverable := terminal.Sanitize(p.Slug)
	key, kind := "spec.sheets", "sheet"
	if !p.managesSheets {
		key, kind = "spec.cells", "cell"
	}
	if rev := p.Revision; rev != nil {
		return qerrors.NewBadUsagef("%s writes %s bindings to deliverable/%s draft release %q, which has no template; "+
			"upload one first: qctl upload deliverable-template FILE --deliverable %s --release %s",
			key, kind, deliverable, terminal.Sanitize(rev.Release), cmdutil.ShellArg(deliverable), cmdutil.ShellArg(releaseSelector(rev)))
	}
	return qerrors.NewBadUsagef("%s writes %s bindings to deliverable/%s release %q, which apply creates without a template; set spec.template",
		key, kind, deliverable, terminal.Sanitize(p.Release))
}

// isTarget reports whether tree is the release the binding writes go to: it
// has an id, and it is not p.CopyFrom's.
func (p *Plan) isTarget(tree *api.DeliverableRevisionResponse) bool {
	return tree.ID != uuid.Nil && (p.CopyFrom == nil || tree.ID != p.CopyFrom.ID)
}

// templateSheets returns the sheet names of tree's template inventory, and
// false when tree has none or stands in for a release apply creates with
// spec.template.
func (p *Plan) templateSheets(tree *api.DeliverableRevisionResponse) ([]string, bool) {
	if tree.WorkbookInventory == nil || p.TemplatePath != "" && !p.isTarget(tree) {
		return nil, false
	}
	names := make([]string, 0, len(tree.WorkbookInventory.Sheets))
	for _, sheet := range tree.WorkbookInventory.Sheets {
		names = append(names, sheet.Name)
	}
	return names, true
}

// missingSheet is the refusal of a write naming sheet, which the template
// whose sheets are names lacks.
func (p *Plan) missingSheet(sheet string, names []string) error {
	release := p.Release
	if p.Revision != nil {
		release = p.Revision.Release
	}
	listed := "it has no sheets"
	if len(names) > 0 {
		quoted := make([]string, len(names))
		for i, name := range names {
			quoted[i] = fmt.Sprintf("%q", terminal.Sanitize(name))
		}
		listed = "its sheets are " + strings.Join(quoted, ", ")
	}
	return qerrors.NewBadUsagef("%s: the template of deliverable/%s release %q has no sheet %q, and the server refuses a binding to a sheet "+
		"its template lacks; sheet names match exactly, case included, and %s", sheetKey(sheet), terminal.Sanitize(p.Slug),
		terminal.Sanitize(release), terminal.Sanitize(sheet), listed)
}

// mirrorRefs returns Sheet!A1 for each cell binding of tree whose
// same_rows_as_sheet_binding_id is sheetBindingID, sorted.
func mirrorRefs(tree *api.DeliverableRevisionResponse, sheetBindingID uuid.UUID) []string {
	var refs []string
	for _, cell := range copied(tree.CellBindings) {
		if cell.SameRowsAsSheetBindingID != nil && *cell.SameRowsAsSheetBindingID == sheetBindingID {
			refs = append(refs, terminal.Sanitize(cell.SheetName)+"!"+terminal.Sanitize(cell.CellRef))
		}
	}
	slices.Sort(refs)
	return refs
}

// mayRemove reports whether the server may re-anchor the column bindings of a
// tree sheet whose columns spec.sheets lists before apply writes them: on a
// header_rows or data_start_row PATCH, or when a copied release gets
// spec.template.
func (p *Plan) mayRemove(tree *api.DeliverableRevisionResponse) bool {
	replaced := p.CopyFrom != nil && p.TemplatePath != ""
	stored := sheetsByName(tree)
	for _, sheet := range p.sheets {
		current, ok := stored[sheet.name]
		if ok && sheet.spec.ColumnsByIndex() != nil && len(copied(current.ColumnBindings)) > 0 && (replaced || sheet.relayouts(current)) {
			return true
		}
	}
	return false
}

// sheetNotes returns the notes on the binding writes: a template replacing a
// copied release's, and each sheet following a series while the deliverable
// will have no period_param.
func (p *Plan) sheetNotes(m *Manifest, tree *api.DeliverableRevisionResponse) []string {
	var notes []string
	if p.CopyFrom != nil && p.TemplatePath != "" && len(copied(tree.SheetBindings)) > 0 {
		notes = append(notes, fmt.Sprintf("spec.template replaces the template the copy of release %q starts with: the server may re-anchor "+
			"the copied column bindings, and apply plans their writes from the created release", terminal.Sanitize(p.CopyFrom.Release)))
	}
	return append(notes, p.seriesNotes(m, tree)...)
}

// draftConfirmations returns the confirmation notes of tree, leaving out the
// bindings requests save or delete, or none when p.Revision is not a
// draft.
func (p *Plan) draftConfirmations(tree *api.DeliverableRevisionResponse, requests []Request) []string {
	if p.Revision != nil && p.Revision.State != api.DeliverableRevisionStateDraft {
		return nil
	}
	release := p.Release
	if p.Revision != nil {
		release = releaseSelector(p.Revision)
	}
	cleared := clearedBindings(tree, requests)
	return slices.Concat(confirmationNotes(p.Slug, release, tree, cleared), cellConfirmationNotes(p.Slug, release, tree, cleared))
}

// seriesNotes notes each planned sheet that follows a table series, as
// written or as stored in tree, when the deliverable's period_param, as
// written or as read, is unset.
func (p *Plan) seriesNotes(m *Manifest, tree *api.DeliverableRevisionResponse) []string {
	var period *string
	if p.Current != nil {
		period = p.Current.PeriodParam
	}
	if m.Has(keyPeriodParam) {
		period = m.Spec.PeriodParam
	}
	if period != nil && *period != "" {
		return nil
	}
	stored := sheetsByName(tree)
	var notes []string
	for _, sheet := range p.sheets {
		series := sheet.series
		if !sheet.spec.Has(keySeries) {
			series = stored[sheet.name].MemberSeriesID
		}
		if series != nil {
			notes = append(notes, fmt.Sprintf("sheet %q follows a table series, but the deliverable has no period_param: "+
				"validate reports an error until spec.period_param is set", terminal.Sanitize(sheet.name)))
		}
	}
	return notes
}

// confirmationNotes returns one note per sheet of tree, by name, whose column
// bindings that need confirmation are not all in cleared, naming the rest by
// letter with the commands that check and confirm them. release is the
// --release value of those commands.
func confirmationNotes(slug, release string, tree *api.DeliverableRevisionResponse, cleared map[uuid.UUID]bool) []string {
	sheets := copied(tree.SheetBindings)
	slices.SortFunc(sheets, func(a, b api.SheetBindingResponse) int { return cmp.Compare(a.SheetName, b.SheetName) })
	var notes []string
	for _, sheet := range sheets {
		columns := copied(sheet.ColumnBindings)
		slices.SortFunc(columns, func(a, b api.ColumnBindingResponse) int { return cmp.Compare(a.TargetColumnIndex, b.TargetColumnIndex) })
		var letters []string
		for _, column := range columns {
			if column.NeedsConfirmation && !cleared[column.ID] {
				letters = append(letters, ColumnLetter(column.TargetColumnIndex))
			}
		}
		if len(letters) == 0 {
			continue
		}
		flagged, them := "columns "+strings.Join(letters, ", ")+" need", "them"
		if len(letters) == 1 {
			flagged, them = "column "+letters[0]+" needs", "it"
		}
		revision := cmdutil.ShellArg(terminal.Sanitize(slug)) + " --release " + cmdutil.ShellArg(terminal.Sanitize(release))
		notes = append(notes, fmt.Sprintf("sheet %q %s confirmation: a template change or a sheet rename left %s in place "+
			"with no header label to re-anchor by; check: qctl describe deliverable-revision %s; "+
			"confirm: qctl deliverable confirm-columns %s --sheet %s --column %s",
			terminal.Sanitize(sheet.SheetName), flagged, them, revision, revision, cmdutil.ShellArg(terminal.Sanitize(sheet.SheetName)),
			strings.Join(letters, ",")))
	}
	return notes
}

// clearedBindings returns the ids of tree's column and cell bindings that requests,
// planned against tree, save or delete.
func clearedBindings(tree *api.DeliverableRevisionResponse, requests []Request) map[uuid.UUID]bool {
	cleared := map[uuid.UUID]bool{}
	for _, r := range requests {
		switch r.step {
		case stepColumnDelete, stepColumnPatch, stepCellDelete, stepCellPatch:
			cleared[r.stored] = true
		case stepSheetDelete:
			for _, sheet := range copied(tree.SheetBindings) {
				if sheet.ID != r.stored {
					continue
				}
				for _, column := range copied(sheet.ColumnBindings) {
					cleared[column.ID] = true
				}
			}
		}
	}
	return cleared
}

// removalLabels labels each binding DELETE in requests.
func removalLabels(requests []Request) []string {
	var labels []string
	for _, r := range requests {
		switch r.step {
		case stepSheetDelete:
			labels = append(labels, sheetLabel(r.Sheet))
		case stepColumnDelete:
			labels = append(labels, columnLabel(r.Sheet, r.Column))
		case stepCellDelete:
			labels = append(labels, cellLabel(r.Sheet, r.Cell))
		}
	}
	return labels
}

// sheetDelete returns the DELETE of stored, the sheet binding ref names.
func sheetDelete(revision string, ref sheetRef, stored api.SheetBindingResponse) Request {
	note := sheetLabel(stored.SheetName) + " is not in spec.sheets"
	switch n := len(copied(stored.ColumnBindings)); n {
	case 0:
	case 1:
		note += "; its 1 column binding goes with it"
	default:
		note += fmt.Sprintf("; its %d column bindings go with it", n)
	}
	return Request{Method: http.MethodDelete, Path: pathRevisions + revision + pathSheetBindings + "/" + ref.path, Note: note,
		Sheet: stored.SheetName, SheetBindingID: ref.id, step: stepSheetDelete, key: sheetKey(stored.SheetName), stored: stored.ID}
}

// treeSheet returns the ref of a tree sheet binding: its id when known, and
// the copied-sheet placeholder otherwise.
func treeSheet(known bool, sheet api.SheetBindingResponse) sheetRef {
	if known {
		return sheetRef{id: sheet.ID, path: sheet.ID.String()}
	}
	return sheetRef{path: copiedSheetBindingID}
}

// desiredSheet returns the planned sheet named name, or nil.
func (p *Plan) desiredSheet(name string) *plannedSheet {
	for i := range p.sheets {
		if p.sheets[i].name == name {
			return &p.sheets[i]
		}
	}
	return nil
}

// sheetsByName returns tree's sheet bindings keyed by sheet name.
func sheetsByName(tree *api.DeliverableRevisionResponse) map[string]api.SheetBindingResponse {
	sheets := map[string]api.SheetBindingResponse{}
	for _, sheet := range copied(tree.SheetBindings) {
		sheets[sheet.SheetName] = sheet
	}
	return sheets
}

// manifestColumnSource returns column's source_type, source_column and
// run_param, with unused source fields nil.
func manifestColumnSource(column ManifestColumn) (api.DeliverableColumnSourceType, *string, *string) {
	if column.Literal != nil {
		return api.DeliverableColumnSourceTypeLiteral, nil, nil
	}
	if column.RunParam != "" {
		return api.DeliverableColumnSourceTypeRunParam, nil, &column.RunParam
	}
	return api.DeliverableColumnSourceTypeColumn, &column.Column, nil
}

// manifestColumnChanged reports whether column's source or format differs
// from binding's.
func manifestColumnChanged(column ManifestColumn, binding api.ColumnBindingResponse) bool {
	source, sourceColumn, runParam := manifestColumnSource(column)
	return binding.SourceType != source || !sameValue(binding.SourceColumn, sourceColumn) ||
		!sameValue(binding.RunParam, runParam) || !sameValue(binding.LiteralValue, column.Literal) || !sameValue(binding.FormatOverride, column.Format)
}

// manifestColumnBody is the whole column binding that column at index
// describes, null for the unused source key and for an absent format.
func manifestColumnBody(index int, column ManifestColumn) map[string]any {
	source, sourceColumn, runParam := manifestColumnSource(column)
	body := map[string]any{
		wireTargetColumnIndex: index, wireSourceType: string(source), wireSourceColumn: optional(sourceColumn),
		wireRunParam: optional(runParam), wireFormatOverride: optional(column.Format),
	}
	if column.Literal != nil {
		body[wireLiteralValue] = *column.Literal
	}
	return body
}

func columnUpsertBody(index int, column ManifestColumn, binding api.ColumnBindingResponse) map[string]any {
	body := manifestColumnBody(index, column)
	if binding.SourceType == api.DeliverableColumnSourceTypeLiteral && column.Literal == nil {
		body[wireLiteralValue] = nil
	}
	return body
}

// sortKeysBody returns keys as sort_keys, positioned by list index from 0.
func sortKeysBody(keys []ManifestSortKey) []map[string]any {
	body := make([]map[string]any, 0, len(keys))
	for i, key := range keys {
		body = append(body, sortKeyBody(i, key.Column, key.Descending))
	}
	return body
}

// storedSortKeysBody returns keys as sort_keys by position, positions kept.
func storedSortKeysBody(keys *[]api.SortKeySchema) []map[string]any {
	sorted := sortedKeys(keys)
	body := make([]map[string]any, 0, len(sorted))
	for _, key := range sorted {
		body = append(body, sortKeyBody(key.Position, key.ColumnName, key.Descending != nil && *key.Descending))
	}
	return body
}

func sortKeyBody(position int, column string, descending bool) map[string]any {
	return map[string]any{"position": position, "column_name": column, "descending": descending}
}

// sameRowOrder reports whether want lists stored's columns and directions in
// stored's position order.
func sameRowOrder(want []ManifestSortKey, stored *[]api.SortKeySchema) bool {
	return slices.EqualFunc(want, sortedKeys(stored), func(w ManifestSortKey, key api.SortKeySchema) bool {
		return w.Column == key.ColumnName && w.Descending == (key.Descending != nil && *key.Descending)
	})
}

// filterChanged reports whether want, a written row filter or nil for a
// written null, and stored differ: only one is nil, their rows differ, or
// their descriptions differ once trimmed.
func filterChanged(want *row_filters.Filter, stored *api.RowFilter) bool {
	have := row_filters.FromAPI(stored)
	if want == nil || have == nil {
		return want != have
	}
	return !row_filters.SameRows(want, have) || strings.TrimSpace(want.DescriptionText()) != strings.TrimSpace(have.DescriptionText())
}

// repeatsDescription reports whether want and stored are both row filters,
// keeping different rows under descriptions that match once trimmed.
func repeatsDescription(want *row_filters.Filter, stored *api.RowFilter) bool {
	have := row_filters.FromAPI(stored)
	return want != nil && have != nil && !row_filters.SameRows(want, have) &&
		strings.TrimSpace(want.DescriptionText()) == strings.TrimSpace(have.DescriptionText())
}

// filterBody returns f as a saved row filter, or nil when f is nil.
func filterBody(f *row_filters.Filter) any {
	if f == nil {
		return nil
	}
	return row_filters.Saved(f)
}

// sortedKeys returns a copy of keys sorted by position.
func sortedKeys(keys *[]api.SortKeySchema) []api.SortKeySchema {
	sorted := copied(keys)
	slices.SortFunc(sorted, func(a, b api.SortKeySchema) int { return cmp.Compare(a.Position, b.Position) })
	return sorted
}

// sameValue reports whether a and b are both nil or point to equal values.
func sameValue[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// optionalID returns *id, or nil when id is nil.
func optionalID(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return *id
}

func sheetKey(sheet string) string {
	return "spec.sheets." + cellText(sheet)
}

func columnKey(sheet string, index int) string {
	return sheetKey(sheet) + "." + keyColumns + "." + ColumnLetter(index)
}

func sheetLabel(sheet string) string {
	return fmt.Sprintf("sheet %q", terminal.Sanitize(sheet))
}

func columnLabel(sheet string, index int) string {
	return sheetLabel(sheet) + " column " + ColumnLetter(index)
}
