package deliverables

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/row_filters"
)

const sheetsManifestHead = deliverableManifestHeader + deliverableManifestBasics + "  release: v2\n  sheets:\n"

// indentLines indents each line by n spaces and ends it with a newline.
func indentLines(n int, lines ...string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(strings.Repeat(" ", n) + line + "\n")
	}
	return b.String()
}

// sheetDoc is a manifest whose spec.sheets has one sheet, Data, made of lines.
func sheetDoc(lines ...string) string {
	return sheetsManifestHead + "    Data:\n" + indentLines(6, lines...)
}

// validSheet is a valid Data sheet followed by lines.
func validSheet(lines ...string) string {
	return sheetDoc(append([]string{"table: claims", "header_rows: [1]", "data_start_row: 2"}, lines...)...)
}

func TestParseManifestRejectsBadSheets(t *testing.T) {
	for _, tc := range []struct{ name, doc, message string }{
		{"null sheets", sheetsManifestHead[:len(sheetsManifestHead)-1] + " null\n", "spec.sheets must be a mapping"},
		{"sheets a list", sheetsManifestHead[:len(sheetsManifestHead)-1] + " []\n", "invalid Deliverable manifest"},
		{"empty sheet name", sheetsManifestHead + "    \"\":\n" + indentLines(6, "table: claims", "header_rows: []", "data_start_row: 1"), "spec.sheets: a sheet name cannot be empty"},
		{"null sheet", sheetsManifestHead + "    Data: null\n", "spec.sheets.Data.table is required"},
		{"missing table", sheetDoc("header_rows: [1]", "data_start_row: 2"), "spec.sheets.Data.table is required"},
		{"blank table", sheetDoc(`table: " "`, "header_rows: [1]", "data_start_row: 2"), "spec.sheets.Data.table is required"},
		{"zero table ID", sheetDoc("table: 0", "header_rows: [1]", "data_start_row: 2"), "spec.sheets.Data.table: a table ID must be positive"},
		{"negative table ID", sheetDoc(`table: "-4"`, "header_rows: [1]", "data_start_row: 2"), "spec.sheets.Data.table: a table ID must be positive"},
		{"blank series", validSheet(`series: " "`), "spec.sheets.Data.series must be non-blank"},
		{"empty series", validSheet(`series: ""`), "spec.sheets.Data.series must be non-blank"},
		{"missing data_start_row", sheetDoc("table: claims", "header_rows: []"), "spec.sheets.Data.data_start_row is required"},
		{"zero data_start_row", sheetDoc("table: claims", "header_rows: []", "data_start_row: 0"), "spec.sheets.Data.data_start_row must be at least 1"},
		{"null data_start_row", sheetDoc("table: claims", "header_rows: []", "data_start_row: null"), "spec.sheets.Data.data_start_row must be at least 1"},
		{"missing header_rows", sheetDoc("table: claims", "data_start_row: 2"), "spec.sheets.Data.header_rows is required"},
		{"null header_rows", sheetDoc("table: claims", "header_rows: null", "data_start_row: 2"), "spec.sheets.Data.header_rows is required"},
		{"four header_rows", sheetDoc("table: claims", "header_rows: [1, 2, 3, 4]", "data_start_row: 5"), "spec.sheets.Data.header_rows has at most 3 rows"},
		{"header row 0", sheetDoc("table: claims", "header_rows: [0]", "data_start_row: 2"), "spec.sheets.Data.header_rows[0] must be at least 1"},
		{"header rows decreasing", sheetDoc("table: claims", "header_rows: [2, 1]", "data_start_row: 3"), "spec.sheets.Data.header_rows must be strictly increasing"},
		{"header rows repeated", sheetDoc("table: claims", "header_rows: [1, 1]", "data_start_row: 3"), "spec.sheets.Data.header_rows must be strictly increasing"},
		{"header row at data_start_row", sheetDoc("table: claims", "header_rows: [2]", "data_start_row: 2"), "spec.sheets.Data.header_rows[0] must be above data_start_row 2"},
		{"header row below data_start_row", sheetDoc("table: claims", "header_rows: [1, 3]", "data_start_row: 2"), "spec.sheets.Data.header_rows[1] must be above data_start_row 2"},
		{"negative instruction_row_count", validSheet("instruction_row_count: -1"), "spec.sheets.Data.instruction_row_count must be 0–10"},
		{"instruction_row_count over 10", validSheet("instruction_row_count: 11"), "spec.sheets.Data.instruction_row_count must be 0–10"},
		{"null instruction_row_count", validSheet("instruction_row_count: null"), "spec.sheets.Data.instruction_row_count must be 0–10"},
		{"null row_order", validSheet("row_order: null"), "spec.sheets.Data.row_order must be a list"},
		{"row_order without a column", validSheet("row_order:", "  - descending: true"), "spec.sheets.Data.row_order[0].column is required"},
		{"row_order blank column", validSheet("row_order:", `  - column: "   "`), "spec.sheets.Data.row_order[0].column must be non-blank"},
		{"row_order blank column after a valid one", validSheet("row_order:", "  - column: amount", `  - {column: "\t", descending: true}`), "spec.sheets.Data.row_order[1].column must be non-blank"},
		{"row_order column twice", validSheet("row_order:", "  - column: amount", "  - {column: amount, descending: true}"), `spec.sheets.Data.row_order[1]: column "amount" is listed twice`},
		{"row_order with an unknown key", validSheet("row_order:", "  - {column: amount, position: 0}"), "position"},
		{"null columns", validSheet("columns: null"), "spec.sheets.Data.columns must be a mapping"},
		{"columns a list", validSheet("columns: []"), "invalid Deliverable manifest"},
		{"column and run_param", validSheet("columns:", "  C: {column: amount, run_param: period}"), "spec.sheets.Data.columns.C: set exactly one of column, run_param or literal"},
		{"neither column nor run_param", validSheet("columns:", "  C: {}"), "spec.sheets.Data.columns.C: set exactly one of column, run_param or literal"},
		{"null column", validSheet("columns:", "  C: null"), "spec.sheets.Data.columns.C: set exactly one of column, run_param or literal"},
		{"format alone", validSheet("columns:", "  C: {format: '0.00'}"), "spec.sheets.Data.columns.C: set exactly one of column, run_param or literal"},
		{"blank run_param", validSheet("columns:", `  C: {run_param: " "}`), "spec.sheets.Data.columns.C.run_param must be non-blank"},
		{"blank column", validSheet("columns:", `  A: {column: "   "}`), "spec.sheets.Data.columns.A.column must be non-blank"},
		{"blank column with a format", validSheet("columns:", `  A: {column: "\u001f", format: "0.00"}`), "spec.sheets.Data.columns.A.column must be non-blank"},
		{"blank column beside run_param", validSheet("columns:", `  A: {column: "   ", run_param: period}`), "spec.sheets.Data.columns.A: set exactly one of column, run_param or literal"},
		{"blank run_param beside column", validSheet("columns:", `  A: {column: amount, run_param: "   "}`), "spec.sheets.Data.columns.A: set exactly one of column, run_param or literal"},
		{"blank row_order column in JSON", `{"apiVersion":"qluster.ai/v1","kind":"Deliverable","metadata":{"slug":"a"},"spec":{"name":"A","filename_pattern":"a.xlsx","release":"v1",` +
			`"sheets":{"Data":{"table":"7","header_rows":[],"data_start_row":1,"row_order":[{"column":" "}],"columns":{"A":{"column":"x"}}}}}}`, "spec.sheets.Data.row_order[0].column must be non-blank"},
		{"column past XFD", validSheet("columns:", "  XFE: {column: amount}"), "spec.sheets.Data.columns.XFE: "},
		{"index past 16384", validSheet("columns:", "  16385: {column: amount}"), "spec.sheets.Data.columns.16385: "},
		{"index 0", validSheet("columns:", "  0: {column: amount}"), "spec.sheets.Data.columns.0: "},
		{"letter and number", validSheet("columns:", "  C3: {column: amount}"), "spec.sheets.Data.columns.C3: "},
		{"C and 3", validSheet("columns:", "  C: {column: amount}", "  3: {column: premium}"), "spec.sheets.Data.columns: 3 and C both name column C"},
		{"C and c", validSheet("columns:", "  C: {column: amount}", "  c: {column: premium}"), "spec.sheets.Data.columns: C and c both name column C"},
		{"unknown column key", validSheet("columns:", "  C: {column: amount, colour: blue}"), "colour"},
		{"unknown sheet key", validSheet("colour: blue"), "colour"},
		{"row_filter beside an unknown key", validSheet("colour: blue", "row_filter: null"), "colour"},
		{"unknown row_filter key", validSheet("row_filter: {description: open, colour: blue}"), "spec.sheets.Data.row_filter.colour: unknown field"},
		{"row_filter a list", validSheet("row_filter: []"), "spec.sheets.Data.row_filter: expected a mapping"},
		{"C and 3 in JSON", `{"apiVersion":"qluster.ai/v1","kind":"Deliverable","metadata":{"slug":"a"},"spec":{"name":"A","filename_pattern":"a.xlsx","release":"v1",` +
			`"sheets":{"Data":{"table":"7","header_rows":[],"data_start_row":1,"columns":{"3":{"column":"x"},"C":{"column":"y"}}}}}}`, "spec.sheets.Data.columns: 3 and C both name column C"},
		{"spec.cells beside spec.sheets", validSheet() + "  cells: []\n", "invalid Deliverable manifest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(tc.doc))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d (err %v, manifest %#v), want 2", code, err, m)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error %q does not mention %q", err, tc.message)
			}
		})
	}
}

