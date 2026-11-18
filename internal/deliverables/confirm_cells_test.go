package deliverables

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func confirmCellFixture(t *testing.T) *api.DeliverableRevisionResponse {
	t.Helper()
	var tree api.DeliverableRevisionResponse
	data := `{"id":"` + revV3 + `","state":"draft","cell_bindings":[
 {"id":"5d000000-0000-4000-8000-000000000001","sheet_name":"Summary","cell_ref":"B1",
  "source_type":"literal","literal_value":"Report","needs_confirmation":true},
 {"id":"5d000000-0000-4000-8000-000000000002","sheet_name":"Summary","cell_ref":"B2",
  "source_type":"run_param","run_param":"period","format_override":"yyyy-mm","needs_confirmation":true},
 {"id":"5d000000-0000-4000-8000-000000000003","sheet_name":"Summary","cell_ref":"B3",
  "source_type":"aggregate","aggregate_member_dataset_id":5,"aggregate_member_series_id":"` + bpSeriesID + `",
  "aggregate_func":"sum","aggregate_column":"premium","format_override":"0.00","label_cell_ref":"A3",
  "target_label":null,"needs_confirmation":true,
  "row_filter":{"description":"Large premiums","conditions":[{"column_name":"premium","operator":"gte",
    "value":"9007199254740993","value_high":"9007199254740995","value_list":["9007199254740997"],"case_sensitive":false}],
    "predicate":{"source":"def applies(row):\n    return row['premium'] > 0","columns":["premium"]}}},
 {"id":"5d000000-0000-4000-8000-000000000004","sheet_name":"Summary","cell_ref":"B4",
  "source_type":"aggregate","same_rows_as_sheet_binding_id":"` + bpDataID + `",
  "aggregate_func":"count","needs_confirmation":true},
 {"id":"5d000000-0000-4000-8000-000000000005","sheet_name":"Summary","cell_ref":"B5",
  "source_type":"literal","literal_value":"Already saved","needs_confirmation":false},
 {"id":"5d000000-0000-4000-8000-000000000006","sheet_name":"Other","cell_ref":"B1",
  "source_type":"literal","literal_value":"Leave alone","needs_confirmation":true}]}`
	if err := json.Unmarshal([]byte(data), &tree); err != nil {
		t.Fatal(err)
	}
	return &tree
}

func TestParseCellList(t *testing.T) {
	got, err := ParseCellList([]string{" $b$001 ", "d2", "XFD1048576"})
	if err != nil || !reflect.DeepEqual(got, []string{"B1", "D2", "XFD1048576"}) {
		t.Fatalf("refs = %v, %v", got, err)
	}
	for _, keys := range [][]string{nil, {""}, {" "}, {"B1", "$b$01"}, {"A"}, {"0"}, {"B0"}, {"XFE1"}, {"A1048577"}, {"B1:C2"}, {"Summary!B1"}} {
		if _, err := ParseCellList(keys); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Errorf("%q: error = %v, want exit 2", keys, err)
		}
	}
}

func TestConfirmCellsPreservesEverySourceAndFilter(t *testing.T) {
	tree := confirmCellFixture(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	c := NewClient(mock.Server.URL, testOrgID, 0)
	var wantRequests []string
	for _, binding := range *tree.CellBindings {
		if binding.SheetName != "Summary" || !binding.NeedsConfirmation {
			continue
		}
		path := fmt.Sprintf("/api/orgs/%s/deliverable-revisions/%s/cell-bindings/%s", testOrgID, tree.ID, binding.ID)
		wantRequests = append(wantRequests, "PATCH "+path)
		mock.RegisterHandler("PATCH", path, func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			var body map[string]json.RawMessage
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			// Compare with the response's fields, excluding server-only fields.
			stored, _ := json.Marshal(binding)
			var want map[string]json.RawMessage
			_ = json.Unmarshal(stored, &want)
			for _, key := range []string{"id", "needs_confirmation", "target_label"} {
				delete(want, key)
			}
			for _, key := range []string{"row_filter", "aggregate_func"} {
				if _, ok := want[key]; !ok {
					want[key] = json.RawMessage("null")
				}
			}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("%s body = %s, want stored fields %s", binding.CellRef, data, stored)
			}
			binding.NeedsConfirmation = false
			binding.TargetLabel = strPtr("New label")
			testutil.RespondJSON(w, http.StatusOK, binding)
		})
	}
	result, err := c.ConfirmCells("token", tree, "Summary", []string{"B5", "B4", "B3", "B2", "B1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := mock.Requests(); !reflect.DeepEqual(got, wantRequests) {
		t.Fatalf("requests = %v, want %v", got, wantRequests)
	}
	if len(result.Cells) != 5 || result.Cells[4].Confirmed || result.Cells[4].Binding.LiteralValue == nil {
		t.Fatalf("result = %+v", result)
	}
	for _, cell := range result.Cells[:4] {
		if !cell.Confirmed || cell.Binding.NeedsConfirmation || pointerText(cell.Binding.TargetLabel) != "New label" {
			t.Errorf("confirmed cell = %+v, want PATCH response", cell)
		}
	}
	data, _ := json.Marshal(result.Bindings())
	for _, number := range []string{"9007199254740993", "9007199254740995", "9007199254740997"} {
		if !strings.Contains(string(data), number) {
			t.Errorf("returned filter lost %s: %s", number, data)
		}
	}
	wantReport := "  Cell Summary!B1: confirmed\n  Cell Summary!B2: confirmed\n  Cell Summary!B3: confirmed\n" +
		"  Cell Summary!B4: confirmed\n  Cell Summary!B5: already confirmed\n"
	if got := FormatConfirmCells(result); got != wantReport {
		t.Errorf("report = %q, want %q", got, wantReport)
	}
}

