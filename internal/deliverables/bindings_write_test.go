package deliverables

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	writeSheetBindingID  = "bbbbbbbb-0000-4000-8000-000000000001"
	writeColumnBindingID = "cccccccc-0000-4000-8000-000000000001"
	writeSeriesID        = "dddddddd-0000-4000-8000-000000000001"

	sheetBindingsRoute  = revisionPath + "/sheet-bindings"
	sheetBindingRoute   = sheetBindingsRoute + "/" + writeSheetBindingID
	columnBindingsRoute = "/api/orgs/" + testOrgID + "/deliverable-sheet-bindings/" + writeSheetBindingID + "/column-bindings"
	columnBindingRoute  = columnBindingsRoute + "/" + writeColumnBindingID

	writtenSheetBinding = `{"id":"` + writeSheetBindingID + `","member_dataset_id":12,"member_series_id":null,"sheet_name":"Claims",` +
		`"data_start_row":3,"header_rows":[2],"instruction_row_count":0,"row_filter":null,"sort_keys":[],"column_bindings":[]}`
	patchedSheetBinding = `{"id":"` + writeSheetBindingID + `","member_dataset_id":12,"member_series_id":null,"sheet_name":"Claims",` +
		`"data_start_row":4,"header_rows":[3],"instruction_row_count":0,"row_filter":null,"sort_keys":[],"column_bindings":[],` +
		`"anchor_report":{"column_bindings":[{"column_binding_id":"` + writeColumnBindingID + `","sheet_binding_id":"` + writeSheetBindingID + `",` +
		`"sheet_name":"Claims","from_column_index":3,"to_column_index":4,"outcome":"moved","reason":null,"target_label":"Amount"}],"cell_bindings":[]}}`
	writtenColumnBinding = `{"id":"` + writeColumnBindingID + `","source_type":"column","source_column":"amount","run_param":null,` +
		`"target_column_index":3,"format_override":null,"origin":"user","target_label":"Amount","needs_confirmation":false,"warnings":[]}`
)