// exportedSheet is s without its marks and column indexes.
func exportedSheet(s ManifestSheet) ManifestSheet {
	s.present, s.byIndex = nil, nil
	return s
}

func TestParseManifestReadsEverySheetField(t *testing.T) {
	doc := sheetsManifestHead + indentLines(4,
		"Data:",
		"  table: claims",
		"  series: monthly claims",
		"  header_rows: [1, 2]",
		"  data_start_row: 3",
		"  instruction_row_count: 0",
		"  row_order:",
		"    - column: policy_id",
		"    - {column: amount, descending: true}",
		"  columns:",
		"    A: {column: policy_id}",
		`    2: {column: amount, format: "#,##0.00"}`,
		"    c: {run_param: period}",
		"    xfd: {column: note}",
		"    16383: {column: other}",
		"Empty:",
		`  table: "12"`,
		"  header_rows: []",
		"  data_start_row: 1",
	)
	m, err := ParseManifest([]byte(doc))
	if err != nil {
		t.Fatalf("ParseManifest: %v (exit %d)", err, qerrors.GetExitCode(err))
	}
	series, zero, format := "monthly claims", 0, "#,##0.00"
	policy, amount, period := ManifestColumn{Column: "policy_id"}, ManifestColumn{Column: "amount", Format: &format}, ManifestColumn{RunParam: "period"}
	note, other := ManifestColumn{Column: "note"}, ManifestColumn{Column: "other"}
	want := ManifestSheet{
		Table: "claims", Series: &series, HeaderRows: []int{1, 2}, DataStartRow: 3, InstructionRowCount: &zero,
		RowOrder: []ManifestSortKey{{Column: "policy_id"}, {Column: "amount", Descending: true}},
		Columns:  map[string]ManifestColumn{"A": policy, "2": amount, "c": period, "xfd": note, "16383": other},
	}
	data := m.Spec.Sheets["Data"]
	if got := exportedSheet(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("Data = %#v, want %#v", got, want)
	}
	wantIndexes := map[int]ManifestColumn{1: policy, 2: amount, 3: period, 16384: note, 16383: other}
	if got := data.ColumnsByIndex(); !reflect.DeepEqual(got, wantIndexes) {
		t.Fatalf("ColumnsByIndex = %#v, want %#v", got, wantIndexes)
	}
	for _, key := range []string{"table", "series", "header_rows", "data_start_row", "instruction_row_count", "row_order", "columns"} {
		if !data.Has(key) {
			t.Errorf("Data.Has(%q) = false", key)
		}
	}
	empty := m.Spec.Sheets["Empty"]
	if want := (ManifestSheet{Table: "12", HeaderRows: []int{}, DataStartRow: 1}); !reflect.DeepEqual(exportedSheet(empty), want) {
		t.Fatalf("Empty = %#v, want %#v", exportedSheet(empty), want)
	}
	for key, want := range map[string]bool{"table": true, "header_rows": true, "data_start_row": true, "series": false, "instruction_row_count": false, "row_order": false, "columns": false, "row_filter": false} {
		if got := empty.Has(key); got != want {
			t.Errorf("Empty.Has(%q) = %v, want %v", key, got, want)
		}
	}
	if empty.ColumnsByIndex() != nil {
		t.Fatalf("ColumnsByIndex of a sheet without columns = %#v, want nil", empty.ColumnsByIndex())
	}
	if !m.Has("sheets") || len(m.Spec.Sheets) != 2 {
		t.Fatalf("Has(sheets) = %v, sheets = %#v", m.Has("sheets"), m.Spec.Sheets)
	}
}

