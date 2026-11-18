package deliverables

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// literalText returns value as a YAML double-quoted scalar, escaping control
// characters and U+007F.
func literalText(value string) string {
	body, _ := json.Marshal(value)
	return strings.ReplaceAll(string(body), "\x7f", `\u007f`)
}

func TestLiteralsRefuseTextAnExcelCellCannotHold(t *testing.T) {
	documents := map[string]func(string) string{
		"spec.sheets.Data.columns.A.literal": func(text string) string {
			return validSheet("columns:", "  A: {literal: "+text+"}")
		},
		"spec.cells.Summary!A1: literal": func(text string) string {
			return deliverableManifestHeader + deliverableManifestBasics + "  release: v3\n  cells: {Summary!A1: {literal: " + text + "}}\n"
		},
	}
	for path, document := range documents {
		for _, tc := range []struct{ name, value, want string }{
			{"nul", "a\x00b", " holds control character U+0000 at character 2;"},
			{"bell", "\a", " holds control character U+0007 at character 1;"},
			{"vertical tab", "line\vline", " holds control character U+000B at character 5;"},
			{"form feed", "page\f", " holds control character U+000C at character 5;"},
			{"unit separator", "日本\x1f", " holds control character U+001F at character 3;"},
			{"one over", strings.Repeat("x", excelCellLimit+1), " is 32768 characters long; an Excel cell holds at most 32767"},
			{"multibyte over", strings.Repeat("é", excelCellLimit+1), " is 32768 characters long;"},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				_, err := ParseManifest([]byte(document(literalText(tc.value))))
				if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), path+tc.want) {
					t.Fatalf("error = %.200v (exit %d), want exit 2 with %q", err, qerrors.GetExitCode(err), path+tc.want)
				}
			})
		}
		for _, value := range []string{
			"", "tab\tline\nreturn\r", "Résumé 日本語", "delete\x7f", strings.Repeat("x", excelCellLimit), strings.Repeat("日", excelCellLimit),
		} {
			if _, err := ParseManifest([]byte(document(literalText(value)))); err != nil {
				t.Errorf("%s: literal of %d characters refused: %.200v", path, len([]rune(value)), err)
			}
		}
	}
}

// requestValidationRefusal is FastAPI's own 422 for a too-long literal_value.
const requestValidationRefusal = `{"detail":[{"type":"string_too_long","loc":["body","literal_value"],` +
	`"msg":"String should have at most 32767 characters","ctx":{"max_length":32767}}]}`

func TestApplyMapsRequestValidationRefusalToTheLiteral(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
		manifest         func(*testing.T) *Manifest
	}{
		{
			name: "column", path: bpDataColumns,
			manifest: func(t *testing.T) *Manifest {
				return bpManifest(t, "", bpData(`columns: {A: {literal: "Acme"}}`))
			},
			want: "create deliverable column binding failed: 1 binding issue(s):\n" +
				"  - spec.sheets.Data.columns.A.literal: String should have at most 32767 characters",
		},
		{
			name: "cell", path: pathRevisions + revV3 + pathCellBindings,
			manifest: func(t *testing.T) *Manifest { return cellManifest(t, `{Summary!B1: {literal: "Acme"}}`) },
			want: "create deliverable cell binding failed: 1 binding issue(s):\n" +
				"  - spec.cells.Summary!B1.literal: String should have at most 32767 characters",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serveApply(t, planDeliverable(nil), enabledV2, draftV3)
			s.trees(revV3, bpTree(bpSheet(bpDataID, "Data", 5, []int{1}, 2)))
			s.fail(http.MethodPost, tc.path, http.StatusUnprocessableEntity, requestValidationRefusal)
			p, n := s.prepare(tc.manifest(t), ".")
			r, err := s.client().Apply("token", p)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || r != nil {
				t.Fatalf("exit = %d, result = %+v, error = %v", code, r, err)
			}
			if err.Error() != tc.want {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("lost API metadata: %+v", apiErr)
			}
			if got := s.since(n); len(got) != 1 || !strings.HasPrefix(got[0], "POST "+tc.path+" ") {
				t.Errorf("requests = %v, want only the failed POST", got)
			}
		})
	}
}

func TestDetailIssuesReadsOnlyAListedDetail(t *testing.T) {
	issues := detailIssues([]byte(`{"detail":[{"loc":["body","header_rows",1],"msg":"bad"},{"msg":"no loc"}]}`))
	if len(issues) != 2 || strings.Join(*issues[0].Loc, ".") != "body.header_rows.1" || issues[0].Msg != "bad" ||
		len(*issues[1].Loc) != 0 || issues[1].Msg != "no loc" {
		t.Fatalf("issues = %+v", issues)
	}
	for _, body := range []string{`{"detail":{"msg":"x","error_code":"BINDING_INVALID"}}`, `{"detail":"x"}`, `not json`, `{}`} {
		if issues := detailIssues([]byte(body)); len(issues) != 0 {
			t.Errorf("%s: issues = %+v, want none", body, issues)
		}
	}
}
