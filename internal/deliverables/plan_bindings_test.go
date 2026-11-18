package deliverables

import (
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	bpDataID   = "5a000000-0000-4000-8000-000000000001"
	bpOldID    = "5a000000-0000-4000-8000-000000000002"
	bpOtherID  = "5a000000-0000-4000-8000-000000000003"
	bpColumnA  = "5c000000-0000-4000-8000-000000000001"
	bpColumnB  = "5c000000-0000-4000-8000-000000000002"
	bpColumnC  = "5c000000-0000-4000-8000-000000000003"
	bpColumnD  = "5c000000-0000-4000-8000-000000000004"
	bpSeriesID = "5e000000-0000-4000-8000-000000000001"

	bpSheets      = "/deliverable-revisions/" + revV3 + "/sheet-bindings"
	bpDataColumns = "/deliverable-sheet-bindings/" + bpDataID + "/column-bindings"
	bpTreePath    = "/api/orgs/" + testOrgID + "/deliverable-revisions/"
	bpDatasets    = "/api/orgs/" + testOrgID + "/datasets"
	bpBase        = "table: 5"
)

// bpSheet is a stored sheet binding with no series and no sort keys.
func bpSheet(id, name string, table int, headerRows []int, dataStartRow int, columns ...api.ColumnBindingResponse) api.SheetBindingResponse {
	return api.SheetBindingResponse{ID: uuid.MustParse(id), SheetName: name, MemberDatasetID: table, HeaderRows: headerRows,
		DataStartRow: dataStartRow, SortKeys: &[]api.SortKeySchema{}, ColumnBindings: &columns}
}

// bpColumn is a stored column binding at index sourced from table column
// source, or from run parameter source when it starts with "$".
func bpColumn(id string, index int, source string) api.ColumnBindingResponse {
	binding := api.ColumnBindingResponse{ID: uuid.MustParse(id), TargetColumnIndex: index, Origin: "user",
		SourceType: api.DeliverableColumnSourceTypeColumn, SourceColumn: &source}
	if name, ok := strings.CutPrefix(source, "$"); ok {
		binding.SourceType, binding.SourceColumn, binding.RunParam = api.DeliverableColumnSourceTypeRunParam, nil, &name
	}
	return binding
}

func bpFlagged(binding api.ColumnBindingResponse) api.ColumnBindingResponse {
	binding.NeedsConfirmation = true
	return binding
}

func bpFormatted(binding api.ColumnBindingResponse, format string) api.ColumnBindingResponse {
	binding.FormatOverride = &format
	return binding
}

// bpTree is draft release v3, with template 7, holding sheets.
func bpTree(sheets ...api.SheetBindingResponse) *api.DeliverableRevisionResponse {
	template := 7
	return &api.DeliverableRevisionResponse{ID: uuid.MustParse(revV3), DeliverableID: uuid.MustParse(monthlyID), Release: "v3",
		State: api.DeliverableRevisionStateDraft, SkeletonID: &template, CellBindings: &[]api.CellBindingResponse{}, SheetBindings: &sheets}
}

// bpSheetDoc is spec.sheets entry name made of lines.
func bpSheetDoc(name string, lines ...string) string {
	return "    " + name + ":\n" + indentLines(6, lines...)
}

// bpData is spec.sheets entry Data: table 5, header_rows [1] and
// data_start_row 2, then lines.
func bpData(lines ...string) string {
	return bpSheetDoc("Data", append([]string{bpBase, "header_rows: [1]", "data_start_row: 2"}, lines...)...)
}

// bpManifest parses carrier-a-bordereau release v3 with spec lines extra and
// spec.sheets made of sheets, {} when there are none.
func bpManifest(t *testing.T, extra string, sheets ...string) *Manifest {
	t.Helper()
	doc := deliverableManifestHeader + deliverableManifestBasics + extra + "  release: v3\n"
	if len(sheets) == 0 {
		doc += "  sheets: {}\n"
	} else {
		doc += "  sheets:\n" + strings.Join(sheets, "")
	}
	m, err := ParseManifest([]byte(doc))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return m
}

// bpPlanWith is the plan of m against draft release v3, each table read as
// its ID and each series as its UUID.
func bpPlanWith(t *testing.T, m *Manifest) *Plan {
	t.Helper()
	p := &Plan{Slug: "carrier-a-bordereau", Release: "v3", managesSheets: true,
		Revision: &Revision{ID: uuid.MustParse(revV3), Release: "v3", State: api.DeliverableRevisionStateDraft}}
	for _, name := range slices.Sorted(maps.Keys(m.Spec.Sheets)) {
		spec := m.Spec.Sheets[name]
		table, err := strconv.Atoi(spec.Table)
		if err != nil {
			t.Fatal(err)
		}
		sheet := plannedSheet{name: name, spec: spec, table: table}
		if spec.Series != nil {
			id := uuid.MustParse(*spec.Series)
			sheet.series = &id
		}
		p.sheets = append(p.sheets, sheet)
	}
	return p
}

func bpPlan(t *testing.T, sheets ...string) *Plan {
	t.Helper()
	return bpPlanWith(t, bpManifest(t, "", sheets...))
}

