package row_filters //nolint:revive // Matches the resource domain.

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

const (
	noFilterText = "Row filter:\n  (no filter: keeps every row)\n"
	blockedTail  = " the filter cannot decide, such as a quarantined value that does not parse or a predicate that raises an error.\n"
)

func TestWriteReport(t *testing.T) {
	euFilter := &api.RowFilter{
		Description: strPtr("EU orders"),
		Conditions:  &[]api.RowFilterCondition{{ColumnName: "region", Operator: api.In, ValueList: &[]string{"DE", "FR"}}},
	}
	for _, tt := range []struct {
		name   string
		report Report
		want   string
	}{
		{"nil filter", Report{Kept: 3},
			noFilterText + "3 kept / 0 left out / 0 blocked\n"},
		{"filter with a condition", Report{RowFilter: euFilter, Kept: 1280, LeftOut: 7, Blocked: 2, LeftOutSample: []api.RowFilterLeftOutRow{
			{RowID: 3, Values: map[string]any{"region": "US", "amount": json.Number("12.50")}},
			{RowID: 18, Quarantined: true, Values: map[string]any{"region": "UK", "amount": nil}},
		}},
			"Row filter:\n" +
				"  description: EU orders\n" +
				"  conditions (a row must meet every one):\n" +
				"    - region in [\"DE\", \"FR\"]\n" +
				"1280 kept / 7 left out / 2 blocked\n" +
				"Blocked: 2 rows" + blockedTail +
				"Left out, lowest row ids first (2 of 7):\n" +
				"  row 3: amount=12.50, region=\"US\"\n" +
				"  row 18 (quarantined): amount=null, region=\"UK\"\n"},
		{"empty table", Report{},
			noFilterText + "No live rows: the table is empty.\n"},
		{"empty table with a filter", Report{RowFilter: euFilter},
			"Row filter:\n  description: EU orders\n  conditions (a row must meet every one):\n    - region in [\"DE\", \"FR\"]\nNo live rows: the table is empty.\n"},
		{"only blocked rows", Report{Blocked: 1},
			noFilterText + "0 kept / 0 left out / 1 blocked\nBlocked: 1 row" + blockedTail},
		{"blocked rows", Report{Kept: 10, Blocked: 4},
			noFilterText + "10 kept / 0 left out / 4 blocked\nBlocked: 4 rows" + blockedTail},
		{"no thousands separator", Report{Kept: 1234567, LeftOut: 9876543},
			noFilterText + "1234567 kept / 9876543 left out / 0 blocked\n"},
		{"left out without a sample", Report{Kept: 1, LeftOut: 2},
			noFilterText + "1 kept / 2 left out / 0 blocked\n"},
		{"sample is every left-out row", Report{Kept: 1, LeftOut: 2, LeftOutSample: []api.RowFilterLeftOutRow{
			{RowID: 4, Values: map[string]any{"a": true}},
			{RowID: 9, Values: map[string]any{"a": false}},
		}},
			noFilterText + "1 kept / 2 left out / 0 blocked\nLeft out, lowest row ids first:\n  row 4: a=true\n  row 9: a=false\n"},
		{"rows in the order given", Report{LeftOut: 5, LeftOutSample: []api.RowFilterLeftOutRow{
			{RowID: 9}, {RowID: 4, Quarantined: true},
		}},
			noFilterText + "0 kept / 5 left out / 0 blocked\nLeft out, lowest row ids first (2 of 5):\n  row 9\n  row 4 (quarantined)\n"},
		{"value kinds", Report{LeftOut: 1, LeftOutSample: []api.RowFilterLeftOutRow{{RowID: 7, Values: map[string]any{
			"a_null":   nil,
			"b_quote":  "say \"hi\"\tthen\nleave \\ now",
			"c_number": json.Number("12.50"),
			"d_int":    json.Number("-3"),
			"e_true":   true,
			"f_false":  false,
			"g_empty":  "",
			"h_float":  2.5,
		}}}},
			noFilterText + "0 kept / 1 left out / 0 blocked\nLeft out, lowest row ids first:\n" +
				`  row 7: a_null=null, b_quote="say \"hi\"\tthen\nleave \\ now", c_number=12.50, d_int=-3, e_true=true, f_false=false, g_empty="", h_float=2.5` + "\n"},
		{"sanitized", Report{LeftOut: 1, LeftOutSample: []api.RowFilterLeftOutRow{{RowID: 2, Values: map[string]any{
			"col\nname\x1b]0;title\x07": "\x1b[31mred\x1b[0m \u202eevil\r",
			"other":                     []any{"\x1b[2J", "x\ny"},
		}}}},
			noFilterText + "0 kept / 1 left out / 0 blocked\nLeft out, lowest row ids first:\n" +
				`  row 2: col\nname\x1b]0;title\x07="\x1b[31mred\x1b[0m \u202eevil\r", other=[\x1b[2J x\ny]` + "\n"},
		{"masked fields", Report{Kept: 4, LeftOut: 2, MaskedFields: &[]string{"email", "ssn\x1b[31m"}, LeftOutSample: []api.RowFilterLeftOutRow{
			{RowID: 3, Values: map[string]any{"email": "•••••.com", "region": "US", "ssn\x1b[31m": "•••••"}},
			{RowID: 8, Values: map[string]any{"email": nil, "region": "UK", "ssn\x1b[31m": "•••••"}},
		}},
			noFilterText + "4 kept / 2 left out / 0 blocked\nLeft out, lowest row ids first:\n" +
				`  row 3: email="•••••.com", region="US", ssn\x1b[31m="•••••"` + "\n" +
				`  row 8: email=null, region="UK", ssn\x1b[31m="•••••"` + "\n" +
				`Masked fields: email, ssn\x1b[31m` + "\n"},
		{"empty masked fields", Report{Kept: 1, MaskedFields: &[]string{}},
			noFilterText + "1 kept / 0 left out / 0 blocked\n"},
		{"columns sorted by name", Report{LeftOut: 1, LeftOutSample: []api.RowFilterLeftOutRow{{RowID: 1, Values: map[string]any{
			"zeta": "z", "alpha": "a", "Mid": "m", "alpha2": "b",
		}}}},
			noFilterText + "0 kept / 1 left out / 0 blocked\nLeft out, lowest row ids first:\n" +
				`  row 1: Mid="m", alpha="a", alpha2="b", zeta="z"` + "\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteReport(&buf, &tt.report); err != nil {
				t.Fatal(err)
			}
			if buf.String() != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", buf.String(), tt.want)
			}
			if strings.ContainsAny(buf.String(), "\x1b\x07\r\u202e") {
				t.Errorf("output keeps a terminal control character: %q", buf.String())
			}
		})
	}
}

