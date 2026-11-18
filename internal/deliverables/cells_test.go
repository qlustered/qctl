package deliverables

import (
	"encoding/json"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const cellTestID = "5d000000-0000-4000-8000-000000000001"

func cellManifest(t *testing.T, cells string, sheets ...string) *Manifest {
	t.Helper()
	doc := deliverableManifestHeader + deliverableManifestBasics + "  release: v3\n  cells: " + cells + "\n"
	if len(sheets) > 0 {
		doc += "  sheets:\n" + strings.Join(sheets, "")
	}
	m, err := ParseManifest([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func cellPlan(t *testing.T, m *Manifest) *Plan {
	t.Helper()
	p := bpPlanWith(t, m)
	p.managesSheets, p.managesCells = m.Has(keySheets), m.Has(keyCells)
	for _, key := range slices.Sorted(maps.Keys(m.Spec.Cells)) {
		sheet, ref, _ := splitCellKey(key)
		cell := plannedCell{sheet: sheet, ref: ref, spec: m.Spec.Cells[key]}
		if total := cell.spec.Total; total != nil && total.Table != "" {
			table, err := strconv.Atoi(total.Table)
			if err != nil {
				t.Fatal(err)
			}
			cell.table = &table
			if total.Series != nil {
				id := uuid.MustParse(*total.Series)
				cell.series = &id
			}
		}
		p.cells = append(p.cells, cell)
	}
	return p
}

func storedCell(t *testing.T, spec string) api.CellBindingResponse {
	t.Helper()
	m := cellManifest(t, "{Summary!B1: "+spec+"}")
	p := cellPlan(t, m)
	tree := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2))
	body, _, err := p.cellBody(tree, p.cells[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	body["id"] = cellTestID
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var cell api.CellBindingResponse
	if err := json.Unmarshal(data, &cell); err != nil {
		t.Fatal(err)
	}
	return cell
}

func treeWithCell(cell api.CellBindingResponse, sheets ...api.SheetBindingResponse) *api.DeliverableRevisionResponse {
	tree := bpTree(sheets...)
	tree.CellBindings = &[]api.CellBindingResponse{cell}
	return tree
}

func TestNormalizeCellRef(t *testing.T) {
	for input, want := range map[string]string{"A1": "A1", "$a$001": "A1", "a$3": "A3", "$c4": "C4", "xfd1048576": "XFD1048576"} {
		got, err := NormalizeCellRef(input)
		if err != nil || got != want {
			t.Errorf("NormalizeCellRef(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"A0", "A1048577", "XFE1", "AAAA1", " A1", "A1 ", "A-1", "1A", "A1:B2", "$$A1", "A1!", "A999999999999999999999999"} {
		if _, err := NormalizeCellRef(input); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Errorf("NormalizeCellRef(%q) error = %v, want exit 2", input, err)
		}
	}
}

func TestManifestCellsValidation(t *testing.T) {
	head := deliverableManifestHeader + deliverableManifestBasics + "  release: v3\n  cells: "
	for _, cells := range []string{
		"null", "[]", "{A1: {literal: x}}", "{'!A1': {literal: x}}", "{Summary!A0: {literal: x}}",
		"{Summary!A1: {}}", "{Summary!A1: {literal: null}}", "{Summary!A1: {run_param: ' '}}",
		"{Summary!A1: {literal: x, run_param: p}}", "{Summary!A1: {literal: x, total: {table: 5, func: count}}}",
		"{Summary!A1: {total: {series: s, func: count}}}", "{Summary!A1: {total: {table: 0, func: count}}}",
		"{Summary!A1: {total: {table: 5, func: average, column: x}}}", "{Summary!A1: {total: {table: 5, func: sum}}}",
		"{Summary!A1: {total: {table: 5, series: '', func: count}}}",
		"{Summary!A1: {total: {table: 5, same_rows_as: Data, func: count}}}",
		"{Summary!A1: {total: {series: s, same_rows_as: Data, func: count}}}",
		"{Summary!A1: {total: {same_rows_as: ' ', func: count}}}",
		"{Summary!A1: {total: {table: 5, func: count, column: ''}}}",
		"{Summary!A1: {literal: x, label_cell: '$a$01'}}", "{Summary!A1: {literal: x, label_cell: A0}}",
		"{Summary!A1: {literal: x, typo: true}}", "{Summary!A1: {literal: x, row_filter: null}}",
		"{Summary!A1: {run_param: p, row_filter: {description: d, conditions: [{column_name: c, operator: is_blank}]}}}",
		"{Summary!A1: {total: {same_rows_as: Data, func: count, row_filter: {description: d, conditions: [{column_name: c, operator: is_blank}]}}}}",
		"{Summary!A1: {literal: x}, Summary!$a$01: {literal: y}}",
	} {
		t.Run(cells, func(t *testing.T) {
			if _, err := ParseManifest([]byte(head + cells + "\n")); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v, want exit 2", err)
			}
		})
	}
	if _, err := ParseManifest([]byte(deliverableManifestHeader + deliverableManifestBasics + "  cells: {}\n")); qerrors.GetExitCode(err) != 2 {
		t.Fatalf("cells without release error = %v, want exit 2", err)
	}
	m := cellManifest(t, "{'Sheet!with bang!$b$03': {literal: '', label_cell: '$a$03'}, Summary!C4: {total: {table: 5, func: count}}}")
	cell := m.Spec.Cells["Sheet!with bang!B3"]
	if cell.Literal == nil || *cell.Literal != "" || cell.LabelCell == nil || *cell.LabelCell != "A3" {
		t.Fatalf("normalized literal cell = %+v", cell)
	}
}

func TestCellSourcesExportAndReapplyUnchanged(t *testing.T) {
	for _, spec := range []string{
		"{run_param: period}", "{literal: ''}", "{literal: hello, label_cell: A1, format: '0.00'}",
		"{total: {table: 5, func: sum, column: amount}}",
		"{total: {table: 5, series: '" + namedSeriesID + "', func: count}}",
		"{total: {same_rows_as: Data, func: max, column: amount}}",
		"{total: {table: 5, func: count, row_filter: " + openClaimsFilter + "}}",
	} {
		t.Run(spec, func(t *testing.T) {
			cell := storedCell(t, spec)
			tree := treeWithCell(cell, bpSheet(bpDataID, "Data", 5, []int{1}, 2))
			cells, err := CellsFromTree(tree, nil)
			if err != nil {
				t.Fatal(err)
			}
			item, rev := exportFixture()
			m := FromAPI(item, rev)
			m.SetCells(cells)
			assertExportRoundTrips(t, m)
			p := cellPlan(t, m)
			requests, err := p.bindingRequests(tree)
			if err != nil || len(requests) != 0 {
				t.Fatalf("round-trip requests = %v, %v; want unchanged", bpLines(requests), err)
			}
		})
	}
}

func TestCellPatchPreservesFilterAndLabelAndClearsUnusedFields(t *testing.T) {
	cell := storedCell(t, "{total: {table: 5, series: '"+namedSeriesID+"', func: sum, column: amount}, format: '0.00', label_cell: A1}")
	description, operand := "amount positive", "9007199254740993"
	cell.RowFilter = &api.RowFilter{Description: &description, Conditions: &[]api.RowFilterCondition{{ColumnName: "amount", Operator: "gt", Value: &operand}}}
	for _, tc := range []struct {
		spec, source string
		filtered     bool
	}{
		{"{total: {table: 5, func: max, column: amount}}", "aggregate", true},
		{"{run_param: period}", "run_param", false},
		{"{literal: ''}", "literal", false},
		{"{total: {same_rows_as: Data, func: count}}", "aggregate", false},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			p := cellPlan(t, cellManifest(t, "{Summary!B1: "+tc.spec+"}"))
			requests, err := p.bindingRequests(treeWithCell(cell, bpSheet(bpDataID, "Data", 5, []int{1}, 2)))
			if err != nil || len(requests) != 1 || requests[0].step != stepCellPatch {
				t.Fatalf("requests = %v, %v", bpLines(requests), err)
			}
			body := requests[0].Body
			if body[wireSourceType] != tc.source || body[wireFormatOverride] != nil || body[wireLabelCellRef] != "A1" || body[wireAggregateSeries] != nil {
				t.Fatalf("body = %s", planBody(t, body))
			}
			if (body[keyRowFilter] != nil) != tc.filtered || tc.filtered && !strings.Contains(planBody(t, body), operand) {
				t.Fatalf("row_filter = %v, want preserved %v", body[keyRowFilter], tc.filtered)
			}
		})
	}
}

func TestCellPlanMirrorRemovalAndCopy(t *testing.T) {
	cell := storedCell(t, "{total: {same_rows_as: Data, func: count}, label_cell: A1}")
	tree := treeWithCell(cell, bpSheet(bpDataID, "Data", 5, []int{1}, 2), bpSheet(bpOtherID, "New", 5, []int{1}, 2))
	p := cellPlan(t, cellManifest(t, "{Summary!B1: {total: {same_rows_as: New, func: count}}}", bpSheetDoc("New", "table: 5", "header_rows: [1]", "data_start_row: 2")))
	requests, err := p.bindingRequests(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || requests[0].step != stepCellDelete || requests[1].step != stepSheetDelete || requests[2].step != stepCellCreate {
		t.Fatalf("requests = %v, want cell delete, sheet delete, cell create", bpLines(requests))
	}
	if requests[2].Body[wireLabelCellRef] != "A1" || len(removalLabels(requests)) != 2 {
		t.Fatalf("recreated body/removals = %v / %v", requests[2].Body, removalLabels(requests))
	}
	// Changing a copied mirror between two retained sheets must still PATCH.
	p = cellPlan(t, cellManifest(t, "{Summary!B1: {total: {same_rows_as: New, func: count}}}"))
	p.CopyFrom, p.Revision = &Revision{ID: tree.ID}, nil
	requests, err = p.bindingRequests(tree)
	if err != nil || len(requests) != 1 || requests[0].Body[wireSameRowsAs] != copiedSheetBindingID {
		t.Fatalf("copied mirror requests = %v, %v", requests, err)
	}
}

func TestCellPlanRefusalsAndUnmanagedCells(t *testing.T) {
	cell := storedCell(t, "{total: {same_rows_as: Data, func: count}}")
	for _, tc := range []struct {
		name, cells string
		change      func(*Plan, *api.DeliverableRevisionResponse)
	}{
		{"missing mirror", "{Summary!B1: {total: {same_rows_as: Missing, func: count}}}", nil},
		{"data region", "{Data!B2: {literal: x}}", nil},
		{"no template", "{Summary!B1: {literal: x}}", func(_ *Plan, tree *api.DeliverableRevisionResponse) { tree.SkeletonID = nil }},
		{"enabled", "{Summary!B1: {literal: x}}", func(p *Plan, _ *api.DeliverableRevisionResponse) {
			p.Revision.State = api.DeliverableRevisionStateEnabled
		}},
		{"duplicate aliases", "{}", func(_ *Plan, tree *api.DeliverableRevisionResponse) {
			dupe := cell
			dupe.CellRef = "$b$01"
			*tree.CellBindings = append(*tree.CellBindings, dupe)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := cellPlan(t, cellManifest(t, tc.cells))
			tree := treeWithCell(cell, bpSheet(bpDataID, "Data", 5, []int{1}, 2))
			if tc.change != nil {
				tc.change(p, tree)
			}
			if requests, err := p.bindingRequests(tree); qerrors.GetExitCode(err) != 2 || len(requests) != 0 {
				t.Fatalf("requests = %v, error = %v, want exit 2 before writes", requests, err)
			}
		})
	}
	p := bpPlan(t)
	if _, err := p.bindingRequests(treeWithCell(cell, bpSheet(bpDataID, "Data", 5, []int{1}, 2))); qerrors.GetExitCode(err) != 2 {
		t.Fatalf("unmanaged mirror blocks sheet deletion: %v", err)
	}
	p = cellPlan(t, cellManifest(t, "{}"))
	tree := treeWithCell(cell)
	tree.SkeletonID = nil
	requests, err := p.bindingRequests(tree)
	if err != nil || len(requests) != 1 || requests[0].step != stepCellDelete {
		t.Fatalf("delete without template = %v, %v", requests, err)
	}
}

func TestApplyCellsUsesCreatedAndCopiedSheetIDs(t *testing.T) {
	for _, copyRelease := range []bool{false, true} {
		t.Run(strconv.FormatBool(copyRelease), func(t *testing.T) {
			base := bpTree()
			current := planDeliverable(nil)
			revisions := []map[string]any{{"id": revV3, "release": "v3", "state": "enabled", "skeleton_id": 7}}
			base.State = api.DeliverableRevisionStateEnabled
			if copyRelease {
				base.SheetBindings = &[]api.SheetBindingResponse{bpSheet(bpDataID, "Data", 5, []int{1}, 2)}
			}
			s := serveApply(t, current, revisions...)
			s.trees(revV3, base)
			m := cellManifest(t, "{Summary!B1: {total: {same_rows_as: Data, func: count}}}", bpData())
			m.Spec.Release = "v4"
			created := bpTree()
			created.ID, created.Release = uuid.MustParse(revV4), "v4"
			if copyRelease {
				created.SheetBindings = &[]api.SheetBindingResponse{bpSheet(abCopyData, "Data", 5, []int{1}, 2)}
			}
			s.on("POST", "/deliverables/"+monthlyID+"/revisions", created)
			s.on("POST", pathRevisions+revV4+pathSheetBindings, bpSheet(abNewSheet, "Data", 5, []int{1}, 2))
			cell := storedCell(t, "{total: {same_rows_as: Data, func: count}}")
			s.on("POST", pathRevisions+revV4+pathCellBindings, cell)
			s.trees(revV4, created)
			p, _ := s.prepare(m, ".")
			r, err := s.client().Apply("token", p)
			if err != nil || r.BindingWrites == 0 {
				t.Fatalf("Apply = %+v, %v", r, err)
			}
			wantID := abNewSheet
			if copyRelease {
				wantID = abCopyData
			}
			last := s.sent[len(s.sent)-1]
			if !strings.Contains(last, `"same_rows_as_sheet_binding_id":"`+wantID+`"`) || strings.Contains(last, bpDataID) {
				t.Fatalf("cell body uses wrong mirror ID: %s", last)
			}
		})
	}
}

func TestApplyReadsCellBindingsOnce(t *testing.T) {
	s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
	cell := storedCell(t, "{literal: Report}")
	cell.NeedsConfirmation = true
	s.trees(revV3, bpTree(), treeWithCell(cell))
	s.on(http.MethodPost, pathRevisions+revV3+pathCellBindings, cell)
	p, n := s.prepare(cellManifest(t, "{Summary!B1: {literal: Report}}"), ".")

	r, err := s.client().Apply("token", p)
	if err != nil {
		t.Fatal(err)
	}
	s.expect(n, "POST "+pathRevisions+revV3+pathCellBindings+
		` {"aggregate_column":null,"aggregate_func":null,"aggregate_member_dataset_id":null,"aggregate_member_series_id":null,`+
		`"cell_ref":"B1","format_override":null,"literal_value":"Report","row_filter":null,"run_param":null,`+
		`"same_rows_as_sheet_binding_id":null,"sheet_name":"Summary","source_type":"literal"}`, abV3Tree)
	if len(r.confirmations) != 1 || !strings.Contains(r.confirmations[0], `sheet "Summary" cell B1 needs confirmation`) ||
		r.Manifest.Spec.Cells["Summary!B1"].Literal == nil || *r.Manifest.Spec.Cells["Summary!B1"].Literal != "Report" {
		t.Fatalf("result = %+v; want the post-write cell and its confirmation note", r)
	}
}

func TestCellWritesKeepHTTPExitCodes(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 500} {
		for _, method := range []string{"POST", "PATCH", "DELETE"} {
			t.Run(method+strconv.Itoa(status), func(t *testing.T) {
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				path := abOrg + pathRevisions + revV3 + pathCellBindings
				if method != "POST" {
					path += "/" + cellTestID
				}
				mock.RegisterHandler(method, path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, status, map[string]any{"detail": map[string]any{"msg": "refused", "error_code": "BINDING_INVALID"}})
				})
				c := NewClient(mock.URL(), testOrgID, 0)
				var err error
				switch method {
				case "POST":
					_, err = c.CreateCellBinding("token", uuid.MustParse(revV3), map[string]any{})
				case "PATCH":
					_, err = c.PatchCellBinding("token", uuid.MustParse(revV3), uuid.MustParse(cellTestID), map[string]any{})
				default:
					err = c.DeleteCellBinding("token", uuid.MustParse(revV3), uuid.MustParse(cellTestID))
				}
				want := qerrors.GetExitCode(qerrors.FromHTTPStatus(status, "refused"))
				if qerrors.GetExitCode(err) != want {
					t.Fatalf("error = %v (exit %d), want %d", err, qerrors.GetExitCode(err), want)
				}
			})
		}
	}
}