func bpLines(requests []Request) []string {
	var out []string
	for _, r := range requests {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

// bpStoredData is stored sheet Data: table 5, series bpSeriesID, header_rows
// [1], data_start_row 2, one instruction row, and sort keys policy_id then
// amount descending at positions 3 and 7, listed out of order.
func bpStoredData() api.SheetBindingResponse {
	sheet := bpSheet(bpDataID, "Data", 5, []int{1}, 2)
	series, descending := uuid.MustParse(bpSeriesID), true
	sheet.MemberSeriesID, sheet.InstructionRowCount = &series, 1
	sheet.SortKeys = &[]api.SortKeySchema{{Position: 7, ColumnName: "amount", Descending: &descending}, {Position: 3, ColumnName: "policy_id"}}
	return sheet
}

const bpStoredSortKeys = `[{"column_name":"policy_id","descending":false,"position":3},{"column_name":"amount","descending":true,"position":7}]`

func TestBindingRequestsPlansSheetWrites(t *testing.T) {
	matching := []string{`series: "` + bpSeriesID + `"`, "instruction_row_count: 1",
		"row_order: [{column: policy_id}, {column: amount, descending: true}]"}
	seeded := `"instruction_row_count":1,"member_dataset_id":5,"member_series_id":"` + bpSeriesID + `","sheet_name":"Data","sort_keys":` + bpStoredSortKeys
	for _, tc := range []struct {
		name   string
		stored []api.SheetBindingResponse
		sheets []string
		lines  []string
		body   string
		note   string
	}{
		{name: "a matching sheet, row order compared by order and not by position", stored: []api.SheetBindingResponse{bpStoredData()},
			sheets: []string{bpData(matching...)}},
		{name: "unwritten keys keep their stored values", stored: []api.SheetBindingResponse{bpStoredData()},
			sheets: []string{bpSheetDoc("Data", "table: 6", "header_rows: [1]", "data_start_row: 2")},
			lines:  []string{"PATCH " + bpSheets + "/" + bpDataID},
			body: `{"data_start_row":2,"header_rows":[1],"instruction_row_count":1,"member_dataset_id":6,"member_series_id":"` + bpSeriesID +
				`","sheet_name":"Data","sort_keys":` + bpStoredSortKeys + `}`,
			note: "changes table"},
		{name: "a null series clears it", stored: []api.SheetBindingResponse{bpStoredData()},
			sheets: []string{bpData("series: null", "instruction_row_count: 1")},
			lines:  []string{"PATCH " + bpSheets + "/" + bpDataID},
			body: `{"data_start_row":2,"header_rows":[1],"instruction_row_count":1,"member_dataset_id":5,"member_series_id":null,` +
				`"sheet_name":"Data","sort_keys":` + bpStoredSortKeys + `}`,
			note: "changes series"},
		{name: "a reordered row_order is renumbered from 0", stored: []api.SheetBindingResponse{bpStoredData()},
			sheets: []string{bpData("row_order: [{column: amount, descending: true}, {column: policy_id}]")},
			lines:  []string{"PATCH " + bpSheets + "/" + bpDataID},
			body: `{"data_start_row":2,"header_rows":[1],` + strings.Replace(seeded, bpStoredSortKeys,
				`[{"column_name":"amount","descending":true,"position":0},{"column_name":"policy_id","descending":false,"position":1}]`, 1) + `}`,
			note: "changes row_order"},
		{name: "an empty row_order clears it", stored: []api.SheetBindingResponse{bpStoredData()},
			sheets: []string{bpData("row_order: []", "instruction_row_count: 0")},
			lines:  []string{"PATCH " + bpSheets + "/" + bpDataID},
			body: `{"data_start_row":2,"header_rows":[1],"instruction_row_count":0,"member_dataset_id":5,"member_series_id":"` + bpSeriesID +
				`","sheet_name":"Data","sort_keys":[]}`,
			note: "changes instruction_row_count, row_order"},
		{name: "a layout change says the server re-anchors", stored: []api.SheetBindingResponse{bpStoredData()},
			sheets: []string{bpSheetDoc("Data", bpBase, "header_rows: [2]", "data_start_row: 3")},
			lines:  []string{"PATCH " + bpSheets + "/" + bpDataID},
			body:   `{"data_start_row":3,"header_rows":[2],` + seeded + `}`,
			note:   "changes header_rows, data_start_row; the server re-anchors the sheet's column bindings by their header labels"},
		{name: "a layout change on listed columns says apply plans them again", stored: []api.SheetBindingResponse{bpStoredData()},
			sheets: []string{bpSheetDoc("Data", bpBase, "header_rows: []", "data_start_row: 2", "columns: {}")},
			lines:  []string{"PATCH " + bpSheets + "/" + bpDataID},
			body:   `{"data_start_row":2,"header_rows":[],` + seeded + `}`,
			note: "changes header_rows; the server re-anchors the sheet's column bindings by their header labels, " +
				"and apply plans its column writes again from the response"},
		{name: "a create sends every written key",
			sheets: []string{bpData(`series: "`+bpSeriesID+`"`, "instruction_row_count: 2", "row_order: [{column: policy_id}, {column: amount, descending: true}]")},
			lines:  []string{"POST " + bpSheets},
			body: `{"data_start_row":2,"header_rows":[1],"instruction_row_count":2,"member_dataset_id":5,"member_series_id":"` + bpSeriesID + `",` +
				`"sheet_name":"Data","sort_keys":[{"column_name":"policy_id","descending":false,"position":0},{"column_name":"amount","descending":true,"position":1}]}`},
		{name: "a create leaves out what is not written",
			sheets: []string{bpSheetDoc("Data", bpBase, "header_rows: []", "data_start_row: 1", "series: null")},
			lines:  []string{"POST " + bpSheets}, body: `{"data_start_row":1,"header_rows":[],"member_dataset_id":5,"sheet_name":"Data"}`},
		{name: "a sheet spec.sheets lacks is deleted",
			stored: []api.SheetBindingResponse{bpSheet(bpOldID, "Old", 5, []int{1}, 2, bpColumn(bpColumnA, 1, "amount"), bpColumn(bpColumnB, 2, "$period"))},
			lines:  []string{"DELETE " + bpSheets + "/" + bpOldID}, note: `sheet "Old" is not in spec.sheets; its 2 column bindings go with it`},
		{name: "a deleted sheet's one column binding", stored: []api.SheetBindingResponse{bpSheet(bpOldID, "Old", 5, []int{1}, 2, bpColumn(bpColumnA, 1, "amount"))},
			lines: []string{"DELETE " + bpSheets + "/" + bpOldID}, note: `sheet "Old" is not in spec.sheets; its 1 column binding goes with it`},
		{name: "a deleted sheet with no column bindings", stored: []api.SheetBindingResponse{bpSheet(bpOldID, "Old", 5, []int{1}, 2)},
			lines: []string{"DELETE " + bpSheets + "/" + bpOldID}, note: `sheet "Old" is not in spec.sheets`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, err := bpPlan(t, tc.sheets...).bindingRequests(bpTree(tc.stored...))
			if err != nil {
				t.Fatalf("bindingRequests: %v", err)
			}
			if got := bpLines(requests); !reflect.DeepEqual(got, tc.lines) {
				t.Fatalf("requests = %v, want %v", got, tc.lines)
			}
			if len(requests) == 0 {
				return
			}
			r := requests[0]
			if tc.body != "" || r.Body != nil {
				if got := planBody(t, r.Body); got != tc.body {
					t.Errorf("body =\n%s\nwant\n%s", got, tc.body)
				}
			}
			if r.Note != tc.note {
				t.Errorf("note = %q, want %q", r.Note, tc.note)
			}
			if _, rowFilter := r.Body["row_filter"]; rowFilter {
				t.Errorf("body sends row_filter: %v", r.Body)
			}
			wantID := uuid.Nil
			if r.Method != http.MethodPost {
				wantID = requests[0].stored
			}
			if r.SheetBindingID != wantID || r.Sheet == "" || r.Column != 0 || r.ColumnBindingID != uuid.Nil {
				t.Errorf("request fields = %+v", r)
			}
		})
	}
}

// bpFilter is a row_filter line keeping positive amounts, described as
// description, and bpFilterJSON is the row_filter it sends.
func bpFilter(description, operand string) string {
	return `row_filter: {description: "` + description + `", conditions: [{column_name: amount, operator: gt, value: "` + operand + `"}]}`
}

const bpFilterJSON = `{"conditions":[{"case_sensitive":false,"column_name":"amount","operator":"gt","value":"0","value_high":null,"value_list":null}],` +
	`"description":"Positive amounts"}`

// bpStoredFilter is the server's form of bpFilter(description, "0").
func bpStoredFilter(description string) *api.RowFilter {
	operand, sensitive := "0", false
	return &api.RowFilter{Description: &description,
		Conditions: &[]api.RowFilterCondition{{ColumnName: "amount", Operator: api.Gt, Value: &operand, CaseSensitive: &sensitive}}}
}

func bpFiltered(sheet api.SheetBindingResponse, filter *api.RowFilter) api.SheetBindingResponse {
	sheet.RowFilter = filter
	return sheet
}

func TestBindingRequestsPlansSheetRowFilters(t *testing.T) {
	matching := []string{`series: "` + bpSeriesID + `"`, "instruction_row_count: 1",
		"row_order: [{column: policy_id}, {column: amount, descending: true}]"}
	data := func(lines ...string) string { return bpData(append(slices.Clone(matching), lines...)...) }
	stored, filtered := bpStoredData(), bpFiltered(bpStoredData(), bpStoredFilter("Positive amounts"))
	for _, tc := range []struct {
		name   string
		stored []api.SheetBindingResponse
		sheet  string
		method string // "" when nothing is sent
		filter string // the row_filter body; "" when the body has no row_filter key
		note   string
	}{
		{name: "a POST sends a written filter", sheet: bpData(bpFilter("Positive amounts", "0")), method: http.MethodPost, filter: bpFilterJSON},
		{name: "a POST leaves out a null filter", sheet: bpData("row_filter: null"), method: http.MethodPost},
		{name: "a filter where none is stored", stored: []api.SheetBindingResponse{stored}, sheet: data(bpFilter("Positive amounts", "0")),
			method: http.MethodPatch, filter: bpFilterJSON, note: "changes row_filter"},
		{name: "a null filter clears the stored one", stored: []api.SheetBindingResponse{filtered}, sheet: data("row_filter: null"),
			method: http.MethodPatch, filter: "null", note: "changes row_filter"},
		{name: "a null filter where none is stored", stored: []api.SheetBindingResponse{stored}, sheet: data("row_filter: null")},
		{name: "an absent filter keeps the stored one", stored: []api.SheetBindingResponse{filtered}, sheet: data()},
		{name: "an absent filter stays out of another change", stored: []api.SheetBindingResponse{filtered},
			sheet:  bpSheetDoc("Data", append([]string{"table: 6", "header_rows: [1]", "data_start_row: 2"}, matching...)...),
			method: http.MethodPatch, note: "changes table"},
		{name: "the server's normal form and a padded description", stored: []api.SheetBindingResponse{filtered},
			sheet: data(bpFilter("  Positive amounts ", "0"))},
		{name: "a new description alone", stored: []api.SheetBindingResponse{filtered}, sheet: data(bpFilter("Amounts above zero", "0")),
			method: http.MethodPatch, filter: strings.Replace(bpFilterJSON, "Positive amounts", "Amounts above zero", 1), note: "changes row_filter"},
		{name: "new rows and a new description", stored: []api.SheetBindingResponse{filtered}, sheet: data(bpFilter("Amounts over 10", "10")),
			method: http.MethodPatch, filter: strings.NewReplacer(`"0"`, `"10"`, "Positive amounts", "Amounts over 10").Replace(bpFilterJSON),
			note: "changes row_filter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, err := bpPlan(t, tc.sheet).bindingRequests(bpTree(tc.stored...))
			if err != nil {
				t.Fatalf("bindingRequests: %v", err)
			}
			if tc.method == "" {
				if len(requests) != 0 {
					t.Fatalf("requests = %v, want none", bpLines(requests))
				}
				return
			}
			if len(requests) != 1 || requests[0].Method != tc.method {
				t.Fatalf("requests = %v, want one %s", bpLines(requests), tc.method)
			}
			r := requests[0]
			filter, sent := r.Body[keyRowFilter]
			if tc.filter == "" {
				if sent {
					t.Errorf("body sends row_filter: %s", planBody(t, r.Body))
				}
			} else if got := jsonText(filter); !sent || got != tc.filter {
				t.Errorf("row_filter = %s (sent %v), want %s", got, sent, tc.filter)
			}
			if r.Note != tc.note {
				t.Errorf("note = %q, want %q", r.Note, tc.note)
			}
		})
	}
}