func TestConfirmCellsRefusesUnboundCellsBeforeWrites(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	c := NewClient(mock.Server.URL, testOrgID, 0)
	for _, sheet := range []string{"Summary", "Missing", "summary"} {
		_, err := c.ConfirmCells("token", confirmCellFixture(t), sheet, []string{"B1", "C1"})
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "not bound") {
			t.Errorf("sheet %q: error = %v, want exit 2", sheet, err)
		}
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Fatalf("requests = %v, want none", got)
	}
}

func TestCellConfirmationNotesSkipPlannedSavesAndDeletes(t *testing.T) {
	tree := confirmCellFixture(t)
	p := &Plan{Slug: "monthly-bordereau", Release: "v3 beta"}
	requests := []Request{
		{step: stepCellPatch, stored: (*tree.CellBindings)[0].ID},
		{step: stepCellDelete, stored: (*tree.CellBindings)[1].ID},
		{step: stepSheetDelete, stored: uuid.MustParse(bpDataID)},
	}
	notes := p.draftConfirmations(tree, requests)
	if len(notes) != 2 || !strings.Contains(notes[0], `sheet "Other" cell B1 needs confirmation`) ||
		!strings.Contains(notes[1], `sheet "Summary" cells B3, B4 need confirmation`) ||
		!strings.HasSuffix(notes[1], "confirm-cells monthly-bordereau --release 'v3 beta' --sheet Summary --cell 'B3,B4'") {
		t.Fatalf("notes = %q", notes)
	}
	p.Revision = &Revision{State: api.DeliverableRevisionStateEnabled}
	if got := p.draftConfirmations(tree, nil); len(got) != 0 {
		t.Fatalf("enabled notes = %q", got)
	}
}

func TestConfirmCellsFailurePreservesExitAndProgress(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusNotFound, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			tree := confirmCellFixture(t)
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			for i, cell := range (*tree.CellBindings)[:2] {
				path := fmt.Sprintf("/api/orgs/%s/deliverable-revisions/%s/cell-bindings/%s", testOrgID, tree.ID, cell.ID)
				mock.RegisterHandler("PATCH", path, func(w http.ResponseWriter, r *http.Request) {
					if i == 0 {
						cell.NeedsConfirmation = false
						testutil.RespondJSON(w, http.StatusOK, cell)
					} else {
						testutil.RespondJSON(w, status, json.RawMessage(`{"detail":{"msg":"Cannot save binding","error_code":"BINDING_INVALID"}}`))
					}
				})
			}
			c := NewClient(mock.Server.URL, testOrgID, 0)
			_, err := c.ConfirmCells("token", tree, "Summary", []string{"B3", "B2", "B1"})
			wantCode := qerrors.GetExitCode(qerrors.FromHTTPStatus(status, "failure"))
			var apiErr *APIError
			if qerrors.GetExitCode(err) != wantCode || !errors.As(err, &apiErr) || apiErr.StatusCode != status {
				t.Fatalf("error = %v, want wrapped APIError and exit %d", err, wantCode)
			}
			if !strings.HasPrefix(err.Error(), `confirm cell "Summary!B2":`) ||
				!strings.HasSuffix(err.Error(), "Confirmed before the failure: Summary!B1") || len(mock.Requests()) != 2 {
				t.Fatalf("error = %v, requests = %v", err, mock.Requests())
			}
		})
	}
}