func TestCellExportUsesSeriesNamesWithoutSheets(t *testing.T) {
	cell := storedCell(t, "{total: {table: 5, series: '"+namedSeriesID+"', func: count}}")
	tree := treeWithCell(cell)
	tree.ID = uuid.MustParse(sheetsTreeRevision)
	data, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	mock := serveExport(t, string(data))
	defer mock.Close()
	item, rev := exportFixture()
	m, err := NewClient(mock.URL(), testOrgID, 0).ExportManifest("token", item, rev)
	if err != nil {
		t.Fatal(err)
	}
	if got := *m.Spec.Cells["Summary!B1"].Total.Series; got != "Monthly claims" {
		t.Fatalf("exported series = %q", got)
	}
	if !reflect.DeepEqual(mock.Requests(), []string{"GET " + exportRevisionPath, "GET " + exportSeriesPath}) {
		t.Fatalf("requests = %v", mock.Requests())
	}
}

func TestCellPatchRetainsAnUndescribedFilter(t *testing.T) {
	cell := storedCell(t, "{total: {table: 5, func: count}}")
	cell.RowFilter = &api.RowFilter{Conditions: &[]api.RowFilterCondition{{ColumnName: "amount", Operator: "is_not_null"}}}
	p := cellPlan(t, cellManifest(t, "{Summary!B1: {total: {table: 5, func: sum, column: amount}}}"))
	requests, err := p.bindingRequests(treeWithCell(cell))
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests = %v, %v", requests, err)
	}
	filter, ok := requests[0].Body[keyRowFilter].(*api.DescribedRowFilter)
	if !ok || filter.Description != "Only some rows" || !reflect.DeepEqual(filter.Conditions, cell.RowFilter.Conditions) || cell.RowFilter.Description != nil {
		t.Fatalf("saved filter = %+v, stored filter = %+v", filter, cell.RowFilter)
	}
}

