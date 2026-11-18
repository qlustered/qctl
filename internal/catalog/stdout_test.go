package catalog_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommandOutputUsesCobraWriter catches direct writes that bypass SetOut.
func TestCommandOutputUsesCobraWriter(t *testing.T) {
	for _, dir := range []string{"../commands", "../cmdutil", "../../cmd"} {
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if pkg.Name == "fmt" && (sel.Sel.Name == "Print" || sel.Sel.Name == "Printf" || sel.Sel.Name == "Println") {
					t.Errorf("%s: use an explicit command writer instead of fmt.%s", path, sel.Sel.Name)
				}
				if pkg.Name == "os" && sel.Sel.Name == "Stdout" {
					t.Errorf("%s: use cmd.OutOrStdout() instead of os.Stdout", path)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