func TestParseManifestReadsSheetsFromJSONAsFromYAML(t *testing.T) {
	const jsonDoc = `{"apiVersion":"qluster.ai/v1","kind":"Deliverable","metadata":{"slug":"a"},"spec":{"name":"A","filename_pattern":"a.xlsx","release":"v1",` +
		`"sheets":{"Data":{"table":7,"series":null,"header_rows":[1],"data_start_row":2,"row_order":[],"columns":{"3":{"run_param":"period"},"D":{"column":"amount","format":null}}}}}}`
	yamlDoc := "apiVersion: qluster.ai/v1\nkind: Deliverable\nmetadata:\n  slug: a\nspec:\n  name: A\n  filename_pattern: a.xlsx\n  release: v1\n  sheets:\n" +
		indentLines(4, "Data:", "  table: 7", "  series: null", "  header_rows: [1]", "  data_start_row: 2", "  row_order: []", "  columns:", "    3: {run_param: period}", "    D: {column: amount, format: null}")
	fromJSON, err := ParseManifest([]byte(jsonDoc))
	if err != nil {
		t.Fatalf("ParseManifest(JSON): %v", err)
	}
	fromYAML, err := ParseManifest([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("ParseManifest(YAML): %v", err)
	}
	if !reflect.DeepEqual(fromJSON.Spec.Sheets, fromYAML.Spec.Sheets) {
		t.Fatalf("JSON sheets = %#v, YAML sheets = %#v", fromJSON.Spec.Sheets, fromYAML.Spec.Sheets)
	}
	data := fromYAML.Spec.Sheets["Data"]
	if _, ok := data.Columns["3"]; !ok {
		t.Fatalf("YAML key 3 did not decode as \"3\": %#v", data.Columns)
	}
	want := map[int]ManifestColumn{3: {RunParam: "period"}, 4: {Column: "amount"}}
	if !reflect.DeepEqual(data.ColumnsByIndex(), want) {
		t.Fatalf("ColumnsByIndex = %#v, want %#v", data.ColumnsByIndex(), want)
	}
	if data.RowOrder == nil || len(data.RowOrder) != 0 || !data.Has("row_order") {
		t.Fatalf("row_order: [] = %#v, Has = %v", data.RowOrder, data.Has("row_order"))
	}
}

func TestParseManifestTellsAbsentSheetsAndColumnsFromEmpty(t *testing.T) {
	absent, err := ParseManifest([]byte(deliverableManifestHeader + deliverableManifestBasics + "  release: v2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if absent.Has("sheets") || absent.Spec.Sheets != nil {
		t.Fatalf("absent sheets: Has = %v, sheets = %#v", absent.Has("sheets"), absent.Spec.Sheets)
	}
	empty, err := ParseManifest([]byte(sheetsManifestHead[:len(sheetsManifestHead)-1] + " {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !empty.Has("sheets") || empty.Spec.Sheets == nil || len(empty.Spec.Sheets) != 0 {
		t.Fatalf("sheets: {}: Has = %v, sheets = %#v", empty.Has("sheets"), empty.Spec.Sheets)
	}
	noColumns, err := ParseManifest([]byte(validSheet("columns: {}")))
	if err != nil {
		t.Fatal(err)
	}
	if sheet := noColumns.Spec.Sheets["Data"]; !sheet.Has("columns") || sheet.ColumnsByIndex() == nil || len(sheet.ColumnsByIndex()) != 0 {
		t.Fatalf("columns: {}: Has = %v, indexes = %#v", sheet.Has("columns"), sheet.ColumnsByIndex())
	}
}

func TestSheetSeriesTellsAbsentFromNullFromSet(t *testing.T) {
	name := "monthly claims"
	for _, tc := range []struct {
		name, line string
		has        bool
		want       *string
	}{
		{"absent", "", false, nil},
		{"null", "series: null", true, nil},
		{"name", "series: monthly claims", true, &name},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := []string{}
			if tc.line != "" {
				lines = append(lines, tc.line)
			}
			m, err := ParseManifest([]byte(validSheet(lines...)))
			if err != nil {
				t.Fatal(err)
			}
			sheet := m.Spec.Sheets["Data"]
			if sheet.Has("series") != tc.has || !reflect.DeepEqual(sheet.Series, tc.want) {
				t.Fatalf("Has(series) = %v, series = %v; want %v, %v", sheet.Has("series"), sheet.Series, tc.has, tc.want)
			}
		})
	}
}

func TestParseColumnKey(t *testing.T) {
	for key, want := range map[string]int{
		"A": 1, "a": 1, "Z": 26, "AA": 27, "aZ": 52, "XFD": 16384, "xfd": 16384, "XFC": 16383,
		"1": 1, "3": 3, "26": 26, "16384": 16384,
	} {
		if got, err := ParseColumnKey(key); err != nil || got != want {
			t.Errorf("ParseColumnKey(%q) = %d, %v; want %d", key, got, err, want)
		}
	}
	for _, key := range []string{"", "0", "03", "16385", "99999999999999999999", "XFE", "ZZZ", "AAAA", "A1", "1A", " A", "A ", "-1", "+1", "Ä", "A-"} {
		if _, err := ParseColumnKey(key); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Errorf("ParseColumnKey(%q): exit %d (err %v), want 2", key, qerrors.GetExitCode(err), err)
		}
	}
}