// cellFilter is a total's row_filter keeping positive amounts, described as
// description.
func cellFilter(description, operand string) string {
	return `row_filter: {description: "` + description + `", conditions: [{column_name: amount, operator: gt, value: "` + operand + `"}]}`
}

func TestCellSavesPlanRowFilters(t *testing.T) {
	plain := storedCell(t, "{total: {table: 5, func: count}}")
	filtered := plain
	filtered.RowFilter = bpStoredFilter("Positive amounts")
	count := func(keys string) string { return "{table: 5, func: count, " + keys + "}" }
	for _, tc := range []struct {
		name   string
		stored *api.CellBindingResponse
		total  string
		step   requestStep // 0 when nothing is sent
		filter string      // the row_filter body
	}{
		{name: "a POST sends a written filter", total: count(cellFilter("Positive amounts", "0")), step: stepCellCreate, filter: bpFilterJSON},
		{name: "a filter where none is stored", stored: &plain, total: count(cellFilter("Positive amounts", "0")), step: stepCellPatch,
			filter: bpFilterJSON},
		{name: "a null filter clears the stored one", stored: &filtered, total: count("row_filter: null"), step: stepCellPatch, filter: "null"},
		{name: "a null filter where none is stored", stored: &plain, total: count("row_filter: null")},
		{name: "an absent filter changes nothing", stored: &filtered, total: "{table: 5, func: count}"},
		{name: "an absent filter is preserved through another change", stored: &filtered, total: "{table: 5, func: sum, column: amount}",
			step: stepCellPatch, filter: bpFilterJSON},
		{name: "the server's normal form and a padded description", stored: &filtered, total: count(cellFilter(" Positive amounts  ", "0"))},
		{name: "a new description alone", stored: &filtered, total: count(cellFilter("Amounts above zero", "0")), step: stepCellPatch,
			filter: strings.Replace(bpFilterJSON, "Positive amounts", "Amounts above zero", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := cellPlan(t, cellManifest(t, "{Summary!B1: {total: "+tc.total+"}}"))
			tree := bpTree()
			if tc.stored != nil {
				tree = treeWithCell(*tc.stored)
			}
			requests, err := p.bindingRequests(tree)
			if err != nil {
				t.Fatalf("bindingRequests: %v", err)
			}
			if tc.step == 0 {
				if len(requests) != 0 {
					t.Fatalf("requests = %v, want none", bpLines(requests))
				}
				return
			}
			if len(requests) != 1 || requests[0].step != tc.step {
				t.Fatalf("requests = %v, want one step %d", bpLines(requests), tc.step)
			}
			if filter, sent := requests[0].Body[keyRowFilter]; !sent || jsonText(filter) != tc.filter {
				t.Errorf("row_filter = %s (sent %v), want %s", jsonText(filter), sent, tc.filter)
			}
		})
	}
}

