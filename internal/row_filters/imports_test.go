package row_filters //nolint:revive // Matches the resource domain.

import (
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const qctlModule = "github.com/qlustered/qctl"

// allowedQctlImports are the only qctl packages this package's non-test code
// may import. None of them is a resource domain.
var allowedQctlImports = []string{
	qctlModule + "/internal/api",
	qctlModule + "/internal/apierror",
	qctlModule + "/internal/client",
	qctlModule + "/internal/errors",
	qctlModule + "/internal/pkg/manifest",
	qctlModule + "/internal/pkg/piimask",
	qctlModule + "/internal/terminal",
}

// forbiddenImports returns the qctl imports of the Go file name (read from
// src when non-nil) that allowedQctlImports does not list.
func forbiddenImports(t *testing.T, name string, src any) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var forbidden []string
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("%s: import %s: %v", name, spec.Path.Value, err)
		}
		inModule := path == qctlModule || strings.HasPrefix(path, qctlModule+"/")
		if inModule && !slices.Contains(allowedQctlImports, path) {
			forbidden = append(forbidden, path)
		}
	}
	return forbidden
}

func TestPackageImportsNoResourceDomain(t *testing.T) {
	// Harness-alive control: a checker that cannot flag a domain import would
	// pass every real file below.
	control := `package row_filters

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
)
`
	if got, want := forbiddenImports(t, "control.go", control), []string{qctlModule + "/internal/dataset_rules"}; !slices.Equal(got, want) {
		t.Fatalf("control: forbiddenImports = %v, want %v", got, want)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		for _, path := range forbiddenImports(t, name, nil) {
			t.Errorf("%s imports %s; internal/row_filters may import only %s", name, path, strings.Join(allowedQctlImports, ", "))
		}
	}
	if checked == 0 {
		t.Fatal("found no non-test .go files to check")
	}
}
