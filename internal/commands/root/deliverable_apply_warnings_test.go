package root

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestApplyDeliverablePrintsColumnWriteWarnings(t *testing.T) {
	const (
		formulaWarning = `[{"binding_kind":"column_binding","loc":["body","target_column_index"],` +
			`"msg":"Skeleton formulas will be overwritten."}]`
		createWarnings = `[{"binding_kind":"column_binding","loc":["body","target_column_index"],` +
			`"msg":"Skeleton formulas will be overwritten."},{"binding_kind":"column_binding","msg":"Check\n\tformat."}]`
		patchWarningText  = "Warning: sheet \"Data\" column B: body.target_column_index: Skeleton formulas will be overwritten.\n"
		createWarningText = "Warning: sheet \"Data\" column C: body.target_column_index: Skeleton formulas will be overwritten.\n" +
			"Warning: sheet \"Data\" column C: Check\\n\\tformat.\n"
		notesWarningText = "Warning: sheet \"Notes\" column C: body.target_column_index: Skeleton formulas will be overwritten.\n"
	)
	response := func(id string, column int, warnings string) string {
		return fmt.Sprintf(`{"id":%q,"target_column_index":%d,"source_type":"run_param","run_param":"period",`+
			`"source_column":null,"format_override":null,"origin":"user","target_label":null,"needs_confirmation":false,"warnings":%s}`,
			id, column, warnings)
	}
	for _, tc := range []struct {
		name, failure string
		noWarnings    bool
		code          int
		writes        string
		warnings      string
	}{
		{name: "success", writes: "binding writes: 1 column updated, 2 columns created\n",
			warnings: patchWarningText + createWarningText + notesWarningText},
		{name: "no warnings", noWarnings: true, writes: "binding writes: 1 column updated, 2 columns created\n"},
		{name: "later write fails", failure: "write", code: qerrors.ExitBadUsage,
			writes: "binding writes: 1 column updated, 1 column created\n", warnings: patchWarningText + createWarningText},
		{name: "read back fails", failure: "read", code: qerrors.ExitNotFound,
			writes:   "binding writes: 1 column updated, 2 columns created\n",
			warnings: patchWarningText + createWarningText + notesWarningText},
	} {
		for _, format := range []string{"table", "json", "yaml"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				s := sheetsServer(t)
				patchWarnings, postWarnings := formulaWarning, createWarnings
				if tc.noWarnings {
					patchWarnings, postWarnings = "null", "[]"
				}
				patchPath := dataColumnBindingsPath + "/" + sheetsColumnB
				notesPath := "/api/orgs/" + scopeTestOrgID + "/deliverable-sheet-bindings/" + sheetsNotesID + "/column-bindings"
				s.answer(t, "PATCH", patchPath, response(sheetsColumnB, 2, patchWarnings))
				s.answer(t, "POST", dataColumnBindingsPath, response(sheetsColumnC, 3, postWarnings))
				s.answer(t, "POST", notesPath, response(sheetsColumnC, 3, patchWarnings))
				if tc.failure == "write" {
					s.RegisterHandler("POST", notesPath, func(w http.ResponseWriter, r *http.Request) {
						checkDeliverableToken(t, r)
						testutil.RespondJSON(w, http.StatusUnprocessableEntity, json.RawMessage(`{"detail":{"msg":"Invalid column",`+
							`"error_code":"BINDING_INVALID","error_id":""}}`))
					})
				}
				if tc.failure == "read" {
					reads := 0
					s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, r *http.Request) {
						checkDeliverableToken(t, r)
						if reads++; reads > 1 {
							testutil.RespondJSON(w, http.StatusNotFound, json.RawMessage(`{"detail":{"msg":"Revision not found",`+
								`"error_code":"NOT_FOUND","error_id":""}}`))
							return
						}
						testutil.RespondJSON(w, http.StatusOK, json.RawMessage(sheetsDraftTree))
					})
				}
				path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(sheetsData,
					"      columns:\n        A: {column: policy}\n        B: {run_param: period}\n        C: {run_param: period}\n",
					sheetsNotes, "      columns:\n        C: {run_param: period}\n"))
				out, errOut, err := runManifest(t, "apply", "-f", path, "-o", format)
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
				}
				want := tc.writes + tc.warnings
				if tc.failure == "" {
					want = "deliverable/monthly-bordereau updated\n" + want
				}
				human := out
				if format != "table" {
					human = errOut
					m := mustParseDeliverable(t, out)
					if m.Spec.Release != "2026.2" || m.Has("sheets") != (tc.failure == "") {
						t.Errorf("result manifest = %+v", m.Spec)
					}
				} else if errOut != "" {
					t.Errorf("stderr = %q, want empty", errOut)
				}
				if human != want {
					t.Errorf("output =\n%s\nwant\n%s", human, want)
				}
				if strings.Contains(out, "Warning:") && format != "table" {
					t.Errorf("warning leaked to structured stdout: %s", out)
				}
				if got, want := s.writes(), []string{"PATCH " + patchPath, postColumn, "POST " + notesPath}; !reflect.DeepEqual(got, want) {
					t.Errorf("writes = %v, want %v", got, want)
				}
			})
		}
	}
}