func TestBindingWritesSendTheBodyAsRawJSON(t *testing.T) {
	revision, sheet, column := uuid.MustParse(revV3), uuid.MustParse(writeSheetBindingID), uuid.MustParse(writeColumnBindingID)
	wantSheet := func(t *testing.T, got *api.SheetBindingResponse, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID != sheet || got.SheetName != "Claims" || got.DataStartRow != 3 {
			t.Errorf("sheet binding = %+v, want the response's", got)
		}
	}
	wantColumn := func(t *testing.T, got *api.ColumnBindingResponse, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID != column || got.TargetColumnIndex != 3 || got.TargetLabel == nil || *got.TargetLabel != "Amount" {
			t.Errorf("column binding = %+v, want the response's", got)
		}
	}
	for _, tc := range []struct {
		name     string
		method   string
		path     string
		response string
		want     string
		send     func(*testing.T, *Client)
	}{
		{name: "sheet create leaves unwritten keys out", method: http.MethodPost, path: sheetBindingsRoute, response: writtenSheetBinding,
			want: `{"data_start_row":3,"header_rows":[2],"member_dataset_id":12,"sheet_name":"Claims","sort_keys":[]}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.CreateSheetBinding("token", revision, map[string]any{"member_dataset_id": 12, "sheet_name": "Claims",
					"data_start_row": 3, "header_rows": []int{2}, "sort_keys": []any{}})
				wantSheet(t, got, err)
			}},
		{name: "sheet create names a series", method: http.MethodPost, path: sheetBindingsRoute, response: writtenSheetBinding,
			want: `{"data_start_row":3,"header_rows":[2],"instruction_row_count":2,"member_dataset_id":12,"member_series_id":"` + writeSeriesID + `","sheet_name":"Claims"}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.CreateSheetBinding("token", revision, map[string]any{"member_dataset_id": 12, "sheet_name": "Claims",
					"data_start_row": 3, "header_rows": []int{2}, "instruction_row_count": 2, "member_series_id": uuid.MustParse(writeSeriesID)})
				wantSheet(t, got, err)
			}},
		{name: "sheet patch sends a nil value as null", method: http.MethodPatch, path: sheetBindingRoute, response: patchedSheetBinding,
			want: `{"data_start_row":4,"header_rows":null,"instruction_row_count":null,"member_dataset_id":12,"member_series_id":null,` +
				`"sheet_name":"Claims","sort_keys":[{"column_name":"amount","descending":true,"position":0}]}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.PatchSheetBinding("token", revision, sheet, map[string]any{"member_dataset_id": 12, "sheet_name": "Claims",
					"data_start_row": 4, "member_series_id": nil, "header_rows": nil, "instruction_row_count": nil,
					"sort_keys": []map[string]any{{"position": 0, "column_name": "amount", "descending": true}}})
				if err != nil {
					t.Fatal(err)
				}
				if got == nil || got.ID != sheet || got.DataStartRow != 4 || got.AnchorReport == nil || len(got.AnchorReport.ColumnBindings) != 1 {
					t.Fatalf("sheet binding = %+v, want the response's binding and anchor report", got)
				}
				if change := got.AnchorReport.ColumnBindings[0]; change.ColumnBindingID != column || change.Outcome != api.BindingAnchorOutcomeMoved ||
					change.FromColumnIndex != 3 || change.ToColumnIndex != 4 {
					t.Errorf("anchor change = %+v, want column %s moved from 3 to 4", change, column)
				}
			}},
		{name: "sheet patch leaves an unwritten key out", method: http.MethodPatch, path: sheetBindingRoute, response: writtenSheetBinding,
			want: `{"data_start_row":3,"member_dataset_id":12,"sheet_name":"Claims"}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.PatchSheetBinding("token", revision, sheet, map[string]any{"member_dataset_id": 12, "sheet_name": "Claims", "data_start_row": 3})
				if err != nil {
					t.Fatal(err)
				}
				if got == nil || got.ID != sheet || got.AnchorReport != nil {
					t.Errorf("sheet binding = %+v, want the response's, with no anchor report", got)
				}
			}},
		{name: "column create", method: http.MethodPost, path: columnBindingsRoute, response: writtenColumnBinding,
			want: `{"source_column":"amount","source_type":"column","target_column_index":3}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.CreateColumnBinding("token", sheet, map[string]any{"target_column_index": 3, "source_type": "column", "source_column": "amount"})
				wantColumn(t, got, err)
			}},
		{name: "column patch sends a nil value as null", method: http.MethodPatch, path: columnBindingRoute, response: writtenColumnBinding,
			want: `{"format_override":null,"run_param":"period","source_column":null,"source_type":"run_param","target_column_index":3}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.PatchColumnBinding("token", sheet, column, map[string]any{"target_column_index": 3, "source_type": "run_param",
					"source_column": nil, "run_param": "period", "format_override": nil})
				wantColumn(t, got, err)
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var sent []byte
			var contentType string
			mock.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, r *http.Request) {
				sent, _ = io.ReadAll(r.Body)
				contentType = r.Header.Get("Content-Type")
				respondBody(w, http.StatusOK, tc.response)
			})
			tc.send(t, NewClient(mock.URL(), testOrgID, 0))
			if got, want := strings.Join(mock.Requests(), ","), tc.method+" "+tc.path; got != want {
				t.Errorf("requests = %s, want %s", got, want)
			}
			if string(sent) != tc.want {
				t.Errorf("body = %s, want %s", sent, tc.want)
			}
			if contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", contentType)
			}
		})
	}
}