func TestColumnLetterRoundTripsEveryColumn(t *testing.T) {
	for index := 1; index <= maxColumnIndex; index++ {
		letter := ColumnLetter(index)
		byLetter, err := ParseColumnKey(letter)
		if err != nil || byLetter != index {
			t.Fatalf("ParseColumnKey(ColumnLetter(%d) = %q) = %d, %v", index, letter, byLetter, err)
		}
		if byNumber, err := ParseColumnKey(strconv.Itoa(index)); err != nil || byNumber != index {
			t.Fatalf("ParseColumnKey(%q) = %d, %v", strconv.Itoa(index), byNumber, err)
		}
	}
	for index, want := range map[int]string{0: "", 1: "A", 26: "Z", 27: "AA", 702: "ZZ", 703: "AAA", 16384: "XFD"} {
		if got := ColumnLetter(index); got != want {
			t.Errorf("ColumnLetter(%d) = %q, want %q", index, got, want)
		}
	}
}

const (
	sheetsTreeRevision = "bbbbbbbb-0000-4000-8000-000000000001"
	namedSeriesID      = "cccccccc-0000-4000-8000-000000000001"
	unnamedSeriesID    = "cccccccc-0000-4000-8000-000000000002"
)

// sheetsTreeBody is a revision tree with a named series, an unlisted series
// and no series, sort keys out of position order, and a row filter.
const sheetsTreeBody = `{"id":"` + sheetsTreeRevision + `","deliverable_id":"` + monthlyID + `","release":"v2","state":"draft","description":null,` +
	`"enabled_at":null,"enabled_by_id":null,"skeleton_id":7,"cell_bindings":[],"sheet_bindings":[` +
	`{"id":"dddddddd-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":42,"member_series_id":"` + namedSeriesID + `",` +
	`"data_start_row":3,"header_rows":[1,2],"instruction_row_count":1,` +
	`"row_filter":{"description":"Open claims","conditions":[{"column_name":"status","operator":"eq","value":"open"}]},` +
	`"sort_keys":[{"position":1,"column_name":"amount","descending":true},{"position":0,"column_name":"policy_id","descending":false}],` +
	`"column_bindings":[` +
	`{"id":"eeeeeeee-0000-4000-8000-000000000001","target_column_index":28,"source_type":"column","source_column":"amount","run_param":null,` +
	`"format_override":"#,##0.00","origin":"user","needs_confirmation":false,"target_label":"Amount"},` +
	`{"id":"eeeeeeee-0000-4000-8000-000000000002","target_column_index":1,"source_type":"run_param","source_column":null,"run_param":"period",` +
	`"format_override":null,"origin":"suggested","needs_confirmation":true,"target_label":null}]},` +
	`{"id":"dddddddd-0000-4000-8000-000000000002","sheet_name":"Orphan","member_dataset_id":9,"member_series_id":"` + unnamedSeriesID + `",` +
	`"data_start_row":1,"header_rows":[],"instruction_row_count":0,"sort_keys":[],"column_bindings":[]},` +
	`{"id":"dddddddd-0000-4000-8000-000000000003","sheet_name":"Plain","member_dataset_id":5,"member_series_id":null,` +
	`"data_start_row":2,"header_rows":[1],"instruction_row_count":0}]}`

