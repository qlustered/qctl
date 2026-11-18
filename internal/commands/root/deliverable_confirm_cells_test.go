package root

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/pflag"
)

const confirmCell2ID = "5d000000-0000-4000-8000-000000000002"

func resetConfirmCellsFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"deliverable", "confirm-cells"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"release", "revision", "sheet", "cell"} {
			flag := cmd.Flags().Lookup(name)
			if slice, ok := flag.Value.(pflag.SliceValue); ok {
				_ = slice.Replace(nil)
			} else if err := flag.Value.Set(flag.DefValue); err != nil {
				t.Fatal(err)
			}
			flag.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

func runConfirmCells(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetArgs(append([]string{"deliverable", "confirm-cells"}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), errOut.String(), err
}

func TestDeliverableConfirmCellsFormatsAndSelectors(t *testing.T) {
	for _, tc := range []struct {
		format, ref string
		flags       []string
	}{
		{"table", "monthly-bordereau", nil},
		{"json", "Monthly bordereau", []string{"--release", "2026.2"}},
		{"yaml", "deliverable/" + liveDeliverableID, []string{"--revision", "deliverable-revision/" + draftRevisionID}},
		{"json", liveDeliverableID, []string{"--release", draftRevisionID}},
	} {
		t.Run(tc.format+tc.ref, func(t *testing.T) {
			resetConfirmCellsFlags(t)
			s := cellsServer(t)
			flagged := strings.Replace(rootCell, `"needs_confirmation":false`, `"needs_confirmation":true`, 1)
			other := strings.ReplaceAll(strings.ReplaceAll(rootCell, rootCellID, confirmCell2ID), `"B1"`, `"B2"`)
			tree := strings.Replace(sheetsDraftTree, `"cell_bindings":[]`, `"cell_bindings":[`+other+`,`+flagged+`]`, 1)
			answerRevisionTree(t, s.MockAPIServer, draftRevisionID, tree)
			saved := strings.TrimSuffix(rootCell, "}") + `,"label_cell_ref":"A1","target_label":"Report"}`
			s.answer(t, "PATCH", rootCellsPath+"/"+rootCellID, saved)
			args := []string{tc.ref, "--sheet", "Summary", "--cell", " B2 , $b$01 ", "-o", tc.format}
			out, errOut, err := runConfirmCells(t, append(args, tc.flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			wantWrites := []string{"PATCH " + rootCellsPath + "/" + rootCellID}
			if got := s.writes(); !reflect.DeepEqual(got, wantWrites) {
				t.Fatalf("writes = %v, want %v", got, wantWrites)
			}
			var body map[string]json.RawMessage
			_ = json.Unmarshal([]byte(s.bodies[wantWrites[0]]), &body)
			if len(body) != 13 || string(body["literal_value"]) != `"Report"` || string(body["cell_ref"]) != `"B1"` ||
				string(body["row_filter"]) != "null" || string(body["label_cell_ref"]) != "null" {
				t.Errorf("body = %v", body)
			}
			report := errOut
			if tc.format == "table" {
				report = out
				if errOut != "" {
					t.Errorf("stderr = %q", errOut)
				}
			} else {
				want := `[{
 "id":"` + rootCellID + `","sheet_name":"Summary","cell_ref":"B1","source_type":"literal","literal_value":"Report",
 "run_param":null,"format_override":null,"needs_confirmation":false,"label_cell_ref":"A1","target_label":"Report",
 "aggregate_column":null,"aggregate_member_dataset_id":null,"aggregate_member_series_id":null,"same_rows_as_sheet_binding_id":null},
 {"id":"` + confirmCell2ID + `","sheet_name":"Summary","cell_ref":"B2","source_type":"literal","literal_value":"Report",
 "run_param":null,"format_override":null,"needs_confirmation":false,"label_cell_ref":null,"target_label":null,
 "aggregate_column":null,"aggregate_member_dataset_id":null,"aggregate_member_series_id":null,"same_rows_as_sheet_binding_id":null}]`
				if tc.format == "json" {
					assertSeriesJSON(t, out, want)
				} else {
					testutil.AssertYAMLReadsAsJSON(t, out, want)
				}
			}
			wantReport := "deliverable/" + liveDeliverableID + " release \"2026.2\" sheet \"Summary\"\n" +
				"  Cell Summary!B1: confirmed\n  Cell Summary!B2: already confirmed\n"
			if report != wantReport {
				t.Errorf("report = %q, want %q", report, wantReport)
			}
		})
	}
}

func TestDeliverableConfirmCellsKeepsFilterNumbers(t *testing.T) {
	resetConfirmCellsFlags(t)
	s := cellsServer(t)
	cell := `{"id":"` + rootCellID + `","sheet_name":"Summary","cell_ref":"B1","source_type":"aggregate",
 "aggregate_member_dataset_id":42,"aggregate_func":"count","needs_confirmation":true,
 "row_filter":{"description":"One policy","conditions":[{"column_name":"policy","operator":"eq","value":"9007199254740993"}]}}`
	tree := strings.Replace(sheetsDraftTree, `"cell_bindings":[]`, `"cell_bindings":[`+cell+`]`, 1)
	answerRevisionTree(t, s.MockAPIServer, draftRevisionID, tree)
	s.answer(t, "PATCH", rootCellsPath+"/"+rootCellID, strings.Replace(cell, `"needs_confirmation":true`, `"needs_confirmation":false`, 1))
	out, _, err := runConfirmCells(t, liveDeliverableID, "--sheet", "Summary", "--cell", "B1", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if body := s.bodies["PATCH "+rootCellsPath+"/"+rootCellID]; !strings.Contains(body, "9007199254740993") ||
		!strings.Contains(out, "9007199254740993") {
		t.Fatalf("number changed: body = %s, output = %s", body, out)
	}
}

func TestDeliverableConfirmCellsLocalValidation(t *testing.T) {
	base := []string{liveDeliverableID, "--sheet", "Summary"}
	for _, tail := range [][]string{
		nil, {"--cell", ""}, {"--cell", "B1,"}, {"--cell", "b1,$B$01"}, {"--cell", "A0"}, {"--cell", "XFE1"},
		{"--cell", "B1:C2"}, {"--cell", "Summary!B1"}, {"--cell", "B1", "--release", ""},
		{"--cell", "B1", "--revision", ""}, {"--cell", "B1", "--revision", "bad"},
		{"--cell", "B1", "--release", "2026.2", "--revision", draftRevisionID}, {"--cell", "B1", "-o", "name"},
		{"--cell", "B1", "--sheet", ""},
	} {
		t.Run(strings.Join(tail, " "), func(t *testing.T) {
			resetConfirmCellsFlags(t)
			args := append(append([]string{"deliverable", "confirm-cells"}, base...), tail...)
			if err := runOutsideGetWithoutContext(t, args...); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("error = %v, want exit 2", err)
			}
		})
	}
	for _, args := range [][]string{nil, {""}, {"one", "two"}, {liveDeliverableID, "--cell", "B1"}} {
		resetConfirmCellsFlags(t)
		if err := runOutsideGetWithoutContext(t, append([]string{"deliverable", "confirm-cells"}, args...)...); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Errorf("%q: error = %v, want exit 2", args, err)
		}
	}
}

func TestDeliverableConfirmCellsRefusesWrongTargets(t *testing.T) {
	for _, tc := range []struct {
		name, detail, sheet, cell string
		flags                     []string
		code                      int
	}{
		{name: "enabled", flags: []string{"--release", "2026.1"}, code: 2},
		{name: "disabled", detail: strings.Replace(sheetsDraftTree, `"state":"draft"`, `"state":"disabled"`, 1), code: 2},
		{name: "enabled after listing", detail: revisionTreeRecord, code: 2},
		{name: "foreign parent", detail: strings.ReplaceAll(sheetsDraftTree, liveDeliverableID, trashedDeliverableID), code: 3},
		{name: "unknown sheet", sheet: "summary", code: 2},
		{name: "missing cell beside flagged", cell: "B1,C1", code: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetConfirmCellsFlags(t)
			s := cellsServer(t)
			detail := tc.detail
			if detail == "" {
				detail = sheetsDraftTree
			}
			flagged := strings.Replace(rootCell, `"needs_confirmation":false`, `"needs_confirmation":true`, 1)
			detail = strings.Replace(detail, `"cell_bindings":[]`, `"cell_bindings":[`+flagged+`]`, 1)
			answerRevisionTree(t, s.MockAPIServer, draftRevisionID, detail)
			sheet, cell := tc.sheet, tc.cell
			if sheet == "" {
				sheet = "Summary"
			}
			if cell == "" {
				cell = "B1"
			}
			_, _, err := runConfirmCells(t, append([]string{liveDeliverableID, "--sheet", sheet, "--cell", cell}, tc.flags...)...)
			if qerrors.GetExitCode(err) != tc.code || len(s.writes()) != 0 {
				t.Fatalf("error = %v, writes = %v, want exit %d without writes", err, s.writes(), tc.code)
			}
		})
	}
}

func TestDeliverableCellConfirmationNotesInApplyAndDiff(t *testing.T) {
	for _, verb := range []string{"apply", "diff"} {
		for _, cells := range []string{"{'Summary!B1': {literal: Report}}", "{'Summary!B1': {literal: Changed}}", "{}"} {
			t.Run(verb+cells, func(t *testing.T) {
				s := cellsServer(t)
				flagged := strings.Replace(rootCell, `"needs_confirmation":false`, `"needs_confirmation":true`, 1)
				tree := strings.Replace(sheetsDraftTree, `"cell_bindings":[]`, `"cell_bindings":[`+flagged+`]`, 1)
				answerRevisionTree(t, s.MockAPIServer, draftRevisionID, tree)
				for _, method := range []string{"PATCH", "DELETE"} {
					s.RegisterHandler(method, rootCellsPath+"/"+rootCellID, func(w http.ResponseWriter, r *http.Request) {
						if r.Body != nil {
							data, _ := io.ReadAll(r.Body)
							s.bodies[method+" "+rootCellsPath+"/"+rootCellID] = string(data)
						}
						if method == "DELETE" {
							answerRevisionTree(t, s.MockAPIServer, draftRevisionID, sheetsDraftTree)
							w.WriteHeader(http.StatusNoContent)
						} else {
							answerRevisionTree(t, s.MockAPIServer, draftRevisionID, strings.Replace(tree, flagged, rootCell, 1))
							testutil.RespondJSON(w, http.StatusOK, json.RawMessage(rootCell))
						}
					})
				}
				path := writeManifest(t, t.TempDir(), "cells.yaml", rootCellsManifest(cells))
				args := []string{verb, "-f", path}
				if verb == "apply" {
					args = append(args, "--yes")
				}
				out, errOut, err := runManifest(t, args...)
				wantCode := 0
				matching := strings.Contains(cells, "literal: Report")
				if verb == "diff" && !matching {
					wantCode = 1
				}
				if qerrors.GetExitCode(err) != wantCode {
					t.Fatalf("error = %v, want exit %d", err, wantCode)
				}
				text := out + errOut
				if strings.Contains(text, "confirm-cells") != matching {
					t.Fatalf("notes = %s, want confirmation command only for matching cell", text)
				}
				if (verb == "diff" || matching) && len(s.writes()) != 0 {
					t.Fatalf("unexpected writes: %v", s.writes())
				}
			})
		}
	}
}

func TestDeliverableConfirmCellsAlreadyConfirmedAndPartialFailure(t *testing.T) {
	for _, status := range []int{0, http.StatusConflict, http.StatusNotFound, http.StatusInternalServerError} {
		resetConfirmCellsFlags(t)
		s := cellsServer(t)
		if status == 0 {
			out, _, err := runConfirmCells(t, liveDeliverableID, "--sheet", "Summary", "--cell", "B1")
			if err != nil || len(s.writes()) != 0 || !strings.Contains(out, "already confirmed") {
				t.Fatalf("output = %s, error = %v, writes = %v", out, err, s.writes())
			}
			continue
		}
		flagged := strings.Replace(rootCell, `"needs_confirmation":false`, `"needs_confirmation":true`, 1)
		other := strings.ReplaceAll(strings.ReplaceAll(flagged, rootCellID, confirmCell2ID), `"B1"`, `"B2"`)
		tree := strings.Replace(sheetsDraftTree, `"cell_bindings":[]`, `"cell_bindings":[`+other+`,`+flagged+`]`, 1)
		answerRevisionTree(t, s.MockAPIServer, draftRevisionID, tree)
		s.RegisterHandler("PATCH", rootCellsPath+"/"+confirmCell2ID, func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, status, json.RawMessage(`{"detail":{"msg":"Cannot save binding"}}`))
		})
		out, _, err := runConfirmCells(t, liveDeliverableID, "--sheet", "Summary", "--cell", "B2", "--cell", "B1", "-o", "json")
		if qerrors.GetExitCode(err) != qerrors.GetExitCode(qerrors.FromHTTPStatus(status, "failure")) || out != "" ||
			!strings.HasSuffix(err.Error(), "Confirmed before the failure: Summary!B1") ||
			!reflect.DeepEqual(s.writes(), []string{"PATCH " + rootCellsPath + "/" + rootCellID, "PATCH " + rootCellsPath + "/" + confirmCell2ID}) {
			t.Fatalf("output = %s, error = %v, writes = %v", out, err, s.writes())
		}
	}
}
