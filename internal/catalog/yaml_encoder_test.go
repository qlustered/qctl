package catalog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The mechanism this file guards.
//
// yaml.v3 writes a float64 with strconv.FormatFloat(f, 'g', -1, 64), so a
// whole number of a million or more comes out in exponent form: 1234567 as
// 1.234567e+06. Every number the generated client decodes into an interface{}
// field is a float64, and so is every typed float64 field, so any YAML the CLI
// prints can carry one. And jsonnum.Unmarshal leaves an integer past what
// int64 and float64 hold as a json.Number, which yaml.v3 writes as a quoted
// string. jsonnum.NewYAMLEncoder and jsonnum.MarshalYAML write the first as an
// integer and the second as a number; a yaml.v3 encoder built anywhere else
// brings both back for whatever it prints.

// yamlEncoderHome is the package that builds the yaml.v3 encoders commands
// print through.
const yamlEncoderHome = "internal/pkg/jsonnum/"

// bareYAMLEncoderAllowed names the files whose yaml.v3 encoders print nothing
// a command writes to its output.
var bareYAMLEncoderAllowed = map[string]string{
	"internal/config/config.go":          "Config.Save writes the qctl config file",
	"internal/pkg/manifest/documents.go": "SplitDocuments re-marshals the documents of an input manifest",
}

// yamlV3Paths are the import paths of yaml.v3 whose encoders this file guards:
// gopkg.in/yaml.v3 and its successors under go.yaml.in, which the module graph
// already carries.
var yamlV3Paths = map[string]bool{
	"gopkg.in/yaml.v3":   true,
	"go.yaml.in/yaml/v3": true,
	"go.yaml.in/yaml/v4": true,
}

// isYAMLEncoderFunc reports whether name is one of the yaml.v3 functions that
// encode.
func isYAMLEncoderFunc(name string) bool { return name == "NewEncoder" || name == "Marshal" }

// bareYAMLEncoders reports each reference in file to yaml.v3's NewEncoder or
// Marshal, called or not, under whatever name the file imports the package as.
func bareYAMLEncoders(fset *token.FileSet, file *ast.File) []string {
	names := map[string]bool{}
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); !yamlV3Paths[path] {
			continue
		}
		name := "yaml"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name != "_" {
			names[name] = true
		}
	}
	if len(names) == 0 {
		return nil
	}
	// notPackageNames are identifiers that name a selected field or method, or
	// declare a method, rather than refer to a package-level name.
	notPackageNames := map[*ast.Ident]bool{}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Recv != nil {
				notPackageNames[x.Name] = true
			}
		case *ast.SelectorExpr:
			notPackageNames[x.Sel] = true
			if pkg, ok := x.X.(*ast.Ident); ok && names[pkg.Name] && isYAMLEncoderFunc(x.Sel.Name) {
				found = append(found, x.Sel.Name+" at "+fset.Position(x.Pos()).String())
			}
		case *ast.Ident:
			// A dot import makes NewEncoder and Marshal bare identifiers.
			if names["."] && !notPackageNames[x] && isYAMLEncoderFunc(x.Name) {
				found = append(found, x.Name+" at "+fset.Position(x.Pos()).String())
			}
		}
		return true
	})
	return found
}

// parseGoFile parses path, relative to the repository root.
func parseGoFile(t *testing.T, fset *token.FileSet, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(fset, filepath.Join("../..", path), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

// TestNoBareYAMLEncoder is the tripwire for a yaml.v3 encoder built outside
// jsonnum.
func TestNoBareYAMLEncoder(t *testing.T) {
	fset := token.NewFileSet()
	for _, path := range goSourceFiles(t) {
		if strings.HasPrefix(path, yamlEncoderHome) || strings.Contains(path, "/testdata/") {
			continue
		}
		found := bareYAMLEncoders(fset, parseGoFile(t, fset, path))
		if _, ok := bareYAMLEncoderAllowed[path]; ok {
			if len(found) == 0 {
				t.Errorf("%s is allowed a yaml.v3 encoder but no longer builds one; drop it from bareYAMLEncoderAllowed", path)
			}
			continue
		}
		for _, call := range found {
			t.Errorf("%s calls %s.\n"+
				"yaml.v3 prints a whole float64 of a million or more in exponent form\n"+
				"(1234567 as 1.234567e+06). Use jsonnum.NewYAMLEncoder or jsonnum.MarshalYAML,\n"+
				"which take the same arguments and print it as an integer.", path, call)
		}
	}

	// Harness-alive control: the sanctioned encoders must still match, or a
	// rename in the detector would leave this passing over nothing at all.
	home := filepath.Join(yamlEncoderHome, "yaml.go")
	if got := bareYAMLEncoders(fset, parseGoFile(t, fset, home)); len(got) != 2 {
		t.Fatalf("expected the detector to find yaml.NewEncoder and yaml.Marshal in %s, found %v -- this sweep is checking nothing", home, got)
	}
}

// TestBareYAMLEncoderDetectorFollowsTheImportName: an aliased or dot import of
// yaml.v3, a successor's import path, and a reference that is not a call are
// still caught; another package's NewEncoder, yaml.v3's other names, and a
// method that happens to be called Marshal are not.
func TestBareYAMLEncoderDetectorFollowsTheImportName(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want int
	}{
		{`package p; import y "gopkg.in/yaml.v3"; var _, _ = y.Marshal(1)`, 1},
		{`package p; import . "gopkg.in/yaml.v3"; var _ = NewEncoder(nil)`, 1},
		{`package p; import (. "gopkg.in/yaml.v3"; "encoding/json"); var _ = json.NewEncoder(nil); var _ Node`, 0},
		{`package p; import "gopkg.in/yaml.v3"; var _ = yaml.NewEncoder(nil)`, 1},
		{`package p; import "encoding/json"; var _ = json.NewEncoder(nil)`, 0},
		{`package p; import ("gopkg.in/yaml.v3"; "encoding/json"); var _ = json.NewEncoder(nil); var _ yaml.Node`, 0},
		{`package p; import "gopkg.in/yaml.v3"; var marshal = yaml.Marshal`, 1},
		{`package p; import "gopkg.in/yaml.v3"; var _ yaml.Marshaler; var _ = yaml.NewDecoder(nil)`, 0},
		{`package p; import "go.yaml.in/yaml/v4"; var _, _ = yaml.Marshal(1)`, 1},
		{`package p; import yaml "go.yaml.in/yaml/v3"; var _ = yaml.NewEncoder(nil)`, 1},
		{`package p; import . "gopkg.in/yaml.v3"; var f = Marshal; type t struct{}; func (t) Marshal() {}; var _ = t{}.Marshal`, 1},
	} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "src.go", tc.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.src, err)
		}
		if got := bareYAMLEncoders(fset, file); len(got) != tc.want {
			t.Errorf("%q: found %v, want %d", tc.src, got, tc.want)
		}
	}
}
