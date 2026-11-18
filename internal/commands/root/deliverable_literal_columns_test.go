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

func literalColumnsTree(value string) string {
	encoded, _ := json.Marshal(value)
	return strings.Replace(sheetsDraftTree, `"source_type":"column","source_column":"premium","run_param":null`,
		`"source_type":"literal","source_column":null,"run_param":null,"literal_value":`+string(encoded), 1)
}

func TestLiteralColumnsExportApplyAndDescribe(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		for _, value := range []string{"", "Acme", "=SUM(A1:A9)"} {
			t.Run(format+"/"+value, func(t *testing.T) {
				s := sheetsServer(t)
				answerRevisionTree(t, s.MockAPIServer, draftRevisionID, literalColumnsTree(value))
				s.RegisterHandler(http.MethodGet, deliverableTestPath+"/"+liveDeliverableID+"/revisions", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[{"id":"`+draftRevisionID+`","release":"2026.2","state":"draft"}]`))
				})
				exported, _, err := runManifest(t, "get", "deliverable", "monthly-bordereau", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				m := mustParseDeliverable(t, exported)
				column := m.Spec.Sheets["Data"].Columns["B"]
				if column.Literal == nil || *column.Literal != value || column.Column != "" || column.RunParam != "" {
					t.Fatalf("exported literal = %+v, want %q", column, value)
				}
				path := writeManifest(t, t.TempDir(), "literal."+format, exported)
				out, stderr, err := runManifest(t, "diff", "-f", path, "-o", "table")
				if err != nil || out != "" || stderr != "" {
					t.Fatalf("diff = %q / %q / %v, want unchanged", out, stderr, err)
				}
				out, _, err = runManifest(t, "apply", "-f", path, "-o", "table")
				if err != nil || !strings.Contains(out, "unchanged") || len(s.writes()) != 0 {
					t.Fatalf("apply = %s / %v, writes = %v", out, err, s.writes())
				}
				out, _, err = runManifest(t, "describe", "deliverable-revision", "monthly-bordereau", "-o", "table")
				if err != nil || !strings.Contains(out, `Column 2 (B) <- literal `) {
					t.Fatalf("describe = %s / %v", out, err)
				}
			})
		}
	}
}

func TestLiteralColumnDiffMatchesApplyBody(t *testing.T) {
	s := sheetsServer(t)
	path := writeManifest(t, t.TempDir(), "literal.yaml", sheetsManifest(sheetsData,
		"      columns:\n        A: {column: policy}\n        B: {literal: '0.25', format: '0%'}\n", sheetsNotes))
	out, _, err := runManifest(t, "diff", "-f", path, "-o", "table")
	if qerrors.GetExitCode(err) != 1 || !strings.Contains(out, `"source_type": "literal"`) ||
		!strings.Contains(out, `"literal_value": "0.25"`) || !strings.Contains(out, `"format_override": "0%"`) {
		t.Fatalf("diff = %s / %v, want literal request with format", out, err)
	}
	if len(s.writes()) != 0 {
		t.Fatalf("diff wrote %v", s.writes())
	}
	patchPath := dataColumnBindingsPath + "/" + sheetsColumnB
	tree := strings.Replace(literalColumnsTree("0.25"), `"format_override":null,"origin":"user","target_label":"Premium"`,
		`"format_override":"0%","origin":"user","target_label":"Premium"`, 1)
	var revision struct {
		Sheets []struct {
			Columns []json.RawMessage `json:"column_bindings"`
		} `json:"sheet_bindings"`
	}
	if err := json.Unmarshal([]byte(tree), &revision); err != nil {
		t.Fatal(err)
	}
	s.RegisterHandler(http.MethodPatch, patchPath, func(w http.ResponseWriter, req *http.Request) {
		s.bodies["PATCH "+patchPath] = readBody(t, req)
		answerRevisionTree(t, s.MockAPIServer, draftRevisionID, tree)
		testutil.RespondJSON(w, http.StatusOK, revision.Sheets[0].Columns[1])
	})
	_, _, err = runManifest(t, "apply", "-f", path, "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(s.bodies["PATCH "+patchPath]), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"source_type": "literal", "source_column": nil, "run_param": nil,
		"literal_value": "0.25", "format_override": "0%", "target_column_index": float64(2),
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("apply body = %+v, want %+v", body, want)
	}
}