var errReportWrite = errors.New("write failed")

type failingWriter struct{ writes int }

func (f *failingWriter) Write([]byte) (int, error) {
	f.writes++
	return 0, errReportWrite
}

func TestWriteReportReturnsTheWriteError(t *testing.T) {
	w := &failingWriter{}
	report := Report{Kept: 1, LeftOut: 1, Blocked: 1, LeftOutSample: []api.RowFilterLeftOutRow{{RowID: 1}}}
	if err := WriteReport(w, &report); !errors.Is(err, errReportWrite) {
		t.Errorf("WriteReport error = %v, want %v", err, errReportWrite)
	}
	if w.writes != 1 {
		t.Errorf("WriteReport wrote %d times, want 1", w.writes)
	}
}

// jsonFieldNames returns the sorted names encoding/json gives t's exported fields.
func jsonFieldNames(t reflect.Type) []string {
	var names []string
	for i := range t.NumField() {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if !field.IsExported() || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = field.Name
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestReportMirrorsTheWireSchema(t *testing.T) {
	// Harness-alive control: a renamed, untagged and skipped field each change
	// the set, so a field added to or dropped from either side cannot pass.
	control := reflect.TypeFor[struct {
		Renamed  int `json:"renamed,omitempty"`
		Untagged int
		Skipped  int `json:"-"`
	}]()
	if got, want := jsonFieldNames(control), []string{"Untagged", "renamed"}; !slices.Equal(got, want) {
		t.Fatalf("control: jsonFieldNames = %v, want %v", got, want)
	}

	got := jsonFieldNames(reflect.TypeFor[Report]())
	want := jsonFieldNames(reflect.TypeFor[api.SheetRowFilterReport]())
	if !slices.Equal(got, want) {
		t.Errorf("Report has JSON fields %v; api.SheetRowFilterReport has %v", got, want)
	}
}