func TestCellSavesRefuseANewFilterUnderTheStoredDescription(t *testing.T) {
	cell := storedCell(t, "{total: {table: 5, func: count}}")
	cell.RowFilter = bpStoredFilter("Positive amounts")
	p := cellPlan(t, cellManifest(t, "{Summary!B1: {total: {table: 5, func: count, "+cellFilter("Positive amounts", "10")+"}}}"))
	requests, err := p.bindingRequests(treeWithCell(cell))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || requests != nil {
		t.Fatalf("requests = %v, exit = %d, want none and %d; error = %v", bpLines(requests), code, qerrors.ExitBadUsage, err)
	}
	want := `spec.cells.Summary!B1.total.row_filter keeps different rows from the stored filter but repeats its description "Positive amounts"; ` +
		"describe which rows the new filter keeps"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestApplyMirrorRemovalResumesAfterEachFailure(t *testing.T) {
	old := storedCell(t, "{total: {same_rows_as: Data, func: count}, label_cell: A1}")
	data, other := bpSheet(bpDataID, "Data", 5, []int{1}, 2), bpSheet(bpOtherID, "New", 5, []int{1}, 2)
	initial := treeWithCell(old, data, other)
	finalCell := old
	finalCell.SameRowsAsSheetBindingID = &other.ID
	final := treeWithCell(finalCell, other)
	m := cellManifest(t, "{Summary!B1: {total: {same_rows_as: New, func: count}, label_cell: A1}}", bpSheetDoc("New", "table: 5", "header_rows: [1]", "data_start_row: 2"))
	paths := []string{pathRevisions + revV3 + pathCellBindings + "/" + cellTestID, bpSheets + "/" + bpDataID, pathRevisions + revV3 + pathCellBindings}
	methods := []string{http.MethodDelete, http.MethodDelete, http.MethodPost}
	for failure := range paths {
		t.Run(strconv.Itoa(failure), func(t *testing.T) {
			s := serveApply(t, planDeliverable(nil), draftV3)
			s.trees(revV3, initial)
			s.on(methods[0], paths[0])
			s.on(methods[1], paths[1])
			s.on(methods[2], paths[2], finalCell)
			s.fail(methods[failure], paths[failure], http.StatusForbidden, `{"detail":{"msg":"denied"}}`)
			p, _ := s.prepare(m, ".")
			r, err := s.client().Apply("token", p)
			if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
				t.Fatalf("error = %v, want exit 4", err)
			}
			if failure == 0 {
				if r != nil {
					t.Fatalf("first failure result = %+v, want nil", r)
				}
			} else if r == nil || r.BindingWrites != failure || !strings.Contains(err.Error(), `cell "Summary!B1"`) && failure == 2 {
				t.Fatalf("partial result = %+v, error = %v", r, err)
			}
			if len(s.sent) != failure+1 {
				t.Fatalf("requests continued after failure: %v", s.sent)
			}
			remaining := initial
			switch failure {
			case 1:
				remaining = bpTree(data, other)
			case 2:
				remaining = bpTree(other)
			}
			s.trees(revV3, remaining, final)
			s.on(methods[0], paths[0])
			s.on(methods[1], paths[1])
			s.on(methods[2], paths[2], finalCell)
			p, _ = s.prepare(m, ".")
			r, err = s.client().Apply("token", p)
			if err != nil || r.BindingWrites != len(paths)-failure || len(s.sent) != len(paths)-failure {
				t.Fatalf("resume = %+v, %v, writes = %v", r, err, s.sent)
			}
			for i, request := range s.sent {
				if !strings.HasPrefix(request, methods[failure+i]+" "+paths[failure+i]) {
					t.Fatalf("resume request %d = %s", i, request)
				}
			}
			s.trees(revV3, final)
			p, _ = s.prepare(m, ".")
			if len(p.Requests) != 0 {
				t.Fatalf("reapply = %v, want unchanged", planLines(p))
			}
		})
	}
}