func TestBindingRequestsRefuseANewFilterUnderTheStoredDescription(t *testing.T) {
	sheet := bpData(bpFilter(" Positive amounts", "10"))
	stored := bpSheet(bpDataID, "Data", 5, []int{1}, 2)
	stored.RowFilter = bpStoredFilter("Positive amounts ")
	copying := func(p *Plan, tree *api.DeliverableRevisionResponse) {
		p.Revision, p.CopyFrom = nil, &Revision{ID: uuid.MustParse(revV2), Release: "v2", State: api.DeliverableRevisionStateEnabled}
		tree.ID, tree.State = uuid.MustParse(revV2), api.DeliverableRevisionStateEnabled
	}
	for _, tc := range []struct {
		name   string
		change func(*Plan, *api.DeliverableRevisionResponse)
	}{
		{name: "a draft"},
		{name: "the source of a copy", change: copying},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, tree := bpPlan(t, sheet), bpTree(stored)
			if tc.change != nil {
				tc.change(p, tree)
			}
			requests, err := p.bindingRequests(tree)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || requests != nil {
				t.Fatalf("requests = %v, exit = %d, want none and %d; error = %v", bpLines(requests), code, qerrors.ExitBadUsage, err)
			}
			want := `spec.sheets.Data.row_filter keeps different rows from the stored filter but repeats its description "Positive amounts"; ` +
				"describe which rows the new filter keeps"
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
		})
	}
	// No stored filter, or the stored filter's rows, means no refusal.
	for _, tree := range []*api.DeliverableRevisionResponse{bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2)),
		bpTree(bpFiltered(bpSheet(bpDataID, "Data", 5, []int{1}, 2), bpStoredFilter("Positive amounts")))} {
		if _, err := bpPlan(t, bpData(bpFilter("Positive amounts", "0"))).bindingRequests(tree); err != nil {
			t.Errorf("bindingRequests: %v", err)
		}
	}
}

func TestBindingRequestsOrderSheetsThenEachSheetsColumns(t *testing.T) {
	stored := []api.SheetBindingResponse{
		bpSheet(bpOtherID, "Beta", 5, []int{1}, 2),
		bpSheet(bpOldID, "Alpha", 5, []int{1}, 2, bpColumn(bpColumnB, 2, "amount")),
		bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnA, 1, "amount"), bpColumn(bpColumnC, 3, "policy_id"), bpColumn(bpColumnD, 4, "carrier")),
	}
	p := bpPlan(t,
		bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3", "columns: {C: {column: claim_id}, D: {column: carrier}, b: {column: amount}}"),
		bpSheetDoc("New", bpBase, "header_rows: []", "data_start_row: 1", "columns: {2: {column: policy_id}, A: {run_param: period}}"))
	requests, err := p.bindingRequests(bpTree(stored...))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DELETE " + bpSheets + "/" + bpOldID,
		"DELETE " + bpSheets + "/" + bpOtherID,
		"PATCH " + bpSheets + "/" + bpDataID,
		"DELETE " + bpDataColumns + "/" + bpColumnA,
		"PATCH " + bpDataColumns + "/" + bpColumnC,
		"POST " + bpDataColumns,
		"POST " + bpSheets,
		"POST /deliverable-sheet-bindings/<created-sheet-binding-id>/column-bindings",
		"POST /deliverable-sheet-bindings/<created-sheet-binding-id>/column-bindings",
	}
	if got := bpLines(requests); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i, fields := range map[int]struct {
		sheet          string
		column         int
		sheetID, colID string
	}{
		0: {"Alpha", 0, bpOldID, ""}, 3: {"Data", 1, bpDataID, bpColumnA}, 4: {"Data", 3, bpDataID, bpColumnC},
		5: {"Data", 2, bpDataID, ""}, 6: {"New", 0, "", ""}, 7: {"New", 1, "", ""}, 8: {"New", 2, "", ""},
	} {
		r := requests[i]
		if r.Sheet != fields.sheet || r.Column != fields.column || idText(r.SheetBindingID) != fields.sheetID || idText(r.ColumnBindingID) != fields.colID {
			t.Errorf("request %d = sheet %q column %d ids %s/%s, want %+v", i, r.Sheet, r.Column, r.SheetBindingID, r.ColumnBindingID, fields)
		}
	}
	if want := []string{`sheet "Alpha"`, `sheet "Beta"`, `sheet "Data" column A`}; !reflect.DeepEqual(removalLabels(requests), want) {
		t.Errorf("removals = %q, want %q", removalLabels(requests), want)
	}
}

