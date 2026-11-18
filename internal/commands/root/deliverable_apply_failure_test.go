package root

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	// reanchorManifest moves sheet Data's header row to 2 and its data to row
	// 3, keeps A, adds C, and lists premium at D.
	reanchorManifest = deliverableManifestHead + "monthly-bordereau\n" + liveDeliverableSpec + "  release: \"2026.2\"\n  sheets:\n" +
		"    Data:\n      table: 42\n      header_rows: [2]\n      data_start_row: 3\n" +
		"      columns:\n        A: {column: policy}\n        C: {column: claims}\n        D: {column: premium}\n" + sheetsNotes

	// reanchoredDataPatch answers reanchorManifest's PATCH of sheet Data: the
	// new header row has Policy over A and Premium over D, so the server
	// moves B to D and reports it.
	reanchoredDataPatch = `{"id":"` + sheetsDataID + `","sheet_name":"Data","member_dataset_id":42,"member_series_id":null,` +
		`"data_start_row":3,"header_rows":[2],"instruction_row_count":0,"row_filter":null,"sort_keys":[],"column_bindings":[
    {"id":"` + sheetsColumnA + `","target_column_index":1,"source_type":"column","source_column":"policy","run_param":null,
     "format_override":null,"origin":"user","target_label":"Policy","needs_confirmation":false,"warnings":[]},
    {"id":"` + sheetsColumnB + `","target_column_index":4,"source_type":"column","source_column":"premium","run_param":null,
     "format_override":null,"origin":"user","target_label":"Premium","needs_confirmation":false,"warnings":[]}],
 "anchor_report":{"cell_bindings":[],"column_bindings":[{"column_binding_id":"` + sheetsColumnB + `","sheet_binding_id":"` + sheetsDataID + `",
   "sheet_name":"Data","target_label":"Premium","from_column_index":2,"to_column_index":4,"outcome":"moved","reason":null}]}}`

	// reanchoredDataReport is what apply prints for reanchoredDataPatch's
	// anchor report.
	reanchoredDataReport = "\nRe-anchored bindings (1):\n  Column Data!B -> D: moved; label: Premium [" + sheetsColumnB + "]\n"
)

// A failed apply prints what it wrote before the error: the binding writes
// that succeeded and the anchor report of the sheet PATCH, which the server
// sends once. Under -o json|yaml they go to stderr.
func TestApplyDeliverableFailurePrintsWhatItWrote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fail   func(t *testing.T, s *manifestServer)
		code   int
		error  string
		writes string
	}{
		{name: "column write fails",
			fail: func(t *testing.T, s *manifestServer) {
				s.RegisterHandler("POST", dataColumnBindingsPath, func(w http.ResponseWriter, r *http.Request) {
					checkDeliverableToken(t, r)
					testutil.RespondJSON(w, http.StatusUnprocessableEntity, json.RawMessage(`{"detail":{"msg":"invalid",`+
						`"error_code":"BINDING_INVALID","error_id":"","context":{"errors":[{"binding_kind":"column_binding","binding_id":null,`+
						`"loc":["body","target_column_index"],"msg":"Target column 3 intersects merged range(s) ['C5:D5']."}]}}}`))
				})
			},
			code:   qerrors.ExitBadUsage,
			error:  `apply stopped at step 2 of 2 (POST /deliverable-sheet-bindings/` + sheetsDataID + `/column-bindings for sheet "Data" column C)`,
			writes: "binding writes: 1 sheet updated\n"},
		{name: "read back fails",
			fail: func(t *testing.T, s *manifestServer) {
				reads := 0
				s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, r *http.Request) {
					checkDeliverableToken(t, r)
					if reads++; reads > 1 {
						testutil.RespondJSON(w, http.StatusNotFound,
							json.RawMessage(`{"detail":{"msg":"Revision can not be found in this organization.","error_code":"NOT_FOUND","error_id":""}}`))
						return
					}
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(sheetsDraftTree))
				})
			},
			code:   qerrors.ExitNotFound,
			error:  "deliverable/monthly-bordereau: every write was sent, but reading it back failed: ",
			writes: "binding writes: 1 sheet updated, 1 column created\n"},
	} {
		for _, format := range []string{"table", "yaml"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				s := sheetsServer(t)
				s.answer(t, "PATCH", draftSheetBindingsPath+"/"+sheetsDataID, reanchoredDataPatch)
				tc.fail(t, s)
				path := writeManifest(t, t.TempDir(), "deliverable.yaml", reanchorManifest)
				out, errOut, err := runManifest(t, "apply", "-f", path, "--yes", "-o", format)
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
				}
				if !strings.Contains(err.Error(), tc.error) {
					t.Errorf("error =\n%s\nwant it to contain\n%s", err, tc.error)
				}
				if tc.name == "column write fails" && !strings.Contains(err.Error(),
					"spec.sheets.Data.columns.C: Target column 3 intersects merged range(s) ['C5:D5'].") {
					t.Errorf("error has no manifest column path: %v", err)
				}
				// The re-plan after the PATCH patches nothing at D and deletes
				// nothing.
				if got, want := s.writes(), []string{patchData, postColumn}; !reflect.DeepEqual(got, want) {
					t.Errorf("writes = %v, want %v", got, want)
				}
				human, structured := out, ""
				if format != "table" {
					human, structured = errOut, out
				}
				if want := tc.writes + reanchoredDataReport; human != want {
					t.Errorf("printed =\n%s\nwant\n%s", human, want)
				}
				if format == "table" {
					if errOut != "" {
						t.Errorf("stderr = %q", errOut)
					}
					return
				}
				if m := mustParseDeliverable(t, structured); m.Spec.Release != "2026.2" || m.Has("sheets") {
					t.Errorf("result manifest = %+v, want release 2026.2 without spec.sheets", m.Spec)
				}
			})
		}
	}
}

