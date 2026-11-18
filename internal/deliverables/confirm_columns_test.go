package deliverables

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	confirmColumnB  = "cccccccc-0000-4000-8000-00000000000b"
	confirmColumnC  = "cccccccc-0000-4000-8000-00000000000c"
	confirmColumnAA = "cccccccc-0000-4000-8000-0000000000aa"

	// confirmSheet binds B (unflagged), C (flagged, a table column with no
	// format) and AA (flagged, a run parameter with a format).
	confirmSheet = `{"id":"` + writeSheetBindingID + `","member_dataset_id":12,"member_series_id":null,"sheet_name":"Claims",` +
		`"data_start_row":3,"header_rows":[2],"instruction_row_count":0,"row_filter":null,"sort_keys":[],"column_bindings":[` +
		`{"id":"` + confirmColumnAA + `","source_type":"run_param","source_column":null,"run_param":"period","target_column_index":27,` +
		`"format_override":"yyyy-mm","origin":"user","target_label":null,"needs_confirmation":true,"warnings":[]},` +
		`{"id":"` + confirmColumnB + `","source_type":"column","source_column":"premium","run_param":null,"target_column_index":2,` +
		`"format_override":"0.00","origin":"suggested","target_label":"Premium","needs_confirmation":false,"warnings":[]},` +
		`{"id":"` + confirmColumnC + `","source_type":"column","source_column":"amount","run_param":null,"target_column_index":3,` +
		`"format_override":null,"origin":"suggested","target_label":null,"needs_confirmation":true,"warnings":[]}]}`

	confirmedC = `{"id":"` + confirmColumnC + `","source_type":"column","source_column":"amount","run_param":null,"target_column_index":3,` +
		`"format_override":null,"origin":"user","target_label":"Amount","needs_confirmation":false,` +
		`"warnings":[{"binding_kind":"column_binding","binding_id":"` + confirmColumnC + `","loc":["column_binding","C"],"msg":"Formula overwritten"}]}`
	confirmedAA = `{"id":"` + confirmColumnAA + `","source_type":"run_param","source_column":null,"run_param":"period","target_column_index":27,` +
		`"format_override":"yyyy-mm","origin":"user","target_label":null,"needs_confirmation":false,"warnings":[]}`

	wantBodyC  = `{"format_override":null,"run_param":null,"source_column":"amount","source_type":"column","target_column_index":3}`
	wantBodyAA = `{"format_override":"yyyy-mm","run_param":"period","source_column":null,"source_type":"run_param","target_column_index":27}`
)

func confirmColumnRoute(id string) string { return columnBindingsRoute + "/" + id }

func confirmFixture(t *testing.T) api.SheetBindingResponse {
	t.Helper()
	var sheet api.SheetBindingResponse
	if err := json.Unmarshal([]byte(confirmSheet), &sheet); err != nil {
		t.Fatal(err)
	}
	return sheet
}

// confirmServer answers each column-binding PATCH with responses[id], 200
// unless statuses names another, and records each request body by id.
func confirmServer(t *testing.T, responses map[string]string, statuses map[string]int) (*Client, *testutil.MockAPIServer, map[string]string) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	bodies := map[string]string{}
	for _, id := range []string{confirmColumnB, confirmColumnC, confirmColumnAA} {
		mock.RegisterHandler(http.MethodPatch, confirmColumnRoute(id), func(w http.ResponseWriter, r *http.Request) {
			sent, _ := io.ReadAll(r.Body)
			bodies[id] = string(sent)
			status := http.StatusOK
			if s, ok := statuses[id]; ok {
				status = s
			}
			respondBody(w, status, responses[id])
		})
	}
	return NewClient(mock.URL(), testOrgID, 0), mock, bodies
}

func TestParseColumnList(t *testing.T) {
	got, err := ParseColumnList([]string{"c", " AA", "2 "})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{3, 27, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("indexes = %v, want %v", got, want)
	}
	for _, keys := range [][]string{nil, {}, {"C", "3"}, {"aa", "AA"}, {"C", ""}, {"0"}, {"XFE"}, {"C1"}} {
		t.Run(strings.Join(keys, ","), func(t *testing.T) {
			if _, err := ParseColumnList(keys); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("exit = %d, want 2; error = %v", qerrors.GetExitCode(err), err)
			}
		})
	}
}

