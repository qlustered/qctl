package deliverables

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/row_filters"
)

const openClaimsFilter = "{description: Open claims, conditions: [{column_name: status, operator: eq, value: open}]}"

// totalDoc is a manifest whose spec.cells has one total, Summary!B1, with
// the extra total keys and sheets.
func totalDoc(keys string, sheets ...string) string {
	doc := deliverableManifestHeader + deliverableManifestBasics + "  release: v3\n  cells: {Summary!B1: {total: {" + keys + "}}}\n"
	if len(sheets) > 0 {
		doc += "  sheets:\n" + strings.Join(sheets, "")
	}
	return doc
}

func openClaims() *row_filters.Filter {
	description, open := "Open claims", "open"
	return &row_filters.Filter{Description: &description, Conditions: []row_filters.Condition{{ColumnName: "status", Operator: api.Eq, Value: &open}}}
}

func TestRowFilterTellsAbsentFromNullFromSet(t *testing.T) {
	type place struct {
		doc  func(line string) string
		read func(*Manifest) (bool, *row_filters.Filter)
	}
	places := map[string]place{
		"sheet": {
			doc: func(line string) string {
				if line == "" {
					return validSheet()
				}
				return validSheet(line)
			},
			read: func(m *Manifest) (bool, *row_filters.Filter) {
				sheet := m.Spec.Sheets["Data"]
				return sheet.Has(keyRowFilter), sheet.RowFilter
			},
		},
		"total": {
			doc: func(line string) string {
				if line == "" {
					return totalDoc("table: 5, func: count")
				}
				return totalDoc("table: 5, func: count, " + line)
			},
			read: func(m *Manifest) (bool, *row_filters.Filter) {
				total := m.Spec.Cells["Summary!B1"].Total
				return total.Has(keyRowFilter), total.RowFilter
			},
		},
	}
	for name, at := range places {
		for _, tc := range []struct {
			name, line string
			has        bool
			want       *row_filters.Filter
		}{
			{"absent", "", false, nil},
			{"null", "row_filter: null", true, nil},
			{"set", "row_filter: " + openClaimsFilter, true, openClaims()},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				m, err := ParseManifest([]byte(at.doc(tc.line)))
				if err != nil {
					t.Fatalf("ParseManifest: %v (exit %d)", err, qerrors.GetExitCode(err))
				}
				has, filter := at.read(m)
				if has != tc.has || !reflect.DeepEqual(filter, tc.want) {
					t.Fatalf("Has(row_filter) = %v, row_filter = %#v; want %v, %#v", has, filter, tc.has, tc.want)
				}
			})
		}
	}
	m, err := ParseManifest([]byte(totalDoc("table: 5, func: sum, column: amount")))
	if err != nil {
		t.Fatal(err)
	}
	total := m.Spec.Cells["Summary!B1"].Total
	for key, want := range map[string]bool{"table": true, "func": true, "column": true, "series": false, "same_rows_as": false, "row_filter": false} {
		if got := total.Has(key); got != want {
			t.Errorf("total.Has(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestRowFilterRefusals(t *testing.T) {
	longDescription := strings.Repeat("é", 201)
	for _, tc := range []struct{ name, filter string }{
		{"missing description", "{conditions: [{column_name: status, operator: eq, value: open}]}"},
		{"null description", "{description: null, conditions: [{column_name: status, operator: eq, value: open}]}"},
		{"blank description", "{description: '  ', conditions: [{column_name: status, operator: eq, value: open}]}"},
		{"description over 200 characters", "{description: " + longDescription + ", conditions: [{column_name: status, operator: eq, value: open}]}"},
		{"no conditions or predicate", "{description: Open claims}"},
		{"empty conditions", "{description: Open claims, conditions: []}"},
		{"empty mapping", "{}"},
		{"unknown key", "{description: Open claims, conditions: [{column_name: status, operator: eq, value: open}], colour: blue}"},
		{"a list", "[]"},
	} {
		for place, doc := range map[string]string{
			"sheet": validSheet("row_filter: " + tc.filter),
			"total": totalDoc("table: 5, func: count, row_filter: " + tc.filter),
		} {
			t.Run(place+"/"+tc.name, func(t *testing.T) {
				if m, err := ParseManifest([]byte(doc)); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
					t.Fatalf("exit = %d (err %v, manifest %#v), want 2", qerrors.GetExitCode(err), err, m)
				}
			})
		}
	}
	data := bpSheetDoc("Data", "table: 5", "header_rows: [1]", "data_start_row: 2")
	if _, err := ParseManifest([]byte(totalDoc("same_rows_as: Data, func: count, row_filter: "+openClaimsFilter, data))); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("same_rows_as with row_filter: exit = %d (err %v), want 2", qerrors.GetExitCode(err), err)
	}
	m, err := ParseManifest([]byte(totalDoc("same_rows_as: Data, func: count, row_filter: null", data)))
	if err != nil {
		t.Fatalf("same_rows_as with row_filter: null: %v", err)
	}
	if total := m.Spec.Cells["Summary!B1"].Total; !total.Has(keyRowFilter) || total.RowFilter != nil {
		t.Fatalf("Has(row_filter) = %v, row_filter = %#v", total.Has(keyRowFilter), total.RowFilter)
	}
}