func sheetsTree(t *testing.T) *api.DeliverableRevisionResponse {
	t.Helper()
	var tree api.DeliverableRevisionResponse
	if err := json.Unmarshal([]byte(sheetsTreeBody), &tree); err != nil {
		t.Fatal(err)
	}
	return &tree
}

func TestSheetsFromTree(t *testing.T) {
	got := SheetsFromTree(sheetsTree(t), map[uuid.UUID]string{uuid.MustParse(namedSeriesID): "Monthly claims"})
	named, unnamed, one, zero, format := "Monthly claims", unnamedSeriesID, 1, 0, "#,##0.00"
	description, open := "Open claims", "open"
	want := map[string]ManifestSheet{
		"Claims": {
			Table: "42", Series: &named, HeaderRows: []int{1, 2}, DataStartRow: 3, InstructionRowCount: &one,
			RowOrder:  []ManifestSortKey{{Column: "policy_id"}, {Column: "amount", Descending: true}},
			RowFilter: &row_filters.Filter{Description: &description, Conditions: []row_filters.Condition{{ColumnName: "status", Operator: api.Eq, Value: &open}}},
			Columns:   map[string]ManifestColumn{"AB": {Column: "amount", Format: &format}, "A": {RunParam: "period"}},
		},
		"Orphan": {Table: "9", Series: &unnamed, HeaderRows: []int{}, DataStartRow: 1, InstructionRowCount: &zero, Columns: map[string]ManifestColumn{}},
		"Plain":  {Table: "5", HeaderRows: []int{1}, DataStartRow: 2, InstructionRowCount: &zero, Columns: map[string]ManifestColumn{}},
	}
	if len(got) != len(want) {
		t.Fatalf("sheets = %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	for name, wantSheet := range want {
		if sheet := exportedSheet(got[name]); !reflect.DeepEqual(sheet, wantSheet) {
			t.Errorf("%s = %#v, want %#v", name, sheet, wantSheet)
		}
	}
	claims := got["Claims"]
	for _, key := range []string{"table", "series", "header_rows", "data_start_row", "instruction_row_count", "row_order", "row_filter", "columns"} {
		if !claims.Has(key) {
			t.Errorf("Claims.Has(%q) = false", key)
		}
	}
	if want := map[int]ManifestColumn{28: {Column: "amount", Format: &format}, 1: {RunParam: "period"}}; !reflect.DeepEqual(claims.ColumnsByIndex(), want) {
		t.Errorf("Claims.ColumnsByIndex = %#v, want %#v", claims.ColumnsByIndex(), want)
	}
	plain := got["Plain"]
	if plain.Has("series") || plain.Has("row_order") || plain.Has("row_filter") || !plain.Has("columns") || plain.ColumnsByIndex() == nil {
		t.Errorf("Plain marks: series %v, row_order %v, row_filter %v, columns %v, indexes %#v",
			plain.Has("series"), plain.Has("row_order"), plain.Has("row_filter"), plain.Has("columns"), plain.ColumnsByIndex())
	}
	if empty := SheetsFromTree(&api.DeliverableRevisionResponse{}, nil); empty == nil || len(empty) != 0 {
		t.Errorf("SheetsFromTree of a tree without sheets = %#v, want an empty map", empty)
	}
}

func TestSetSheets(t *testing.T) {
	item := Deliverable{ID: uuid.New(), Slug: "a", Name: "A", FilenamePattern: "a.xlsx"}
	m := FromAPI(&item, &Revision{ID: uuid.New(), Release: "v1", State: api.DeliverableRevisionStateDraft})
	m.SetSheets(map[string]ManifestSheet{})
	if m.Has("sheets") || m.Spec.Sheets != nil {
		t.Fatalf("an empty map: Has(sheets) = %v, sheets = %#v", m.Has("sheets"), m.Spec.Sheets)
	}
	m.SetSheets(map[string]ManifestSheet{"Data": {Table: "7", HeaderRows: []int{}, DataStartRow: 1, Columns: map[string]ManifestColumn{"c": {Column: "amount"}}}})
	sheet := m.Spec.Sheets["Data"]
	if !m.Has("sheets") || !sheet.Has("table") || !sheet.Has("columns") || sheet.Has("series") || sheet.Has("instruction_row_count") {
		t.Fatalf("marks: sheets %v, table %v, columns %v, series %v, instruction_row_count %v", m.Has("sheets"), sheet.Has("table"), sheet.Has("columns"), sheet.Has("series"), sheet.Has("instruction_row_count"))
	}
	if want := map[int]ManifestColumn{3: {Column: "amount"}}; !reflect.DeepEqual(sheet.ColumnsByIndex(), want) {
		t.Fatalf("ColumnsByIndex = %#v, want %#v", sheet.ColumnsByIndex(), want)
	}
}