func idText(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func TestColumnRequests(t *testing.T) {
	stored := []api.ColumnBindingResponse{bpFlagged(bpColumn(bpColumnA, 1, "$period")), bpFormatted(bpColumn(bpColumnC, 3, "amount"), "#,##0.00"),
		bpColumn(bpColumnD, 4, "policy_id")}
	matching := `A: {run_param: period}, C: {column: amount, format: "#,##0.00"}, D: {column: policy_id}`
	for _, tc := range []struct {
		name    string
		columns string // the columns line, "" for none
		current []api.ColumnBindingResponse
		lines   []string
		bodies  map[int]string
		notes   map[int]string
	}{
		{name: "matching bindings, a flagged one included, are not written", columns: "columns: {" + matching + "}", current: stored},
		{name: "columns not written plan nothing", current: stored},
		{name: "an empty columns deletes every binding", columns: "columns: {}", current: stored,
			lines: []string{"DELETE " + bpDataColumns + "/" + bpColumnA, "DELETE " + bpDataColumns + "/" + bpColumnC, "DELETE " + bpDataColumns + "/" + bpColumnD},
			notes: map[int]string{1: `sheet "Data" column C is not in spec.sheets.Data.columns`}},
		{name: "deletes, then patches, then posts, each by index", current: stored,
			columns: `columns: {E: {run_param: carrier, format: "@"}, C: {column: amount}, 2: {column: claim_id}}`,
			lines: []string{"DELETE " + bpDataColumns + "/" + bpColumnA, "DELETE " + bpDataColumns + "/" + bpColumnD,
				"PATCH " + bpDataColumns + "/" + bpColumnC, "POST " + bpDataColumns, "POST " + bpDataColumns},
			bodies: map[int]string{
				2: `{"format_override":null,"run_param":null,"source_column":"amount","source_type":"column","target_column_index":3}`,
				3: `{"format_override":null,"run_param":null,"source_column":"claim_id","source_type":"column","target_column_index":2}`,
				4: `{"format_override":"@","run_param":"carrier","source_column":null,"source_type":"run_param","target_column_index":5}`,
			},
			notes: map[int]string{2: `sheet "Data" column C`, 3: `sheet "Data" column B`, 4: `sheet "Data" column E`}},
		{name: "a swap is two PATCHes in place", current: stored[1:],
			columns: `columns: {C: {column: policy_id, format: "#,##0.00"}, D: {column: amount}}`,
			lines:   []string{"PATCH " + bpDataColumns + "/" + bpColumnC, "PATCH " + bpDataColumns + "/" + bpColumnD},
			bodies: map[int]string{
				0: `{"format_override":"#,##0.00","run_param":null,"source_column":"policy_id","source_type":"column","target_column_index":3}`,
				1: `{"format_override":null,"run_param":null,"source_column":"amount","source_type":"column","target_column_index":4}`,
			}},
		{name: "a PATCH of a flagged binding also confirms it", current: stored,
			columns: `columns: {A: {column: period}, C: {column: amount, format: "#,##0.00"}, D: {column: policy_id}}`,
			lines:   []string{"PATCH " + bpDataColumns + "/" + bpColumnA},
			bodies:  map[int]string{0: `{"format_override":null,"run_param":null,"source_column":"period","source_type":"column","target_column_index":1}`},
			notes:   map[int]string{0: `sheet "Data" column A; ` + confirmsNote}},
		{name: "a format the manifest leaves out is cleared", current: stored,
			columns: `columns: {A: {run_param: period}, C: {column: amount}, D: {column: policy_id, format: "0"}}`,
			lines:   []string{"PATCH " + bpDataColumns + "/" + bpColumnC, "PATCH " + bpDataColumns + "/" + bpColumnD}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := []string{bpBase, "header_rows: [1]", "data_start_row: 2"}
			if tc.columns != "" {
				lines = append(lines, tc.columns)
			}
			requests := bpPlan(t, bpSheetDoc("Data", lines...)).columnRequests("Data", uuid.MustParse(bpDataID), tc.current)
			if got := bpLines(requests); !reflect.DeepEqual(got, tc.lines) {
				t.Fatalf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.lines, "\n"))
			}
			for i, body := range tc.bodies {
				if got := planBody(t, requests[i].Body); got != body {
					t.Errorf("request %d body =\n%s\nwant\n%s", i, got, body)
				}
			}
			for i, note := range tc.notes {
				if requests[i].Note != note {
					t.Errorf("request %d note = %q, want %q", i, requests[i].Note, note)
				}
			}
			for _, r := range requests {
				if r.Method == http.MethodDelete && r.Body != nil {
					t.Errorf("DELETE carries a body: %v", r.Body)
				}
				if r.Method != http.MethodPost && r.ColumnBindingID != r.stored {
					t.Errorf("%s %s: ColumnBindingID %s, want the stored %s", r.Method, r.Path, r.ColumnBindingID, r.stored)
				}
				if r.Method == http.MethodPatch && r.Body[wireTargetColumnIndex] != r.Column {
					t.Errorf("PATCH moves the binding: %v", r.Body)
				}
			}
		})
	}
	if got := bpPlan(t, bpData()).columnRequests("Missing", uuid.MustParse(bpDataID), stored); got != nil {
		t.Errorf("columns of a sheet spec.sheets does not list = %v, want none", bpLines(got))
	}
}