func TestRowFilterErrorsNameThePathOnce(t *testing.T) {
	for doc, prefix := range map[string]string{
		validSheet("row_filter: {conditions: [{column_name: status, operator: eq, value: open}]}"):                      "spec.sheets.Data.row_filter needs a description",
		totalDoc("table: 5, func: count, row_filter: {conditions: [{column_name: status, operator: eq, value: open}]}"): "spec.cells.Summary!B1: total.row_filter needs a description",
	} {
		_, err := ParseManifest([]byte(doc))
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.HasPrefix(err.Error(), prefix) || strings.Count(err.Error(), "row_filter") != 1 {
			t.Errorf("error = %v (exit %d), want exit 2 starting %q", err, qerrors.GetExitCode(err), prefix)
		}
	}
}

func TestParseManifestReadsSheetRowFiltersFromJSONAsFromYAML(t *testing.T) {
	const jsonDoc = `{"apiVersion":"qluster.ai/v1","kind":"Deliverable","metadata":{"slug":"a"},"spec":{"name":"A","filename_pattern":"a.xlsx","release":"v1","sheets":{` +
		`"Data":{"table":7,"header_rows":[1],"data_start_row":2,"row_filter":{"description":"Open claims","conditions":[{"column_name":"status","operator":"eq","value":"open"}]}},` +
		`"Cleared":{"table":7,"header_rows":[1],"data_start_row":2,"row_filter":null},` +
		`"Absent":{"table":7,"header_rows":[1],"data_start_row":2}}}}`
	yamlDoc := "apiVersion: qluster.ai/v1\nkind: Deliverable\nmetadata:\n  slug: a\nspec:\n  name: A\n  filename_pattern: a.xlsx\n  release: v1\n  sheets:\n" +
		indentLines(4, "Data:", "  table: 7", "  header_rows: [1]", "  data_start_row: 2", "  row_filter: "+openClaimsFilter,
			"Cleared:", "  table: 7", "  header_rows: [1]", "  data_start_row: 2", "  row_filter: null",
			"Absent:", "  table: 7", "  header_rows: [1]", "  data_start_row: 2")
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
	sheets := fromJSON.Spec.Sheets
	if !sheets["Data"].Has(keyRowFilter) || !reflect.DeepEqual(sheets["Data"].RowFilter, openClaims()) ||
		!sheets["Cleared"].Has(keyRowFilter) || sheets["Cleared"].RowFilter != nil || sheets["Absent"].Has(keyRowFilter) {
		t.Fatalf("JSON row filters: %#v", sheets)
	}
	unknown := strings.Replace(jsonDoc, `"value":"open"}]}`, `"value":"open"}],"colour":"blue"}`, 1)
	if _, err := ParseManifest([]byte(unknown)); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("JSON row_filter with an unknown key: exit = %d (err %v), want 2", qerrors.GetExitCode(err), err)
	}
}