func TestApplyDeliverableColumnRefusalsUseManifestKeys(t *testing.T) {
	for _, tc := range []struct{ name, method, path, columns, want string }{
		{name: "create with numeric key", method: http.MethodPost, path: dataColumnBindingsPath,
			columns: sheetsDataColumns + "        '3': {column: claims}\n", want: "spec.sheets.Data.columns.3.column"},
		{name: "patch with lowercase key", method: http.MethodPatch, path: dataColumnBindingsPath + "/" + sheetsColumnA,
			columns: "      columns:\n        a: {column: claims}\n        B: {column: premium}\n", want: "spec.sheets.Data.columns.a.column"},
	} {
		for _, format := range []string{"table", "json", "yaml"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				s := sheetsServer(t)
				s.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, r *http.Request) {
					checkDeliverableToken(t, r)
					// Prepare's schema check passed before the server lost claims.
					testutil.RespondJSON(w, http.StatusUnprocessableEntity, json.RawMessage(`{"detail":{"msg":"invalid",`+
						`"error_code":"BINDING_INVALID","error_id":"","context":{"errors":[{"binding_kind":"column_binding",`+
						`"loc":["body","source_column"],"msg":"Column claims does not exist."},`+
						`{"binding_kind":"column_binding","loc":["body","future_key"],"msg":"New server constraint."}]}}}`))
				})
				path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(sheetsData, tc.columns, sheetsNotes))
				out, _, err := runManifest(t, "apply", "-f", path, "-o", format)
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Fatalf("exit = %d, want 2; error = %v", code, err)
				}
				for _, message := range []string{tc.want + ": Column claims does not exist.", "body.future_key: New server constraint."} {
					if !strings.Contains(err.Error(), message) {
						t.Errorf("error = %v, want %q", err, message)
					}
				}
				if out != "" {
					t.Errorf("stdout = %q, want empty after first write fails", out)
				}
				if got, want := s.writes(), []string{tc.method + " " + tc.path}; !reflect.DeepEqual(got, want) {
					t.Errorf("writes = %v, want %v", got, want)
				}
			})
		}
	}
}

// An unchanged apply reads the release's tree for -o json|yaml's export only.
func TestApplyDeliverableUnchangedReadsTheTreeOnlyForTheExport(t *testing.T) {
	for _, tc := range []struct {
		format string
		code   int
	}{
		{format: "table", code: qerrors.ExitSuccess},
		{format: "yaml", code: qerrors.ExitNotFound},
	} {
		t.Run(tc.format, func(t *testing.T) {
			s := deliverableManifestServer(t)
			s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, r *http.Request) {
				checkDeliverableToken(t, r)
				testutil.RespondJSON(w, http.StatusNotFound,
					json.RawMessage(`{"detail":{"msg":"Revision can not be found in this organization.","error_code":"NOT_FOUND","error_id":""}}`))
			})
			path := writeManifest(t, t.TempDir(), "deliverable.yaml",
				deliverableManifestHead+"monthly-bordereau\n"+liveDeliverableSpec+"  release: \"2026.2\"\n")
			out, errOut, err := runManifest(t, "apply", "-f", path, "-o", tc.format)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if tc.format == "table" && (out != "deliverable/monthly-bordereau unchanged\n" || errOut != "") {
				t.Errorf("stdout = %q, stderr = %q", out, errOut)
			}
			if got := s.writes(); len(got) != 0 {
				t.Errorf("writes = %v", got)
			}
		})
	}
}
