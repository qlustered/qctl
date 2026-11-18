package deliverables

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

func sheetJSONDoc(sheet string) string {
	return `{"apiVersion":"qluster.ai/v1","kind":"Deliverable","metadata":{"slug":"a"},` +
		`"spec":{"name":"A","filename_pattern":"a.xlsx","release":"v1","sheets":{"Data":` + sheet + `}}}`
}

func TestSheetValidationUsesManifestPathsAndTypes(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, json, message string
	}{
		{"sheet list", "[]", `[]`, "spec.sheets.Data: expected a mapping"},
		{"table list", "{table: []}", `{"table":[]}`, "spec.sheets.Data.table: expected a table name or integer ID"},
		{"series mapping", "{series: {name: s}}", `{"series":{"name":"s"}}`, "spec.sheets.Data.series: expected a string"},
		{"headers mapping", "{header_rows: {first: 1}}", `{"header_rows":{"first":1}}`, "spec.sheets.Data.header_rows: expected a list"},
		{"header string", "{header_rows: [one]}", `{"header_rows":["one"]}`, "spec.sheets.Data.header_rows[0]: expected an integer"},
		{"data row string", "{data_start_row: two}", `{"data_start_row":"two"}`, "spec.sheets.Data.data_start_row: expected an integer"},
		{"instruction string", "{instruction_row_count: one}", `{"instruction_row_count":"one"}`, "spec.sheets.Data.instruction_row_count: expected an integer"},
		{"order mapping", "{row_order: {column: amount}}", `{"row_order":{"column":"amount"}}`, "spec.sheets.Data.row_order: expected a list"},
		{"order item list", "{row_order: [[]]}", `{"row_order":[[]]}`, "spec.sheets.Data.row_order[0]: expected a mapping"},
		{"descending string", "{row_order: [{descending: maybe}]}", `{"row_order":[{"descending":"maybe"}]}`, "spec.sheets.Data.row_order[0].descending: expected a boolean"},
		{"order unknown", "{row_order: [{position: 0}]}", `{"row_order":[{"position":0}]}`, "spec.sheets.Data.row_order[0].position: unknown field"},
		{"columns list", "{columns: []}", `{"columns":[]}`, "spec.sheets.Data.columns: expected a mapping"},
		{"column scalar", "{columns: {A: amount}}", `{"columns":{"A":"amount"}}`, "spec.sheets.Data.columns.A: expected a mapping"},
		{"column source list", "{columns: {A: {column: []}}}", `{"columns":{"A":{"column":[]}}}`, "spec.sheets.Data.columns.A.column: expected a string"},
		{"column parameter map", "{columns: {A: {run_param: {name: period}}}}", `{"columns":{"A":{"run_param":{"name":"period"}}}}`, "spec.sheets.Data.columns.A.run_param: expected a string"},
		{"column format list", "{columns: {A: {format: []}}}", `{"columns":{"A":{"format":[]}}}`, "spec.sheets.Data.columns.A.format: expected a string"},
		{"sheet unknown", "{totals: []}", `{"totals":[]}`, "spec.sheets.Data.totals: unknown field"},
		{"column unknown", "{columns: {A: {colour: blue}}}", `{"columns":{"A":{"colour":"blue"}}}`, "spec.sheets.Data.columns.A.colour: unknown field"},
		{"case mismatch", "{Table: 7}", `{"Table":7}`, "spec.sheets.Data.Table: unknown field"},
	} {
		for format, doc := range map[string]string{
			"yaml": sheetsManifestHead + "    Data: " + tc.yaml + "\n",
			"json": sheetJSONDoc(tc.json),
		} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				_, err := ParseManifest([]byte(doc))
				if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("error = %v (exit %d), want exit 2 and %q", err, qerrors.GetExitCode(err), tc.message)
				}
				if strings.Contains(err.Error(), "deliverables.") || strings.Contains(err.Error(), "cannot unmarshal") {
					t.Errorf("error leaks decoder types: %v", err)
				}
			})
		}
	}
}