func TestApplyCellsReplansAfterCopiedTemplateMovesThem(t *testing.T) {
	dir := planWorkbook(t)
	s := serveApply(t, planDeliverable(nil), disabledV1, enabledV2)
	cell := storedCell(t, "{literal: Report, label_cell: A1}")
	source := treeWithCell(cell)
	source.ID, source.State = uuid.MustParse(revV2), api.DeliverableRevisionStateEnabled
	s.trees(revV2, source)
	moved := cell
	label := "A3"
	moved.ID, moved.CellRef, moved.LabelCellRef = uuid.MustParse(abCopyColumn), "B3", &label
	created := treeWithCell(moved)
	created.ID, created.Release = uuid.MustParse(revV4), "v3"
	final := treeWithCell(cell)
	final.ID, final.Release = created.ID, created.Release
	s.trees(revV4, final)
	s.on(http.MethodPut, "/deliverable-skeletons/upload-url", planMinted)
	s.on(http.MethodPut, putTargetPath, "")
	s.on(http.MethodPost, "/deliverable-skeletons/finalize", finalizedTemplate)
	s.on(http.MethodPost, "/deliverables/"+monthlyID+"/revisions", created)
	s.on(http.MethodDelete, pathRevisions+revV4+pathCellBindings+"/"+moved.ID.String())
	s.on(http.MethodPost, pathRevisions+revV4+pathCellBindings, cell)
	m := cellManifest(t, "{Summary!B1: {literal: Report, label_cell: A1}}")
	m.Spec.Template, m.present[keyTemplate] = "carrier-a.xlsx", true
	p, _ := s.prepare(m, dir)
	if !p.MayRemoveCells || len(p.Removals) != 0 || len(p.Requests) != 4 {
		t.Fatalf("plan = %v, MayRemoveCells = %v, removals = %v", planLines(p), p.MayRemoveCells, p.Removals)
	}
	r, err := s.client().Apply("token", p)
	if err != nil || r.BindingWrites != 2 {
		t.Fatalf("Apply = %+v, %v", r, err)
	}
	if len(s.sent) != 6 || s.sent[4] != "DELETE "+pathRevisions+revV4+pathCellBindings+"/"+moved.ID.String() ||
		!strings.Contains(s.sent[5], `"cell_ref":"B1"`) || !strings.Contains(s.sent[5], `"label_cell_ref":"A1"`) {
		t.Fatalf("re-anchor writes = %v", s.sent)
	}
}