func TestBindingRequestsRefusals(t *testing.T) {
	oldID := uuid.MustParse(bpOldID)
	mirrored := bpTree(bpSheet(bpOldID, "Old", 5, []int{1}, 2))
	mirrored.CellBindings = &[]api.CellBindingResponse{
		{SheetName: "Summary", CellRef: "B2", SameRowsAsSheetBindingID: &oldID},
		{SheetName: "Summary", CellRef: "B1", SameRowsAsSheetBindingID: &oldID},
	}
	untemplated := bpTree(bpSheet(bpOldID, "Old", 5, []int{1}, 2))
	untemplated.SkeletonID = nil
	enabled := func(p *Plan) { p.Revision.State = api.DeliverableRevisionStateEnabled }
	created := func(p *Plan) { p.Revision = nil }
	for _, tc := range []struct {
		name   string
		tree   *api.DeliverableRevisionResponse
		sheets []string
		change func(*Plan)
		says   string // "" when there is no refusal
	}{
		{name: "an enabled release that would change", tree: bpTree(bpStoredData()), change: enabled,
			sheets: []string{bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3")},
			says: `spec.sheets.Data.data_start_row differs from the sheet bindings of deliverable/carrier-a-bordereau release "v3", ` +
				"which is enabled and cannot change; bump spec.release"},
		{name: "an enabled release whose column would change", change: enabled,
			tree:   bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnC, 3, "amount"))),
			sheets: []string{bpData("columns: {C: {column: claim_id}}")}, says: "spec.sheets.Data.columns.C differs"},
		{name: "an enabled release whose sheet would go", tree: bpTree(bpSheet(bpOldID, "Old", 5, []int{1}, 2)), change: enabled,
			says: "spec.sheets.Old differs"},
		{name: "an enabled release that matches", tree: bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2)), change: enabled,
			sheets: []string{bpData("columns: {}")}},
		{name: "a draft with no template", tree: untemplated, sheets: []string{bpData()},
			says: `spec.sheets writes sheet bindings to deliverable/carrier-a-bordereau draft release "v3", which has no template; ` +
				"upload one first: qctl upload deliverable-template FILE --deliverable carrier-a-bordereau --release v3"},
		{name: "a draft with no template that only loses sheets", tree: untemplated},
		{name: "a release created without a template", tree: &api.DeliverableRevisionResponse{}, change: created, sheets: []string{bpData()},
			says: `release "v3", which apply creates without a template; set spec.template`},
		{name: "a release created with a template", tree: &api.DeliverableRevisionResponse{}, sheets: []string{bpData()},
			change: func(p *Plan) { p.Revision, p.TemplatePath = nil, "carrier-a.xlsx" }},
		{name: "a sheet cell bindings mirror", tree: mirrored,
			says: `spec.sheets has no sheet "Old", but cell bindings Summary!B1, Summary!B2 total its rows`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := bpPlan(t, tc.sheets...)
			if tc.change != nil {
				tc.change(p)
			}
			requests, err := p.bindingRequests(tc.tree)
			if tc.says == "" {
				if err != nil {
					t.Fatalf("bindingRequests: %v", err)
				}
				return
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || requests != nil {
				t.Fatalf("requests = %v, exit = %d, want none and %d; error = %v", bpLines(requests), code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error lacks %q: %v", tc.says, err)
			}
		})
	}
}

// bpTemplate gives tree a template inventory holding sheets names.
func bpTemplate(tree *api.DeliverableRevisionResponse, names ...string) *api.DeliverableRevisionResponse {
	sheets := make([]api.WorkbookSheetInventory, 0, len(names))
	for _, name := range names {
		sheets = append(sheets, api.WorkbookSheetInventory{Name: name})
	}
	tree.WorkbookInventory = &api.WorkbookInventory{Sheets: sheets}
	return tree
}

func TestBindingRequestsRefuseASheetTheTemplateLacks(t *testing.T) {
	// copying plans a release apply creates by copying enabled release v2,
	// against tree, the copy's source or the created release v4, with
	// spec.template when replaced.
	copying := func(id string, replaced bool) func(*Plan, *api.DeliverableRevisionResponse) {
		return func(p *Plan, tree *api.DeliverableRevisionResponse) {
			p.Revision, p.CopyFrom = nil, &Revision{ID: uuid.MustParse(revV2), Release: "v2", State: api.DeliverableRevisionStateEnabled}
			tree.ID = uuid.MustParse(id)
			if replaced {
				p.TemplatePath = "carrier-a.xlsx"
			}
		}
	}
	claims := bpSheetDoc("Claims", bpBase, "header_rows: [1]", "data_start_row: 2")
	for _, tc := range []struct {
		name   string
		tree   *api.DeliverableRevisionResponse
		sheets []string
		change func(*Plan, *api.DeliverableRevisionResponse)
		says   string // "" when there is no refusal
	}{
		{name: "a misspelled sheet, whose plan deletes the stored one first",
			tree:   bpTemplate(bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnA, 1, "amount"))), "Data", "Summary"),
			sheets: []string{bpSheetDoc("Daat", bpBase, "header_rows: [1]", "data_start_row: 2")},
			says: `spec.sheets.Daat: the template of deliverable/carrier-a-bordereau release "v3" has no sheet "Daat", and the server refuses ` +
				`a binding to a sheet its template lacks; sheet names match exactly, case included, and its sheets are "Data", "Summary"`},
		{name: "a sheet the template has under another case", tree: bpTemplate(bpTree(), "data"), sheets: []string{bpData()},
			says: `has no sheet "Data"`},
		{name: "a template with no sheets", tree: bpTemplate(bpTree()), sheets: []string{bpData()}, says: `has no sheet "Data"`},
		{name: "a PATCH of a sheet the template no longer has", tree: bpTemplate(bpTree(bpStoredData()), "Summary"),
			sheets: []string{bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3")}, says: `spec.sheets.Data: `},
		{name: "a column write to a sheet the template no longer has", tree: bpTemplate(bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2)), "Summary"),
			sheets: []string{bpData("columns: {A: {column: amount}}")}, says: `spec.sheets.Data: `},
		{name: "a sheet the template has", tree: bpTemplate(bpTree(), "Summary", "Data"), sheets: []string{bpData()}},
		{name: "an unchanged sheet the template no longer has", tree: bpTemplate(bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2)), "Summary"),
			sheets: []string{bpData()}},
		{name: "a sheet the template no longer has, deleted", tree: bpTemplate(bpTree(bpSheet(bpOldID, "Old", 5, []int{1}, 2)), "Summary")},
		{name: "a tree with no inventory", tree: bpTree(), sheets: []string{bpData()}},
		{name: "a copy that keeps its source's template", tree: bpTemplate(bpTree(), "Data"), sheets: []string{claims},
			change: copying(revV2, false), says: `spec.sheets.Claims: the template of deliverable/carrier-a-bordereau release "v3" has no sheet "Claims"`},
		{name: "a copy given spec.template", tree: bpTemplate(bpTree(), "Data"), sheets: []string{claims}, change: copying(revV2, true)},
		{name: "the created copy re-planned against spec.template's inventory", tree: bpTemplate(bpTree(), "Data"), sheets: []string{claims},
			change: copying(revV4, true), says: `release "v3" has no sheet "Claims"`},
		{name: "the created copy re-planned against an inventory with the sheet", tree: bpTemplate(bpTree(), "Claims"), sheets: []string{claims},
			change: copying(revV4, true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := bpPlan(t, tc.sheets...)
			if tc.change != nil {
				tc.change(p, tc.tree)
			}
			requests, err := p.bindingRequests(tc.tree)
			if tc.says == "" {
				if err != nil {
					t.Fatalf("bindingRequests: %v", err)
				}
				return
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || requests != nil {
				t.Fatalf("requests = %v, exit = %d, want none and %d; error = %v", bpLines(requests), code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error lacks %q: %v", tc.says, err)
			}
		})
	}
}

// The created release's inventory is checked when apply re-plans against it.
func TestReplanAfterTheRevisionPostRefusesASheetTheNewTemplateLacks(t *testing.T) {
	p := bpPlan(t, bpSheetDoc("Claims", bpBase, "header_rows: [1]", "data_start_row: 2"))
	p.Revision, p.CopyFrom = nil, &Revision{ID: uuid.MustParse(revV2), Release: "v2", State: api.DeliverableRevisionStateEnabled}
	p.TemplatePath = "carrier-a.xlsx"
	source := bpTemplate(bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2)), "Data")
	source.ID = uuid.MustParse(revV2)
	planned, err := p.bindingRequests(source)
	if err != nil {
		t.Fatalf("bindingRequests: %v", err)
	}
	requests := append([]Request{{Method: http.MethodPost, Path: "/deliverables/" + monthlyID + "/revisions", step: stepRevision}}, planned...)
	created := bpTemplate(bpTree(bpSheet(abCopyData, "Data", 5, []int{1}, 2)), "Data", "Summary")
	created.ID = uuid.MustParse(revV4)
	replanned, err := p.replanned(requests, 0, &replan{tree: created})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || replanned != nil {
		t.Fatalf("re-planned = %v, exit = %d, want none and %d; error = %v", bpLines(replanned), code, qerrors.ExitBadUsage, err)
	}
}