func TestConfirmColumnsSendsEachFlaggedBindingWhole(t *testing.T) {
	c, mock, bodies := confirmServer(t, map[string]string{confirmColumnC: confirmedC, confirmColumnAA: confirmedAA}, nil)
	result, err := c.ConfirmColumns("token", confirmFixture(t), []int{27, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mock.Requests(), []string{"PATCH " + confirmColumnRoute(confirmColumnC), "PATCH " + confirmColumnRoute(confirmColumnAA)}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v (column order, nothing for B)", got, want)
	}
	if bodies[confirmColumnC] != wantBodyC {
		t.Errorf("C body = %s, want %s", bodies[confirmColumnC], wantBodyC)
	}
	if bodies[confirmColumnAA] != wantBodyAA {
		t.Errorf("AA body = %s, want %s", bodies[confirmColumnAA], wantBodyAA)
	}
	var states []string
	for _, column := range result.Columns {
		states = append(states, ColumnLetter(column.Binding.TargetColumnIndex)+" "+string(column.Binding.Origin)+" "+
			map[bool]string{true: "confirmed", false: "skipped"}[column.Confirmed])
	}
	if want := []string{"B suggested skipped", "C user confirmed", "AA user confirmed"}; !reflect.DeepEqual(states, want) {
		t.Errorf("columns = %v, want %v (the PATCH response for a confirmed one, the stored binding otherwise)", states, want)
	}
	if label := result.Columns[1].Binding.TargetLabel; label == nil || *label != "Amount" {
		t.Errorf("confirmed C label = %v, want the response's", label)
	}
	report := FormatConfirmColumns(result)
	want := "  Column 2 (B) <- column premium: already confirmed\n" +
		"  Column 3 (C) <- column amount: confirmed\n" +
		"    Warning: column_binding.C: Formula overwritten\n" +
		"  Column 27 (AA) <- run parameter period: confirmed\n"
	if report != want {
		t.Errorf("report = %q, want %q", report, want)
	}
}

func TestConfirmColumnsSendsNothingForAnUnflaggedBinding(t *testing.T) {
	c, mock, _ := confirmServer(t, nil, nil)
	result, err := c.ConfirmColumns("token", confirmFixture(t), []int{2})
	if err != nil {
		t.Fatal(err)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
	if len(result.Columns) != 1 || result.Columns[0].Confirmed || result.Columns[0].Binding.ID.String() != confirmColumnB {
		t.Errorf("columns = %+v, want B as stored", result.Columns)
	}
}

func TestConfirmColumnsRefusesAnUnboundColumnBeforeAnyWrite(t *testing.T) {
	empty := confirmFixture(t)
	empty.ColumnBindings = &[]api.ColumnBindingResponse{}
	for _, tc := range []struct {
		name    string
		sheet   api.SheetBindingResponse
		indexes []int
		says    string
	}{
		{"one", confirmFixture(t), []int{3, 4}, `column D is not bound on sheet "Claims" (bound: B, C, AA)`},
		{"several", confirmFixture(t), []int{5, 3, 4}, `columns D, E are not bound on sheet "Claims" (bound: B, C, AA)`},
		{"no bindings", empty, []int{3}, `column C is not bound on sheet "Claims" (it has no column bindings)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock, _ := confirmServer(t, nil, nil)
			_, err := c.ConfirmColumns("token", tc.sheet, tc.indexes)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			if err.Error() != tc.says {
				t.Errorf("error = %q, want %q", err, tc.says)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestConfirmColumnsFailureNamesWhatWasConfirmed(t *testing.T) {
	refusal := `{"detail":{"msg":"Revision ` + revV3 + ` is enabled; children are only editable while draft.","error_id":""}}`
	for _, tc := range []struct {
		name      string
		failing   string
		status    int
		code      int
		requests  int
		confirmed string
	}{
		{name: "second write 409", failing: confirmColumnAA, status: http.StatusConflict, code: qerrors.ExitBadUsage, requests: 2,
			confirmed: "\n\nConfirmed before the failure: C"},
		{name: "first write 404", failing: confirmColumnC, status: http.StatusNotFound, code: qerrors.ExitNotFound, requests: 1},
		{name: "second write 500", failing: confirmColumnAA, status: http.StatusInternalServerError, code: qerrors.ExitGenericError, requests: 2,
			confirmed: "\n\nConfirmed before the failure: C"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responses := map[string]string{confirmColumnC: confirmedC, confirmColumnAA: confirmedAA}
			responses[tc.failing] = refusal
			c, mock, _ := confirmServer(t, responses, map[string]int{tc.failing: tc.status})
			result, err := c.ConfirmColumns("token", confirmFixture(t), []int{3, 27, 2})
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if result != nil {
				t.Errorf("result = %+v, want nil", result)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Errorf("error %v does not wrap the %d *APIError", err, tc.status)
			}
			letter := map[string]string{confirmColumnC: "C", confirmColumnAA: "AA"}[tc.failing]
			if !strings.HasPrefix(err.Error(), "confirm column "+letter+": "+patchColumnBindingFailed) {
				t.Errorf("error = %q, want it to name column %s", err, letter)
			}
			if strings.Contains(err.Error(), "Confirmed before the failure") != (tc.confirmed != "") ||
				!strings.HasSuffix(err.Error(), tc.confirmed) {
				t.Errorf("error = %q, want it to end %q", err, tc.confirmed)
			}
			if got := len(mock.Requests()); got != tc.requests {
				t.Errorf("requests = %v, want %d: nothing after the failure", mock.Requests(), tc.requests)
			}
		})
	}
}
