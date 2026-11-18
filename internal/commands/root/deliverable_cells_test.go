package root

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

const (
	rootCellID    = "5d000000-0000-4000-8000-000000000001"
	rootCellsPath = "/api/orgs/" + scopeTestOrgID + "/deliverable-revisions/" + draftRevisionID + "/cell-bindings"
	rootCell      = `{"id":"` + rootCellID + `","sheet_name":"Summary","cell_ref":"B1","source_type":"literal","literal_value":"Report","run_param":null,"format_override":null,"needs_confirmation":false}`
)

func cellsServer(t *testing.T) *manifestServer {
	t.Helper()
	s := sheetsServer(t)
	tree := strings.Replace(sheetsDraftTree, `"cell_bindings":[]`, `"cell_bindings":[`+rootCell+`]`, 1)
	answerRevisionTree(t, s.MockAPIServer, draftRevisionID, tree)
	s.RegisterHandler("DELETE", rootCellsPath+"/"+rootCellID, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		w.WriteHeader(http.StatusNoContent)
	})
	s.answer(t, "PATCH", rootCellsPath+"/"+rootCellID, rootCell)
	s.answer(t, "POST", rootCellsPath, rootCell)
	return s
}

func rootCellsManifest(cells string) string {
	return deliverableManifestHead + "monthly-bordereau\n" + liveDeliverableSpec + "  release: '2026.2'\n  cells: " + cells + "\n"
}

func TestDeliverableCellRemovalConfirmation(t *testing.T) {
	requireNonTTY(t)
	for _, tc := range []struct {
		name, format        string
		yes, protect, named bool
		code                int
	}{
		{name: "without yes", code: 2},
		{name: "confirmed", yes: true},
		{name: "structured without yes", format: "json", code: 2},
		{name: "structured confirmed", format: "yaml", yes: true},
		{name: "protected context", protect: true, yes: true, code: 2},
		{name: "named protected context", protect: true, named: true, yes: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := cellsServer(t)
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if tc.protect {
				cfg.Contexts[cfg.CurrentContext].Protected = nil
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
			}
			path := writeManifest(t, t.TempDir(), "cells.yaml", rootCellsManifest("{}"))
			args := []string{"apply", "-f", path}
			if tc.yes {
				args = append(args, "--yes")
			}
			if tc.format != "" {
				args = append(args, "-o", tc.format)
			}
			if tc.named {
				args = append(args, "--context", cfg.CurrentContext)
			}
			var out string
			if tc.protect {
				out, err = runRootWithContextFlag(t, args...)
			} else {
				out, _, err = runManifest(t, args...)
			}
			if qerrors.GetExitCode(err) != tc.code {
				t.Fatalf("error = %v, want exit %d", err, tc.code)
			}
			var want []string
			if tc.code == 0 {
				want = []string{"DELETE " + rootCellsPath + "/" + rootCellID}
			}
			if !reflect.DeepEqual(s.writes(), want) {
				t.Fatalf("writes = %v, want %v", s.writes(), want)
			}
			if tc.code == 0 && tc.format == "" && !strings.Contains(out, "1 cell deleted") {
				t.Fatalf("result = %q", out)
			}
		})
	}
}

func TestDeliverableCellDiffAndSourceChange(t *testing.T) {
	s := cellsServer(t)
	path := writeManifest(t, t.TempDir(), "cells.yaml", rootCellsManifest("{'Summary!$b$01': {run_param: period}}"))
	out, _, err := runManifest(t, "diff", "-f", path)
	if qerrors.GetExitCode(err) != 1 || !strings.Contains(out, "PATCH /deliverable-revisions/"+draftRevisionID+"/cell-bindings/"+rootCellID) ||
		!strings.Contains(out, `"literal_value": null`) || !strings.Contains(out, `"cell_ref": "B1"`) {
		t.Fatalf("diff = %q, %v", out, err)
	}
	if len(s.writes()) != 0 {
		t.Fatalf("diff wrote: %v", s.writes())
	}
	out, _, err = runManifest(t, "apply", "-f", path)
	if err != nil || !strings.Contains(out, "1 cell updated") {
		t.Fatalf("apply = %q, %v", out, err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s.bodies["PATCH "+rootCellsPath+"/"+rootCellID]), &body); err != nil {
		t.Fatal(err)
	}
	if string(body["run_param"]) != `"period"` || string(body["literal_value"]) != "null" {
		t.Fatalf("body = %v", body)
	}
}