func TestApplyStopsAfterTheRevisionPostAtASheetTheNewTemplateLacks(t *testing.T) {
	dir := planWorkbook(t)
	s := serveApply(t, planDeliverable(nil), disabledV1, enabledV2)
	source := bpTemplate(bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2)), "Data")
	source.ID, source.State = uuid.MustParse(revV2), api.DeliverableRevisionStateEnabled
	s.trees(revV2, source)
	// spec.template has sheet Summary, not the Claims spec.sheets names.
	created := bpTemplate(bpTree(bpSheet(abCopyData, "Data", 5, []int{1}, 2)), "Summary")
	s.on(http.MethodPut, "/deliverable-skeletons/upload-url", planMinted)
	s.on(http.MethodPut, putTargetPath, "")
	s.on(http.MethodPost, "/deliverable-skeletons/finalize", finalizedTemplate)
	s.on(http.MethodPost, "/deliverables/"+monthlyID+"/revisions", api.DeliverableRevisionCreateResponse{ID: uuid.MustParse(revV4),
		DeliverableID: uuid.MustParse(monthlyID), Release: "v3", State: api.DeliverableRevisionStateDraft, SkeletonID: created.SkeletonID,
		CellBindings: created.CellBindings, WorkbookInventory: created.WorkbookInventory, SheetBindings: created.SheetBindings})
	p, n := s.prepare(bpManifest(t, "  template: carrier-a.xlsx\n", bpSheetDoc("Claims", bpBase, "header_rows: [1]", "data_start_row: 2")), dir)
	want := []string{"DELETE /deliverable-revisions/<created-revision-id>/sheet-bindings/<copied-sheet-binding-id>",
		"POST /deliverable-revisions/<created-revision-id>/sheet-bindings"}
	if got := planLines(p)[4:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want the template, the revision POST and %v", planLines(p), want)
	}

	r, err := s.client().Apply("token", p)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || r == nil || r.Revision == nil || r.BindingWrites != 0 {
		t.Fatalf("result = %+v, exit = %d, want the created release, no binding write and %d; error = %v", r, code, qerrors.ExitBadUsage, err)
	}
	s.expect(n,
		`PUT /deliverable-skeletons/upload-url {"file_size":8,"original_filename":"carrier-a.xlsx"}`,
		"PUT "+putTargetPath+" <8 bytes>",
		`POST /deliverable-skeletons/finalize {"acknowledge_lossy":false,"skeleton_id":41}`,
		"POST /deliverables/"+monthlyID+`/revisions {"acknowledge_lossy":false,"copy_from_revision_id":"`+revV2+`","release":"v3","skeleton_id":7}`)
	if !strings.Contains(err.Error(), `has no sheet "Claims"`) || !strings.Contains(err.Error(), `its sheets are "Summary"`) {
		t.Errorf("error = %v, want the missing sheet and the template's sheets named", err)
	}
}

func TestBindingRequestsAgainstACopyCarryPlaceholders(t *testing.T) {
	source := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnC, 3, "amount")), bpSheet(bpOldID, "Old", 5, []int{1}, 2))
	source.ID, source.State = uuid.MustParse(revV2), api.DeliverableRevisionStateEnabled
	p := bpPlan(t, bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3", "columns: {C: {column: claim_id}}"))
	p.Revision, p.CopyFrom = nil, &Revision{ID: uuid.MustParse(revV2), Release: "v2", State: api.DeliverableRevisionStateEnabled}
	requests, err := p.bindingRequests(source)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DELETE /deliverable-revisions/<created-revision-id>/sheet-bindings/<copied-sheet-binding-id>",
		"PATCH /deliverable-revisions/<created-revision-id>/sheet-bindings/<copied-sheet-binding-id>",
		"PATCH /deliverable-sheet-bindings/<copied-sheet-binding-id>/column-bindings/<copied-column-binding-id>",
	}
	if got := bpLines(requests); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, r := range requests {
		if r.SheetBindingID != uuid.Nil || r.ColumnBindingID != uuid.Nil {
			t.Errorf("%s %s names the source's ids: %s/%s", r.Method, r.Path, r.SheetBindingID, r.ColumnBindingID)
		}
	}
	// The created release's own tree, as apply re-plans against it.
	created := bpTree(bpSheet(bpOtherID, "Data", 5, []int{1}, 2, bpColumn(bpColumnD, 3, "amount")))
	created.ID = uuid.MustParse(revV4)
	requests, err = p.bindingRequests(created)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{
		"PATCH /deliverable-revisions/" + revV4 + "/sheet-bindings/" + bpOtherID,
		"PATCH /deliverable-sheet-bindings/" + bpOtherID + "/column-bindings/" + bpColumnD,
	}
	if got := bpLines(requests); !reflect.DeepEqual(got, want) {
		t.Fatalf("re-planned requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if requests[1].SheetBindingID.String() != bpOtherID || requests[1].ColumnBindingID.String() != bpColumnD {
		t.Errorf("re-planned ids = %s/%s", requests[1].SheetBindingID, requests[1].ColumnBindingID)
	}
}

func TestBindingRequestsWithoutSpecSheets(t *testing.T) {
	requests, err := (&Plan{}).bindingRequests(bpTree(bpStoredData()))
	if err != nil || requests != nil {
		t.Fatalf("requests = %v, error = %v; want none", requests, err)
	}
	if got := (&Plan{}).columnRequests("Data", uuid.MustParse(bpDataID), []api.ColumnBindingResponse{bpColumn(bpColumnA, 1, "amount")}); got != nil {
		t.Fatalf("column requests = %v, want none", bpLines(got))
	}
}