func TestSheetRowsStayWithinExcel(t *testing.T) {
	for _, tc := range []struct {
		rows, message string
	}{
		{`"header_rows":[1048575],"data_start_row":1048576`, ""},
		{`"header_rows":[],"data_start_row":1048576`, ""},
		{`"header_rows":[],"data_start_row":1048577`, "spec.sheets.Data.data_start_row must be at most 1048576"},
		{`"header_rows":[],"data_start_row":2147483648`, "spec.sheets.Data.data_start_row must be at most 1048576"},
		{`"header_rows":[1048577],"data_start_row":1048576`, "spec.sheets.Data.header_rows[0] must be at most 1048576"},
		{`"header_rows":[1,2147483648],"data_start_row":1048576`, "spec.sheets.Data.header_rows[1] must be at most 1048576"},
	} {
		jsonDoc := sheetJSONDoc(`{"table":7,` + tc.rows + `}`)
		var value any
		if err := json.Unmarshal([]byte(jsonDoc), &value); err != nil {
			t.Fatal(err)
		}
		yamlDoc, err := yaml.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for format, doc := range map[string][]byte{"json": []byte(jsonDoc), "yaml": yamlDoc} {
			t.Run(tc.rows+"/"+format, func(t *testing.T) {
				_, err := ParseManifest(doc)
				if tc.message == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("error = %v (exit %d), want exit 2 and %q", err, qerrors.GetExitCode(err), tc.message)
				}
			})
		}
	}
}

func TestSheetColumnKeyErrorsExplainTheKey(t *testing.T) {
	for _, tc := range []struct{ key, message string }{
		{`""`, `spec.sheets.Data.columns[""]: "" is not a column`},
		{`"03"`, `spec.sheets.Data.columns.03: "03" is not a column: numeric indexes cannot have leading zeros`},
		{`"00000000000000000000000000001"`, "numeric indexes cannot have leading zeros"},
	} {
		for format, doc := range map[string]string{
			"yaml": validSheet("columns:", "  "+tc.key+": {column: amount}"),
			"json": sheetJSONDoc(`{"table":7,"header_rows":[],"data_start_row":1,"columns":{` + tc.key + `:{"column":"amount"}}}`),
		} {
			t.Run(tc.key+"/"+format, func(t *testing.T) {
				_, err := ParseManifest([]byte(doc))
				if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("error = %v, want exit 2 and %q", err, tc.message)
				}
			})
		}
	}
}

func TestSheetJSONTableIDsAndStrictFields(t *testing.T) {
	for _, table := range []string{`7`, `"7"`, `"claims"`, `9007199254740993`} {
		m, err := ParseManifest([]byte(sheetJSONDoc(`{"table":` + table + `,"header_rows":[],"data_start_row":1}`)))
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Spec.Sheets["Data"].Table; got != strings.Trim(table, `"`) {
			t.Errorf("table %s decoded as %q", table, got)
		}
	}
	for _, fields := range []string{
		`"table":0`, `"table":-7`, `"table":7.5`, `"table":7.0`, `"table":true`, `"table":9223372036854775808`,
		`"table":7,"table":8`, `"table":7,"columns":{"A":{"column":"a","column":"b"}}`,
	} {
		_, err := ParseManifest([]byte(sheetJSONDoc(`{` + fields + `,"header_rows":[],"data_start_row":1}`)))
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Fatalf("fields %s: error = %v, want exit 2", fields, err)
		}
	}
}

func TestSheetShapesPreserveYAMLAliasesAndMerges(t *testing.T) {
	doc := sheetsManifestHead + indentLines(4,
		"Data: &sheet", "  table: 7", "  header_rows: &headers [1]", "  data_start_row: 2",
		"Copy:", "  <<: *sheet", "  header_rows: *headers")
	m, err := ParseManifest([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Spec.Sheets["Data"], m.Spec.Sheets["Copy"]) {
		t.Fatalf("alias/merge changed sheets: %#v", m.Spec.Sheets)
	}
	bad := strings.Replace(doc, "  data_start_row: 2", "  data_start_row: wrong", 1)
	_, err = ParseManifest([]byte(bad))
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "spec.sheets.Copy.data_start_row: expected an integer") {
		t.Fatalf("merged invalid value: %v", err)
	}
}