func TestBindingDeletesExpectNoContent(t *testing.T) {
	revision, sheet, column := uuid.MustParse(revV3), uuid.MustParse(writeSheetBindingID), uuid.MustParse(writeColumnBindingID)
	for _, tc := range []struct {
		name string
		path string
		send func(*Client) error
	}{
		{name: "sheet", path: sheetBindingRoute,
			send: func(c *Client) error { return c.DeleteSheetBinding("token", revision, sheet) }},
		{name: "column", path: columnBindingRoute,
			send: func(c *Client) error { return c.DeleteColumnBinding("token", sheet, column) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var sent []byte
			mock.RegisterHandler(http.MethodDelete, tc.path, func(w http.ResponseWriter, r *http.Request) {
				if r.Body != nil {
					sent, _ = io.ReadAll(r.Body)
				}
				w.WriteHeader(http.StatusNoContent)
			})
			if err := tc.send(NewClient(mock.URL(), testOrgID, 0)); err != nil {
				t.Fatal(err)
			}
			if got, want := strings.Join(mock.Requests(), ","), http.MethodDelete+" "+tc.path; got != want {
				t.Errorf("requests = %s, want %s", got, want)
			}
			if len(sent) != 0 {
				t.Errorf("body = %s, want none", sent)
			}
		})
	}
}

// bindingCall is one of the six binding writes, sent against a mock server.
type bindingCall struct {
	name   string
	method string
	path   string
	failed string
	body   bool
	send   func(*Client) error
}

func bindingCalls() []bindingCall {
	revision, sheet, column := uuid.MustParse(revV3), uuid.MustParse(writeSheetBindingID), uuid.MustParse(writeColumnBindingID)
	sheetBody := map[string]any{"member_dataset_id": 12, "sheet_name": "Claims", "data_start_row": 3}
	columnBody := map[string]any{"target_column_index": 3, "source_type": "column", "source_column": "amount"}
	return []bindingCall{
		{name: "sheet create", method: http.MethodPost, path: sheetBindingsRoute, failed: createSheetBindingFailed, body: true,
			send: func(c *Client) error { _, err := c.CreateSheetBinding("token", revision, sheetBody); return err }},
		{name: "sheet patch", method: http.MethodPatch, path: sheetBindingRoute, failed: patchSheetBindingFailed, body: true,
			send: func(c *Client) error { _, err := c.PatchSheetBinding("token", revision, sheet, sheetBody); return err }},
		{name: "sheet delete", method: http.MethodDelete, path: sheetBindingRoute, failed: deleteSheetBindingFailed,
			send: func(c *Client) error { return c.DeleteSheetBinding("token", revision, sheet) }},
		{name: "column create", method: http.MethodPost, path: columnBindingsRoute, failed: createColumnBindingFailed, body: true,
			send: func(c *Client) error { _, err := c.CreateColumnBinding("token", sheet, columnBody); return err }},
		{name: "column patch", method: http.MethodPatch, path: columnBindingRoute, failed: patchColumnBindingFailed, body: true,
			send: func(c *Client) error { _, err := c.PatchColumnBinding("token", sheet, column, columnBody); return err }},
		{name: "column delete", method: http.MethodDelete, path: columnBindingRoute, failed: deleteColumnBindingFailed,
			send: func(c *Client) error { return c.DeleteColumnBinding("token", sheet, column) }},
	}
}

func serveBindingRefusal(t *testing.T, call bindingCall, status int, detail string) *Client {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler(call.method, call.path, func(w http.ResponseWriter, _ *http.Request) {
		respondBody(w, status, `{"detail":`+detail+`}`)
	})
	return NewClient(mock.URL(), testOrgID, 0)
}

func TestBindingWriteRefusalsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		detail string
		code   int
		says   string
	}{
		{name: "draft gate 409", status: http.StatusConflict, code: qerrors.ExitBadUsage, says: "children are only editable while draft",
			detail: `{"msg":"Revision ` + revV3 + ` is enabled; children are only editable while draft. Create a new draft revision (optionally copying this one).","error_id":""}`},
		{name: "still referenced 409", status: http.StatusConflict, code: qerrors.ExitBadUsage, says: "is mirrored by cell(s) Summary!C4",
			detail: `{"msg":"Sheet 'Claims' is mirrored by cell(s) Summary!C4; delete them or bind them elsewhere before unbinding the sheet.",` +
				`"error_code":"ERR_STILL_REFERENCED","error_id":"","context":{"usage":{"cell_bindings":1}}}`},
		{name: "404", status: http.StatusNotFound, code: qerrors.ExitNotFound, says: "can not be found",
			detail: `{"msg":"Column binding ` + writeColumnBindingID + ` can not be found on this sheet binding.","error_id":""}`},
		{name: "500", status: http.StatusInternalServerError, code: qerrors.ExitGenericError, says: "Internal server error",
			detail: `{"msg":"Internal server error","error_code":"ERR_INTERNAL","error_id":"5A6B7C8D"}`},
		{name: "object-shaped 422 of another code", status: http.StatusUnprocessableEntity, code: qerrors.ExitBadUsage, says: "row_filter does not compile",
			detail: `{"msg":"row_filter does not compile","error_code":"ERR_VALIDATION","error_id":"","loc":["body","row_filter"]}`},
		{name: "BINDING_INVALID listing no issues", status: http.StatusUnprocessableEntity, code: qerrors.ExitBadUsage, says: "bad binding",
			detail: `{"msg":"bad binding","error_code":"BINDING_INVALID","error_id":"","context":{"errors":[]}}`},
	} {
		for _, call := range bindingCalls() {
			t.Run(tc.name+"/"+call.name, func(t *testing.T) {
				err := call.send(serveBindingRefusal(t, call, tc.status, tc.detail))
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
				}
				apiErr, ok := err.(*APIError)
				if !ok || apiErr.StatusCode != tc.status {
					t.Fatalf("error is %T (%#v), want the *APIError itself with status %d", err, err, tc.status)
				}
				if !strings.Contains(err.Error(), tc.says) || !strings.HasPrefix(err.Error(), call.failed) {
					t.Errorf("error lacks %q after %q: %v", tc.says, call.failed, err)
				}
			})
		}
	}
}

func TestBindingWritesListTheIssuesOfABindingInvalidRefusal(t *testing.T) {
	first := "Sheet 'Data' does not exist in the skeleton (sheets: ['Claims'])."
	second := "Header rows [5] are not above data_start_row 3."
	detail, err := json.Marshal(map[string]any{
		"msg":        first + "; " + second,
		"error_code": "BINDING_INVALID",
		"error_id":   "0D1E2F3A",
		"loc":        []string{"body", "sheet_name"},
		"context": map[string]any{"errors": []map[string]any{
			{"binding_kind": "sheet_binding", "binding_id": nil, "loc": []string{"body", "sheet_name"}, "msg": first},
			{"binding_kind": "sheet_binding", "binding_id": nil, "loc": []string{"body", "header_rows"}, "msg": second},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range bindingCalls() {
		if !call.body {
			continue
		}
		t.Run(call.name, func(t *testing.T) {
			err := call.send(serveBindingRefusal(t, call, http.StatusUnprocessableEntity, string(detail)))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			var invalid *RevisionInvalidError
			if !errors.As(err, &invalid) || len(invalid.Issues) != 2 || invalid.Issues[1].Msg != second {
				t.Fatalf("RevisionInvalidError = %#v; error = %v", invalid, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Code != "BINDING_INVALID" {
				t.Errorf("error metadata lost: %#v", apiErr)
			}
			want := call.failed + ": 2 binding issue(s):\n  - body.sheet_name: " + first + "\n  - body.header_rows: " + second
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			for _, msg := range []string{first, second} {
				if n := strings.Count(err.Error(), msg); n != 1 {
					t.Errorf("error says %q %d times: %q", msg, n, err)
				}
			}
		})
	}
}