func TestMayRemove(t *testing.T) {
	data := bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnA, 1, "amount"))
	empty := bpSheet(bpDataID, "Data", 5, []int{1}, 2)
	copying := func(p *Plan) {
		p.Revision, p.CopyFrom = nil, &Revision{ID: uuid.MustParse(revV2), Release: "v2", State: api.DeliverableRevisionStateEnabled}
	}
	for _, tc := range []struct {
		name   string
		stored api.SheetBindingResponse
		sheet  string
		change func(*Plan)
		want   bool
	}{
		{name: "a layout PATCH of listed columns", stored: data, want: true,
			sheet: bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3", "columns: {A: {column: amount}}")},
		{name: "a layout PATCH of columns not listed", stored: data, sheet: bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3")},
		{name: "a layout PATCH of a sheet with no column bindings", stored: empty,
			sheet: bpSheetDoc("Data", bpBase, "header_rows: [1]", "data_start_row: 3", "columns: {A: {column: amount}}")},
		{name: "listed columns without a layout change", stored: data, sheet: bpData("columns: {A: {column: amount}}")},
		{name: "a copy given spec.template", stored: data, sheet: bpData("columns: {A: {column: amount}}"), want: true,
			change: func(p *Plan) { copying(p); p.TemplatePath = "carrier-a.xlsx" }},
		{name: "a copy keeping its template", stored: data, sheet: bpData("columns: {A: {column: amount}}"), change: copying},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := bpPlan(t, tc.sheet)
			if tc.change != nil {
				tc.change(p)
			}
			if got := p.mayRemove(bpTree(tc.stored)); got != tc.want {
				t.Errorf("mayRemove = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfirmationNotes(t *testing.T) {
	tree := bpTree(
		bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpFlagged(bpColumn(bpColumnC, 3, "amount")), bpColumn(bpColumnD, 4, "policy_id"),
			bpFlagged(bpColumn(bpColumnA, 1, "$period"))),
		bpSheet(bpOtherID, "Q3 data", 5, []int{1}, 2, bpFlagged(bpColumn(bpColumnB, 2, "amount"))),
		bpSheet(bpOldID, "Other", 5, []int{1}, 2, bpFlagged(bpColumn("5c000000-0000-4000-8000-000000000005", 3, "amount"))),
	)
	cleared := map[uuid.UUID]bool{uuid.MustParse("5c000000-0000-4000-8000-000000000005"): true}
	want := []string{
		`sheet "Data" columns A, C need confirmation: a template change or a sheet rename left them in place with no header label to re-anchor by; ` +
			"check: qctl describe deliverable-revision carrier-a-bordereau --release 'v3 beta'; " +
			"confirm: qctl deliverable confirm-columns carrier-a-bordereau --release 'v3 beta' --sheet Data --column A,C",
		`sheet "Q3 data" column B needs confirmation: a template change or a sheet rename left it in place with no header label to re-anchor by; ` +
			"check: qctl describe deliverable-revision carrier-a-bordereau --release 'v3 beta'; " +
			"confirm: qctl deliverable confirm-columns carrier-a-bordereau --release 'v3 beta' --sheet 'Q3 data' --column B",
	}
	if got := confirmationNotes("carrier-a-bordereau", "v3 beta", tree, cleared); !reflect.DeepEqual(got, want) {
		t.Errorf("notes =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSheetNotesSkipFlagsAPlannedWriteClears(t *testing.T) {
	tree := bpTree(
		bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpFlagged(bpColumn(bpColumnA, 1, "$period")), bpFlagged(bpColumn(bpColumnC, 3, "amount"))),
		bpSheet(bpOldID, "Old", 5, []int{1}, 2, bpFlagged(bpColumn(bpColumnB, 2, "amount"))),
	)
	m := bpManifest(t, "", bpData("columns: {A: {column: period}, C: {column: amount}}"))
	p := bpPlanWith(t, m)
	requests, err := p.bindingRequests(tree)
	if err != nil {
		t.Fatal(err)
	}
	notes := p.draftConfirmations(tree, requests)
	if len(notes) != 1 || !strings.HasPrefix(notes[0], `sheet "Data" column C needs confirmation`) || !strings.HasSuffix(notes[0], "--column C") {
		t.Errorf("notes = %q, want one naming Data column C only", notes)
	}
	if i := slices.IndexFunc(requests, func(r Request) bool { return r.step == stepColumnPatch }); i < 0 || !strings.Contains(requests[i].Note, "also confirms it") {
		t.Errorf("the PATCH of flagged column A is not marked: %v", requests)
	}
	p.Revision.State = api.DeliverableRevisionStateDisabled
	if notes := p.draftConfirmations(tree, nil); len(notes) != 0 {
		t.Errorf("notes on a disabled release = %q, want none", notes)
	}
}

func TestSeriesNotes(t *testing.T) {
	period := "period"
	series := uuid.MustParse(bpSeriesID)
	following := bpSheet(bpDataID, "Data", 5, []int{1}, 2)
	following.MemberSeriesID = &series
	plain := bpSheet(bpDataID, "Data", 5, []int{1}, 2)
	note := `sheet "Data" follows a table series, but the deliverable has no period_param: validate reports an error until spec.period_param is set`
	for _, tc := range []struct {
		name    string
		extra   string
		current *string
		stored  api.SheetBindingResponse
		sheet   string
		want    bool
	}{
		{name: "a written series without a period_param", stored: plain, sheet: bpData(`series: "` + bpSeriesID + `"`), want: true},
		{name: "a written series with a period_param", current: &period, stored: plain, sheet: bpData(`series: "` + bpSeriesID + `"`)},
		{name: "a stored series kept while the manifest clears period_param", extra: "  period_param: null\n", current: &period,
			stored: following, sheet: bpData(), want: true},
		{name: "a stored series cleared", stored: following, sheet: bpData("series: null")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := bpManifest(t, tc.extra, tc.sheet)
			p := bpPlanWith(t, m)
			p.Current = &Deliverable{PeriodParam: tc.current}
			got := p.seriesNotes(m, bpTree(tc.stored))
			if want := []string{note}; tc.want != (len(got) > 0) || tc.want && !reflect.DeepEqual(got, want) {
				t.Errorf("notes = %q, want note: %v", got, tc.want)
			}
		})
	}
}

// serveBindings serves the tree of revision id at GET deliverable-revisions/id,
// table claims as id 5, and series Monthly claims.
func (b *planBackend) serveBindings(id string, tree *api.DeliverableRevisionResponse) {
	b.mock.RegisterHandler(http.MethodGet, bpTreePath+id, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, tree)
	})
	b.mock.RegisterHandler(http.MethodGet, bpDatasets, func(w http.ResponseWriter, r *http.Request) {
		results := []datasets.DatasetTiny{}
		if r.URL.Query().Get("name") == "claims" {
			results = append(results, datasets.DatasetTiny{ID: 5, Name: "claims"})
		}
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{Results: results})
	})
	b.mock.RegisterHandler(http.MethodGet, exportSeriesPath, func(w http.ResponseWriter, _ *http.Request) {
		respondBody(w, http.StatusOK, exportSeriesBody)
	})
}

func TestPreparePlansTheSheetsOfAnExistingDraft(t *testing.T) {
	b := servePlan(t, planDeliverable(nil), enabledV2, draftV3)
	b.serveBindings(revV3, bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnC, 3, "amount"))))
	m := planManifest(t, deliverableManifestBasics+"  release: v3\n  sheets:\n"+
		bpSheetDoc("Data", "table: claims", "series: Monthly claims", "header_rows: [1]", "data_start_row: 2", "columns: {C: {column: amount}, D: {column: claim_id}}"))
	p, err := b.client().Prepare("token", m, ".")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got, want := b.mock.Requests(), []string{planListing, planRead, planRevisions, "GET " + bpDatasets, "GET " + exportSeriesPath,
		"GET " + bpTreePath + revV3, "GET " + bpDatasets + "/5/migration-details"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests made =\n%v\nwant\n%v", got, want)
	}
	if got, want := planLines(p), []string{"PATCH " + bpSheets + "/" + bpDataID, "POST " + bpDataColumns}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	patch := p.Requests[0]
	if patch.Body[wireMemberDatasetID] != 5 || patch.Body[wireMemberSeriesID] != uuid.MustParse(namedSeriesID) || patch.Note != "changes series" {
		t.Errorf("PATCH = %+v", patch)
	}
	if p.Tree == nil || p.Tree.ID.String() != revV3 || p.MayRemove || p.Removals != nil || len(p.Notes) != 0 {
		t.Errorf("Tree = %v, MayRemove = %v, Removals = %v, Notes = %v", p.Tree, p.MayRemove, p.Removals, p.Notes)
	}
	for _, r := range p.Requests {
		if r.SheetBindingID.String() != bpDataID {
			t.Errorf("%s %s: SheetBindingID = %s", r.Method, r.Path, r.SheetBindingID)
		}
	}
}

