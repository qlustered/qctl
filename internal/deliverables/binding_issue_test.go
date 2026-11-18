package deliverables

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestBindingIssuesUseManifestPaths(t *testing.T) {
	m := bpManifest(t, "", bpData("columns: {'3': {column: amount}, d: {run_param: period}}"))
	p := &Plan{sheets: []plannedSheet{{name: "Data", spec: m.Spec.Sheets["Data"]}}}
	for _, tc := range []struct {
		name, loc, want string
		column          int
	}{
		{name: "sheet name", loc: "body.sheet_name", want: "spec.sheets.Data"},
		{name: "table", loc: "body.member_dataset_id", want: "spec.sheets.Data.table"},
		{name: "series", loc: "body.member_series_id", want: "spec.sheets.Data.series"},
		{name: "data start", loc: "body.data_start_row", want: "spec.sheets.Data.data_start_row"},
		{name: "instructions", loc: "body.instruction_row_count", want: "spec.sheets.Data.instruction_row_count"},
		{name: "headers", loc: "body.header_rows", want: "spec.sheets.Data.header_rows"},
		{name: "header index", loc: "body.header_rows.1", want: "spec.sheets.Data.header_rows[1]"},
		{name: "row order", loc: "body.sort_keys", want: "spec.sheets.Data.row_order"},
		{name: "sort entry", loc: "body.sort_keys.2", want: "spec.sheets.Data.row_order[2]"},
		{name: "sort column", loc: "body.sort_keys.2.column_name", want: "spec.sheets.Data.row_order[2].column"},
		{name: "sort direction", loc: "body.sort_keys.2.descending", want: "spec.sheets.Data.row_order[2].descending"},
		{name: "sort position", loc: "body.sort_keys.2.position", want: "spec.sheets.Data.row_order[2]"},
		{name: "column target with numeric key", column: 3, loc: "body.target_column_index", want: "spec.sheets.Data.columns.3"},
		{name: "column source", column: 3, loc: "body.source_column", want: "spec.sheets.Data.columns.3.column"},
		{name: "source type", column: 3, loc: "body.source_type", want: "spec.sheets.Data.columns.3"},
		{name: "run parameter with lowercase key", column: 4, loc: "body.run_param", want: "spec.sheets.Data.columns.d.run_param"},
		{name: "literal value", column: 3, loc: "body.literal_value", want: "spec.sheets.Data.columns.3.literal"},
		{name: "format", column: 4, loc: "body.format_override", want: "spec.sheets.Data.columns.d.format"},
		{name: "unknown field", loc: "body.future_key", want: "body.future_key"},
		{name: "unknown nested field", loc: "body.sort_keys.2.future_key", want: "body.sort_keys.2.future_key"},
		{name: "non-numeric index", loc: "body.sort_keys.amount.column_name", want: "body.sort_keys.amount.column_name"},
		{name: "negative index", loc: "body.sort_keys.-1.column_name", want: "body.sort_keys.-1.column_name"},
		{name: "unknown column location", column: 3, loc: "body.source_column.extra", want: "body.source_column.extra"},
		{name: "non-body location", loc: "sheet_bindings.Data.sort_keys.amount", want: "sheet_bindings.Data.sort_keys.amount"},
		{name: "body alone", loc: "body", want: "body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc := strings.Split(tc.loc, ".")
			step := stepSheetCreate
			if tc.column != 0 {
				step = stepColumnCreate
			}
			issue := Issue{Loc: &loc, Msg: "Refused."}
			got := p.formatBindingIssue(Request{Sheet: "Data", Column: tc.column, step: step}, issue)
			if want := tc.want + ": Refused."; got != want {
				t.Errorf("issue = %q, want %q", got, want)
			}
			if !reflect.DeepEqual(loc, strings.Split(tc.loc, ".")) {
				t.Errorf("server loc mutated: %v", loc)
			}
		})
	}
	for _, loc := range []*[]string{nil, {}} {
		if got := p.formatBindingIssue(Request{Sheet: "Data", step: stepSheetCreate}, Issue{Loc: loc, Msg: "Refused."}); got != "Refused." {
			t.Errorf("issue without loc = %q", got)
		}
	}
}

