package catalog_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/resources"
)

// TestPageSchemasExistInGeneratedClient closes the hole that let
// alert.md, cloud-source.md, destination.md and file.md all name OpenAPI
// components no spec has ever defined.
//
// TestFrontmatterReferences and TestSchemaMappingIsHonoredByPages check the
// page against schema.SchemaMapping and the map against the page. Both passed
// for years because page and map were wrong *in agreement*: `qctl explain
// alert` printed `warning: live schema unavailable (schema
// "AlertDetailedSchema" not found)` and no FIELDS section, and CI was green.
// So this test checks a third party neither of them consults.
//
// It parses internal/api/client.gen.go rather than build/openapi.raw.json:
// that spec file is gitignored (.gitignore:39) and absent in CI. Reproducing
// the bug by hand needs a live API — with localhost:8000 down, the broken and
// the fixed command print the same warning line for different reasons — which
// is exactly why the check has to be static.
//
// Caveat, if this ever fires on a name you believe is real: oapi-codegen runs
// with skip-prune: false (internal/api/oapi-codegen.yaml), so a component
// referenced by no operation is dropped from the generated client even though
// the spec defines it. If that is the case, point the page at a component the
// client does use — `explain` should document what qctl actually receives.
func TestPageSchemasExistInGeneratedClient(t *testing.T) {
	declared := generatedTypeNames(t)
	if len(declared) < 100 {
		t.Fatalf("parsed only %d type declarations from the generated client; the parse is broken", len(declared))
	}

	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	for _, p := range pages {
		ns, ref, ok := p.SchemaRef()
		if !ok || ns != resources.SchemaNamespaceOpenAPI {
			continue
		}
		if !declared[ref] {
			t.Errorf("%s.md declares schema openapi:%s, but internal/api/client.gen.go declares no such type.\n"+
				"`qctl explain %s` prints \"live schema unavailable\" and no FIELDS section.\n"+
				"Fix the page and the matching entry in schema.SchemaMapping together.",
				p.Name, ref, p.Name)
		}
	}
}

// generatedTypeNames returns every top-level type name in the generated client.
func generatedTypeNames(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../api/client.gen.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse client.gen.go: %v", err)
	}
	names := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				names[ts.Name.Name] = true
			}
		}
	}
	return names
}

// generatedStructFields maps each struct type the generated client source src
// declares to the JSON names and named types of its tagged fields.
func generatedStructFields(t *testing.T, src []byte) map[string]map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "client.gen.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse client.gen.go: %v", err)
	}
	out := map[string]map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			names := map[string]string{}
			for _, f := range st.Fields.List {
				if f.Tag == nil {
					continue
				}
				tag, err := strconv.Unquote(f.Tag.Value)
				if err != nil {
					t.Fatalf("%s: unquote tag %s: %v", ts.Name.Name, f.Tag.Value, err)
				}
				name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
				if name != "" && name != "-" {
					fieldType := f.Type
					if pointer, ok := fieldType.(*ast.StarExpr); ok {
						fieldType = pointer.X
					}
					if ident, ok := fieldType.(*ast.Ident); ok {
						names[name] = ident.Name
					} else {
						names[name] = ""
					}
				}
			}
			out[ts.Name.Name] = names
		}
	}
	return out
}
