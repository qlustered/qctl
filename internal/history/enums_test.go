package history

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"
)

// generatedValues returns every string constant of the named type declared in
// the generated client. A RENAMED enum value fails to build, because each
// vocabulary below is built from api.* constants; this is what catches a value
// ADDED to the spec that nobody added here.
func generatedValues(t *testing.T, typeName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../api/client.gen.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse client.gen.go: %v", err)
	}
	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != typeName || len(vs.Values) == 0 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", lit.Value, err)
			}
			out = append(out, value)
		}
	}
	return out
}

func assertCoversEnum(t *testing.T, typeName string, got []string, minimum int) {
	t.Helper()
	want := generatedValues(t, typeName)
	if len(want) < minimum {
		t.Fatalf("parsed only %d %s constants; the parse is broken", len(want), typeName)
	}
	sort.Strings(want)
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	if len(sorted) != len(want) {
		t.Fatalf("%s: have %d values, client.gen.go declares %d\n got: %v\nwant: %v",
			typeName, len(sorted), len(want), sorted, want)
	}
	for i := range want {
		if sorted[i] != want[i] {
			t.Errorf("%s drifted from client.gen.go at %d: got %q, want %q", typeName, i, sorted[i], want[i])
		}
	}
}

func TestModelNamesCoverTheGeneratedEnum(t *testing.T) {
	assertCoversEnum(t, "ModelsWithHistory", ModelNames(), 10)
}

func TestOperationNamesCoverTheGeneratedEnum(t *testing.T) {
	assertCoversEnum(t, "SimpleHistoryAction", OperationNames(), 6)
}

func TestUndoStatusNamesCoverTheGeneratedEnum(t *testing.T) {
	assertCoversEnum(t, "HistoryUndoStatus", UndoStatusNames(), 3)
}

func TestOrderByNamesCoverTheGeneratedEnum(t *testing.T) {
	assertCoversEnum(t, "HistoryOrderBy", OrderByNames(), 7)
}