func TestPreparePlansTheSheetsOfACopiedRelease(t *testing.T) {
	dir := planWorkbook(t)
	b := servePlan(t, planDeliverable(nil), disabledV1, enabledV2)
	source := bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpColumn(bpColumnC, 3, "amount")))
	source.ID, source.State = uuid.MustParse(revV2), api.DeliverableRevisionStateEnabled
	b.serveBindings(revV2, source)
	m := planManifest(t, deliverableManifestBasics+"  release: v4\n  template: carrier-a.xlsx\n  sheets:\n"+
		bpData(`columns: {C: {column: amount, format: "0.00"}}`))
	p, err := b.client().Prepare("token", m, dir)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	want := []string{"PUT /deliverable-skeletons/upload-url", "<upload method> <presigned upload URL>", "POST /deliverable-skeletons/finalize",
		"POST /deliverables/" + monthlyID + "/revisions",
		"PATCH /deliverable-sheet-bindings/<copied-sheet-binding-id>/column-bindings/<copied-column-binding-id>"}
	if got := planLines(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := b.mock.Requests(); got[len(got)-2] != "GET "+bpTreePath+revV2 || got[len(got)-1] != "GET "+bpDatasets+"/5/migration-details" {
		t.Errorf("requests made = %v, want the copied revision's tree then its table schema", got)
	}
	if !p.MayRemove || p.Tree.ID.String() != revV2 || p.Removals != nil {
		t.Errorf("MayRemove = %v, Tree = %s, Removals = %v", p.MayRemove, p.Tree.ID, p.Removals)
	}
	if want := []string{`spec.template replaces the template the copy of release "v2" starts with: the server may re-anchor ` +
		"the copied column bindings, and apply plans their writes from the created release"}; !reflect.DeepEqual(p.Notes, want) {
		t.Errorf("notes = %q, want %q", p.Notes, want)
	}
}

func TestPreparePlansTheSheetsOfAReleaseFromScratch(t *testing.T) {
	dir := planWorkbook(t)
	b := servePlan(t, planDeliverable(nil), disabledV1)
	m := planManifest(t, deliverableManifestBasics+"  release: v4\n  template: carrier-a.xlsx\n  sheets:\n"+bpData("columns: {C: {column: amount}}"))
	p, err := b.client().Prepare("token", m, dir)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	want := []string{"POST /deliverables/" + monthlyID + "/revisions", "POST /deliverable-revisions/<created-revision-id>/sheet-bindings",
		"POST /deliverable-sheet-bindings/<created-sheet-binding-id>/column-bindings"}
	if got := planLines(p)[3:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %v, want the template and then %v", planLines(p), want)
	}
	if got, want := b.mock.Requests(), []string{planListing, planRead, planRevisions, "GET " + bpDatasets + "/5/migration-details"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests made = %v, want %v", got, want)
	}
	if p.Tree == nil || p.Tree.ID != uuid.Nil || p.MayRemove {
		t.Errorf("Tree = %+v, MayRemove = %v; want an empty tree", p.Tree, p.MayRemove)
	}
}

func TestPrepareWithoutSpecSheetsReadsNoTree(t *testing.T) {
	b := servePlan(t, planDeliverable(nil), enabledV2, draftV3)
	p, err := b.client().Prepare("token", planManifest(t, deliverableManifestBasics+"  release: v3\n"), ".")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b.mock.Requests(), []string{planListing, planRead, planRevisions}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests made = %v, want %v", got, want)
	}
	if p.Tree != nil || len(p.Requests) != 0 || p.MayRemove || p.Removals != nil {
		t.Errorf("plan = %+v, want no binding plan", p)
	}
}

func TestPrepareNamesTheSheetKeyAResolutionFails(t *testing.T) {
	for _, tc := range []struct{ name, sheet, prefix string }{
		{name: "table", sheet: bpSheetDoc("Data", "table: missing", "header_rows: []", "data_start_row: 1"), prefix: "spec.sheets.Data.table: "},
		{name: "series", sheet: bpSheetDoc("Data", "table: claims", "series: missing", "header_rows: []", "data_start_row: 1"), prefix: "spec.sheets.Data.series: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := servePlan(t, planDeliverable(nil), draftV3)
			b.serveBindings(revV3, bpTree())
			p, err := b.client().Prepare("token", planManifest(t, deliverableManifestBasics+"  release: v3\n  sheets:\n"+tc.sheet), ".")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound || p != nil {
				t.Fatalf("plan = %v, exit = %d, want nil and %d; error = %v", p, code, qerrors.ExitNotFound, err)
			}
			if !strings.HasPrefix(err.Error(), tc.prefix) {
				t.Errorf("error does not start with %q: %v", tc.prefix, err)
			}
			if got := b.mock.Requests(); slices.Contains(got, "GET "+bpTreePath+revV3) {
				t.Errorf("requests made = %v, want no tree read", got)
			}
		})
	}
}

func TestWriteRequestsPrintsBindingWrites(t *testing.T) {
	b := servePlan(t, planDeliverable(nil), enabledV2, draftV3)
	b.serveBindings(revV3, bpTree(
		bpSheet(bpDataID, "Data", 5, []int{1}, 2, bpFlagged(bpColumn(bpColumnA, 1, "$period")), bpColumn(bpColumnC, 3, "amount")),
		bpSheet(bpOldID, "Old", 5, []int{1}, 2),
	))
	m := planManifest(t, deliverableManifestBasics+"  release: v3\n  sheets:\n"+
		bpSheetDoc("Data", bpBase, "header_rows: [2]", "data_start_row: 3", "columns: {A: {column: period_label}, B: {run_param: carrier}, C: {column: amount}}"))
	p, err := b.client().Prepare("token", m, ".")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.WriteRequests(&out); err != nil {
		t.Fatal(err)
	}
	want := `DELETE /deliverable-revisions/` + revV3 + `/sheet-bindings/` + bpOldID + `
# sheet "Old" is not in spec.sheets
PATCH /deliverable-revisions/` + revV3 + `/sheet-bindings/` + bpDataID + `
# changes header_rows, data_start_row; the server re-anchors the sheet's column bindings by their header labels, and apply plans its column writes again from the response
{
  "data_start_row": 3,
  "header_rows": [
    2
  ],
  "instruction_row_count": 0,
  "member_dataset_id": 5,
  "member_series_id": null,
  "sheet_name": "Data",
  "sort_keys": []
}
PATCH /deliverable-sheet-bindings/` + bpDataID + `/column-bindings/` + bpColumnA + `
# sheet "Data" column A; also confirms it: the binding needs confirmation, which any save clears
{
  "format_override": null,
  "run_param": null,
  "source_column": "period_label",
  "source_type": "column",
  "target_column_index": 1
}
POST /deliverable-sheet-bindings/` + bpDataID + `/column-bindings
# sheet "Data" column B
{
  "format_override": null,
  "run_param": "carrier",
  "source_column": null,
  "source_type": "run_param",
  "target_column_index": 2
}
`
	if got := out.String(); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
	if want := []string{`sheet "Old"`}; !reflect.DeepEqual(p.Removals, want) || !p.MayRemove {
		t.Errorf("Removals = %q, MayRemove = %v; want %q and true", p.Removals, p.MayRemove, want)
	}
}