// rowFilterTree has a filtered and an unfiltered sheet, a filtered and an
// unfiltered total, and a mirror total carrying a filter.
const rowFilterTree = `{"id":"` + sheetsTreeRevision + `","deliverable_id":"` + monthlyID + `","release":"v2","state":"draft","description":"September",` +
	`"enabled_at":null,"enabled_by_id":null,"skeleton_id":7,"sheet_bindings":[` +
	`{"id":"dddddddd-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":42,"member_series_id":null,` +
	`"data_start_row":2,"header_rows":[1],"instruction_row_count":0,"sort_keys":[],"column_bindings":[],` +
	`"row_filter":{"description":"Large open claims","conditions":[{"column_name":"status","operator":"in","value_list":["open","reopened"],"case_sensitive":true}],` +
	`"predicate":{"source":"def applies(row):\n    return row[\"amount\"] > decimal(\"100\")\n","columns":["amount"]}}},` +
	`{"id":"dddddddd-0000-4000-8000-000000000002","sheet_name":"Plain","member_dataset_id":5,"member_series_id":null,` +
	`"data_start_row":2,"header_rows":[1],"instruction_row_count":0,"sort_keys":[],"column_bindings":[],"row_filter":null}],` +
	`"cell_bindings":[` +
	`{"id":"5d000000-0000-4000-8000-000000000001","sheet_name":"Summary","cell_ref":"B1","source_type":"aggregate","aggregate_func":"sum",` +
	`"aggregate_column":"amount","aggregate_member_dataset_id":42,"aggregate_member_series_id":null,"same_rows_as_sheet_binding_id":null,` +
	`"row_filter":{"description":"Positive amounts","conditions":[{"column_name":"amount","operator":"gt","value":"0"}]},"needs_confirmation":false},` +
	`{"id":"5d000000-0000-4000-8000-000000000002","sheet_name":"Summary","cell_ref":"B2","source_type":"aggregate","aggregate_func":"count",` +
	`"aggregate_member_dataset_id":42,"same_rows_as_sheet_binding_id":null,"row_filter":null,"needs_confirmation":false},` +
	// The server refuses a filter on a mirror; export must still re-parse.
	`{"id":"5d000000-0000-4000-8000-000000000003","sheet_name":"Summary","cell_ref":"B3","source_type":"aggregate","aggregate_func":"count",` +
	`"same_rows_as_sheet_binding_id":"dddddddd-0000-4000-8000-000000000001",` +
	`"row_filter":{"description":"Positive amounts","conditions":[{"column_name":"amount","operator":"gt","value":"0"}]},"needs_confirmation":false}]}`

func TestExportWritesRowFiltersThatReparse(t *testing.T) {
	mock := serveExport(t, rowFilterTree)
	defer mock.Close()
	item, rev := exportFixture()
	exported, err := NewClient(mock.URL(), testOrgID, 0).ExportManifest("token", item, rev)
	if err != nil {
		t.Fatal(err)
	}
	claims, plain := exported.Spec.Sheets["Claims"], exported.Spec.Sheets["Plain"]
	description, source := "Large open claims", "def applies(row):\n    return row[\"amount\"] > decimal(\"100\")\n"
	wantSheet := &row_filters.Filter{
		Description: &description,
		Conditions:  []row_filters.Condition{{ColumnName: "status", Operator: api.In, ValueList: &[]string{"open", "reopened"}, CaseSensitive: true}},
		Predicate:   &row_filters.Predicate{Source: source, Columns: []string{"amount"}},
	}
	if !claims.Has(keyRowFilter) || !reflect.DeepEqual(claims.RowFilter, wantSheet) || plain.Has(keyRowFilter) || plain.RowFilter != nil {
		t.Fatalf("Claims row_filter = %#v (Has %v), Plain row_filter = %#v (Has %v)", claims.RowFilter, claims.Has(keyRowFilter), plain.RowFilter, plain.Has(keyRowFilter))
	}
	positive, zero := "Positive amounts", "0"
	wantTotal := &row_filters.Filter{Description: &positive, Conditions: []row_filters.Condition{{ColumnName: "amount", Operator: api.Gt, Value: &zero}}}
	for ref, want := range map[string]*row_filters.Filter{"Summary!B1": wantTotal, "Summary!B2": nil, "Summary!B3": nil} {
		total := exported.Spec.Cells[ref].Total
		if total.Has(keyRowFilter) != (want != nil) || !reflect.DeepEqual(total.RowFilter, want) {
			t.Errorf("%s row_filter = %#v (Has %v), want %#v", ref, total.RowFilter, total.Has(keyRowFilter), want)
		}
	}
	var yamlDoc bytes.Buffer
	if err := output.WriteRaw(&yamlDoc, "yaml", exported); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(yamlDoc.String(), "row_filter:"); got != 2 {
		t.Fatalf("export writes row_filter %d times, want 2:\n%s", got, yamlDoc.String())
	}
	assertExportRoundTrips(t, exported)
}

func TestSetCellsMarksATotalWithoutMarks(t *testing.T) {
	item, rev := exportFixture()
	m := FromAPI(item, rev)
	total := &ManifestTotal{Table: "5", Func: api.DeliverableAggregateFuncCount, RowFilter: openClaims()}
	m.SetCells(map[string]ManifestCell{"Summary!B1": {Total: total}})
	got := m.Spec.Cells["Summary!B1"].Total
	for key, want := range map[string]bool{"table": true, "func": true, "row_filter": true, "series": false, "same_rows_as": false, "column": false} {
		if got.Has(key) != want {
			t.Errorf("Has(%q) = %v, want %v", key, got.Has(key), want)
		}
	}
	if total.present != nil {
		t.Fatal("SetCells marked the caller's total")
	}
}
