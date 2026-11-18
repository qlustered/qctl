package catalog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mechanism this file guards.
//
// --request-timeout and --retries were registered in 2024 and read nowhere
// until qctl-eou. What kept them dead was not one oversight: twenty-six
// packages had each written `30 * time.Second` into a client of their own,
// and four more built an *http.Client by hand. Every one of them was correct
// in isolation and wrong together, and nothing could tell them apart from a
// package with a genuine reason to differ.
//
// So the rule is not "do not hard-code 30 seconds" -- a restated default is
// harmless now, because client.ResolveTimeout resolves it the same way. The
// rule is that a deadline must be RESOLVED rather than asserted, and that the
// generated client must never be built without a doer, because its own
// default is an *http.Client with no deadline at all. Both are properties of
// the source, so both can be checked instead of remembered.

// exemptFromResolvedTimeout lists the files allowed to build an *http.Client
// whose Timeout is not a client.ResolveTimeout call, with the reason. An
// entry that stops applying fails this test rather than lingering: see
// TestTimeoutExemptionsStillApply.
var exemptFromResolvedTimeout = map[string]string{
	// filedl sets no Timeout on purpose. An export is the whole matching set
	// of rows (capped at ten million), so no fixed overall deadline is
	// defensible -- one would abort a legitimate large download and leave the
	// user with nothing. It bounds the response headers instead.
	"internal/filedl/filedl.go": "bulk transfer: no overall deadline is defensible",
}

// goSourceFiles returns every hand-written .go file under internal/, relative
// to the repo root. Generated and test files are excluded: client.gen.go
// builds its own fallback *http.Client, which is precisely the default this
// rule exists to keep callers away from, and a test may legitimately construct
// anything.
func goSourceFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.Walk("../../internal", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".gen.go") {
			return nil
		}
		files = append(files, strings.TrimPrefix(path, "../../"))
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	// A walk that matched nothing would make this whole sweep vanish
	// silently, which is the failure mode it exists to prevent.
	if len(files) < 100 {
		t.Fatalf("walked only %d source files under internal/ -- the sweep is not running", len(files))
	}
	return files
}

// httpClientTimeouts yields the Timeout value expression of every
// `http.Client{...}` composite literal in the file, plus a bool for literals
// that set no Timeout at all.
func httpClientTimeouts(t *testing.T, path string) (values []ast.Expr, untimed int) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("../..", path), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Client" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "http" {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Timeout" {
				values = append(values, kv.Value)
				return true
			}
		}
		untimed++
		return true
	})
	return values, untimed
}

// callName renders the function of a call expression as it was written --
// "ResolveTimeout" or "client.ResolveTimeout" -- and returns "" for anything
// that is not a call.
func callName(e ast.Expr) string {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return ""
	}
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return pkg.Name + "." + fn.Sel.Name
		}
		return fn.Sel.Name
	}
	return ""
}

// TestEveryHTTPClientResolvesItsDeadline is the tripwire for the gap this
// change closed. A package that builds its own *http.Client is outside
// internal/client's reach, and a deadline written as a literal there is a
// deadline --request-timeout cannot move.
func TestEveryHTTPClientResolvesItsDeadline(t *testing.T) {
	for _, path := range goSourceFiles(t) {
		// internal/client is where the resolution is implemented, so it is
		// the one place allowed to hold the resolved value directly.
		if strings.HasPrefix(path, "internal/client/") {
			continue
		}
		values, untimed := httpClientTimeouts(t, path)

		if _, exempt := exemptFromResolvedTimeout[path]; exempt {
			continue
		}
		if untimed > 0 {
			t.Errorf("%s builds an http.Client with no Timeout at all.\n"+
				"That is a request qctl cannot ever give up on. Set\n"+
				"Timeout: client.ResolveTimeout(own), or add the file to\n"+
				"exemptFromResolvedTimeout with the reason it is a bulk transfer.", path)
		}
		for _, v := range values {
			if name := callName(v); name != "ResolveTimeout" && !strings.HasSuffix(name, ".ResolveTimeout") {
				t.Errorf("%s sets http.Client.Timeout to something other than a ResolveTimeout call.\n"+
					"A literal deadline here is one --request-timeout cannot move, which is how\n"+
					"twenty-six packages kept that flag dead. Write\n"+
					"Timeout: client.ResolveTimeout(<this package's own deadline, or 0>).", path)
			}
		}
	}
}

// TestTimeoutExemptionsStillApply is the test for the list above. An
// exemption that outlives its file, or its http.Client, is an exemption
// nobody will notice granting cover to something else later.
func TestTimeoutExemptionsStillApply(t *testing.T) {
	for path, reason := range exemptFromResolvedTimeout {
		if reason == "" {
			t.Errorf("%s is exempt with no reason recorded", path)
		}
		if _, err := os.Stat(filepath.Join("../..", path)); err != nil {
			t.Errorf("exempt file %s does not exist: %v", path, err)
			continue
		}
		values, untimed := httpClientTimeouts(t, path)
		if len(values)+untimed == 0 {
			t.Errorf("%s is exempt from the resolved-deadline rule but no longer builds an http.Client.\n"+
				"Drop the entry.", path)
		}
	}
}

// TestTheGeneratedClientIsNeverBuiltWithoutADoer. oapi-codegen's
// NewClientWithResponses falls back to a bare &http.Client{} when given no
// WithHTTPClient option -- no deadline, and no retries. cache.Fetcher did
// exactly that, so a server that accepted the connection and then said
// nothing hung `qctl config set-context` indefinitely.
func TestTheGeneratedClientIsNeverBuiltWithoutADoer(t *testing.T) {
	seen := 0
	for _, path := range goSourceFiles(t) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join("../..", path), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callName(call)
			if name != "api.NewClientWithResponses" && name != "api.NewClient" {
				return true
			}
			seen++
			for _, arg := range call.Args[1:] {
				if strings.HasSuffix(callName(arg), "WithHTTPClient") {
					return true
				}
			}
			t.Errorf("%s:%d builds the generated client with no WithHTTPClient option.\n"+
				"Its own default is &http.Client{} -- no deadline at all, and no retries.\n"+
				"Pass api.WithHTTPClient(client.NewDoer(own, transport)).",
				path, fset.Position(call.Pos()).Line)
			return true
		})
	}
	// Harness-alive control: a rename in the generated client would otherwise
	// leave this passing over nothing at all.
	if seen == 0 {
		t.Fatal("found no api.NewClientWithResponses call sites -- this sweep is checking nothing")
	}
}