func TestBindingIssuesIdentifyStoredSheetValues(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		lines := []string{}
		if explicit {
			lines = append(lines, "row_order: [{column: amount}]", "series: null", "instruction_row_count: 0")
		}
		m := bpManifest(t, "", bpData(lines...))
		p := &Plan{sheets: []plannedSheet{{name: "Data", spec: m.Spec.Sheets["Data"]}}}
		for _, tc := range []struct{ loc, key, path string }{
			{loc: "body.sort_keys.0.column_name", key: "row_order", path: "spec.sheets.Data.row_order[0].column"},
			{loc: "body.member_series_id", key: "series", path: "spec.sheets.Data.series"},
			{loc: "body.instruction_row_count", key: "instruction_row_count", path: "spec.sheets.Data.instruction_row_count"},
			{loc: "body.member_dataset_id", key: "table", path: "spec.sheets.Data.table"},
		} {
			loc := strings.Split(tc.loc, ".")
			want := tc.path
			if !explicit && tc.key != "table" {
				want += " (from stored binding; " + tc.key + " omitted)"
			}
			got := p.formatBindingIssue(Request{Sheet: "Data", step: stepSheetPatch}, Issue{Loc: &loc, Msg: "Refused."})
			if want += ": Refused."; got != want {
				t.Errorf("explicit=%t, loc=%s: issue = %q, want %q", explicit, tc.loc, got, want)
			}
		}
	}
}

func TestApplyMapsSortKeyRefusalAfterPlanning(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
		sheet := bpSheet(bpDataID, "Data", 5, []int{1}, 2)
		// Stored keys need not have consecutive positions; atlas's loc indexes
		// the request list, which apply sorts by position.
		sheet.SortKeys = &[]api.SortKeySchema{{Position: 9, ColumnName: "amount"}, {Position: 2, ColumnName: "policy_id"}}
		s.trees(revV3, bpTree(sheet))
		lines := []string{"table: 6", "header_rows: [1]", "data_start_row: 2"}
		if explicit {
			lines = append(lines, "row_order: [{column: policy_id}, {column: amount}]")
		}
		p, n := s.prepare(bpManifest(t, "", bpSheetDoc("Data", lines...)), ".")
		// The column existed when Prepare checked the schema, then vanished.
		s.fail(http.MethodPatch, bpSheets+"/"+bpDataID, http.StatusUnprocessableEntity,
			`{"detail":{"msg":"invalid","error_code":"BINDING_INVALID","error_id":"sort-refusal","context":{"errors":[`+
				`{"binding_kind":"sort_key","loc":["body","sort_keys","1","column_name"],"msg":"Column 'amount' does not exist."}]}}}`)
		r, err := s.client().Apply("token", p)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || r != nil {
			t.Fatalf("exit = %d, result = %+v, error = %v", code, r, err)
		}
		path := "spec.sheets.Data.row_order[1].column"
		if !explicit {
			path += " (from stored binding; row_order omitted)"
		}
		want := "patch deliverable sheet binding failed: 1 binding issue(s):\n  - " + path + ": Column 'amount' does not exist."
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
		var invalid *RevisionInvalidError
		var apiErr *APIError
		if !errors.As(err, &invalid) || len(invalid.Issues) != 1 || !reflect.DeepEqual(*invalid.Issues[0].Loc, []string{"body", "sort_keys", "1", "column_name"}) {
			t.Fatalf("lost original issue: %v", err)
		}
		if !errors.As(err, &apiErr) || apiErr.Code != "BINDING_INVALID" || apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Context["errors"] == nil {
			t.Errorf("lost API metadata: %+v", apiErr)
		}
		if got := s.since(n); len(got) != 1 || !strings.HasPrefix(got[0], "PATCH "+bpSheets+"/"+bpDataID+" ") {
			t.Errorf("requests = %v, want only the failed PATCH", got)
		}
	}
}
