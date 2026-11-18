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

// internalRoot is internal/ as seen from internal/catalog.
const internalRoot = ".."

// skippedScanDirs are directories the source scans below walk past.
//
// api is the oapi-codegen output: 3MB of generated transport that contains a
// selector call literally named SwitchOrganizationIdp (client.gen.go
// SwitchOrganizationIdpWithResponse, ClientWithResponses delegating to Client).
// It is the wire layer, not a caller, and parsing it costs more than the rest
// of the tree combined.
var skippedScanDirs = map[string]bool{
	"api":      true,
	"testdata": true,
}

// scanInternalPackages parses every non-test .go file under internal/ and
// hands each one to visit, keyed by its package directory relative to
// internal/ ("commands/auth", "datasets", ...).
//
// Test files are skipped on purpose. A test may legitimately construct any of
// the symbols pinned below in order to assert on it -- internal/cmdutil's
// flags_test.go builds a qerrors.NewNotFound to check error wrapping -- and
// the guarantees here are about what the shipped CLI can do.
func scanInternalPackages(t *testing.T, visit func(pkgDir string, file *ast.File)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedScanDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		rel, relErr := filepath.Rel(internalRoot, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		visit(filepath.ToSlash(rel), parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("scanning internal/: %v", err)
	}
}

// callsSelector reports whether any call in file is `x.<name>(...)`.
func callsSelector(file *ast.File, name string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == name {
			found = true
		}
		return true
	})
	return found
}

// TestEveryClientSideNotFoundCarriesTheScopeNote checks the scope-note policy of not-found errors.
func TestEveryClientSideNotFoundCarriesTheScopeNote(t *testing.T) {
	// True requires an organization note; false names a local resource.
	want := map[string]bool{
		"cmdutil":               true,  // resolveCloudSourceByName: no cloud source of that name on the table
		"concepts":              true,  // Resolve: no concept by that reference in this organization
		"datasets":              true,  // ResolveDeletedByName, and ResolveDataset's two name misses
		"dataset_kinds":         true,  // Prepare: a declared concept slug is absent from this organization
		"dataset_rules":         true,  // FindRuleInList: no rule by that reference on the table
		"email_intake":          true,  // ResolveSender: no assigned sender by that email or user id
		"deliverables":          true,  // Resolve: no deliverable by that reference in this organization
		"commands/delete":       true,  // deliverable revision does not belong to the named deliverable
		"commands/get":          true,  // get change-review-policy: no organization policy, or none in force for the table
		"commands/deliverable":  true,  // revision parent or bound sheet does not match
		"rule_families":         true,  // ResolveSlug: no family by that slug in this organization
		"table_series":          true,  // ResolveID: no exact series name in this organization
		"commands/auth":         true,  // auth sessions revoke: no active session by that name or ID
		"commands/data_review":  true,  // bulkOutcome: every bulk-decision skip is NOT_FOUND
		"commands/diff":         true,  // sourceDatasets: no table named a linked table source
		"commands/describe":     true,  // deliverable revision detail does not belong to the named deliverable
		"commands/inspect":      true,  // deliverable preview revision does not belong to the named deliverable
		"commands/rekey":        true,  // resolveSourceIDs: no table named a linked table source
		"commands/review":       true,  // mapDryRunJobLookupError: dry-run job not found in this org
		"commands/rows":         true,  // reportMoveResult: every requested row ID is missing
		"commands/table_series": true,  // runLock: no exact period key in the selected series
		"user_submitted":        true,  // submitAllFailedError and ReportRetract
		"config":                false, // saved contexts are local
		"commands/config":       false, // saved contexts are local
		"commands/cache":        false, // aliases are local and server-scoped
	}

	got := map[string]bool{}
	scanInternalPackages(t, func(pkgDir string, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !isClientSideNotFoundConstructor(sel, call) {
				return true
			}
			got[pkgDir] = true
			if want[pkgDir] && !argsUseScopeNote(call) {
				t.Errorf("%s: a NewNotFound message does not pass through apierror.WithScopeNote.\n"+
					"Every organization-scoped not-found must name the organization it looked in,\n"+
					"the same way apierror.buildHTTPError names it on the 403s and 404s that reach it.",
					pkgDir)
			}
			if scoped, known := want[pkgDir]; known && !scoped && argsUseScopeNote(call) {
				t.Errorf("%s: a local not-found error must not carry an organization scope note", pkgDir)
			}
			return true
		})
	})

	for pkg := range want {
		if !got[pkg] {
			t.Errorf("expected a client-side NewNotFound in %s and found none -- "+
				"if it moved, move this entry with it", pkg)
		}
	}
	for pkg := range got {
		if _, known := want[pkg]; !known {
			t.Errorf("new client-side NewNotFound in %s. Add %q to this test's want set, "+
				"with true for organization-scoped resources or false for local resources.", pkg, pkg)
		}
	}
}

func isClientSideNotFoundConstructor(sel *ast.SelectorExpr, call *ast.CallExpr) bool {
	if sel.Sel.Name == "NewNotFound" {
		return true
	}
	if (sel.Sel.Name != "New" && sel.Sel.Name != "Wrap") || len(call.Args) == 0 {
		return false
	}
	exitCode, ok := call.Args[0].(*ast.SelectorExpr)
	return ok && exitCode.Sel.Name == "ExitNotFound"
}

func TestClientSideNotFoundConstructorForms(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{expr: `qerrors.NewNotFound("missing")`, want: true},
		{expr: `qerrors.New(qerrors.ExitNotFound, "missing")`, want: true},
		{expr: `qerrors.Wrap(qerrors.ExitNotFound, err, "missing")`, want: true},
		{expr: `qerrors.New(qerrors.ExitBadUsage, "bad input")`, want: false},
		{expr: `fmt.Errorf("missing")`, want: false},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			expr, err := parser.ParseExpr(tc.expr)
			if err != nil {
				t.Fatalf("ParseExpr: %v", err)
			}
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				t.Fatalf("expression parsed as %T, want *ast.CallExpr", expr)
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				t.Fatalf("callee parsed as %T, want *ast.SelectorExpr", call.Fun)
			}
			if got := isClientSideNotFoundConstructor(sel, call); got != tc.want {
				t.Errorf("isClientSideNotFoundConstructor() = %t, want %t", got, tc.want)
			}
		})
	}
}

// argsUseScopeNote reports whether apierror.WithScopeNote appears anywhere in
// the call's arguments. It looks at the whole argument subtree rather than
// only the top-level expression, so both
// NewNotFound(apierror.WithScopeNote(fmt.Sprintf(...))) and a builder form
// satisfy it.
func argsUseScopeNote(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		found := false
		ast.Inspect(arg, func(n ast.Node) bool {
			inner, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := inner.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "WithScopeNote" {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// TestOnlyDeliberateCommandsCanChangeOrganization pins the network half of
// the guarantee.
//
// qctl has no cross-organization fallback: no error path re-resolves the
// organization, retries in another one, or re-authenticates. What this test
// checks structurally is that the four code paths which ASK THE SERVER to
// enumerate or change an organization stay confined to the one PACKAGE that
// holds the command meant to use them, so a fallback cannot be added
// quietly. Package, not command, is the resolution an import graph gives:
// commands/auth also holds login, logout and me, so a NEW command added
// inside commands/auth could reach SwitchOrganizationIdp and this test would
// pass. The `why` field records which command each symbol is for. The
// request-boundary tests in internal/commands/root only cover the commands
// they name.
//
// Out of scope, and NOT a gap: internal/commands/config also changes which
// organization qctl acts in -- `config set-context --org` writes
// ctx.Organization (set_context.go) and `config use-context` switches to a
// context with its own. Both DO reach the network: use_context.go's
// newUseContextCommand fetches the target server's OpenAPI spec, and
// set_context.go's updateContextServer hits /api/version, that spec and
// /api/auth/config behind `--server`. What keeps
// them out of this test is that those URLs are server-level -- no
// organization in the path, no bearer token. The organization half of both
// commands never leaves the machine: set-context resolves `--org` against
// the context's cached org list (resolveOrgInput, set_context.go), and
// use-context adopts whatever organization the target context already
// stores. So neither can be an error path's cross-organization retry, which
// is what this test exists to forbid.
// `guide authenticate` names both commands to the reader.
//
// Deliberately NOT pinned: auth.Client.GetMe. It is a whoami that happens to
// carry the caller's active organization ids, and three commands unrelated to
// auth (upload file, delete file, undelete file) call it for the user's ID,
// alongside auth's own call sites. Claiming it is auth-only would be false.
func TestOnlyDeliberateCommandsCanChangeOrganization(t *testing.T) {
	const orgsPkg = `"github.com/qlustered/qctl/internal/orgs"`

	// symbol -> the one package allowed to reach it, and why.
	pinned := map[string]struct {
		allowed string
		why     string
	}{
		"SwitchOrganizationIdp": {"commands/auth", "qctl auth switch-org"},
		"ExchangeForCLIToken":   {"commands/auth", "qctl auth login"},
		"ExchangeForOpsToken":   {"commands/auth", "qctl auth switch-org"},
	}

	callers := map[string]map[string]bool{}
	for symbol := range pinned {
		callers[symbol] = map[string]bool{}
	}
	orgsImporters := map[string]bool{}

	scanInternalPackages(t, func(pkgDir string, file *ast.File) {
		// internal/auth DEFINES the three methods; a definition is a
		// FuncDecl, not a selector call, so it never appears here. Its own
		// delegation to the generated client is
		// SwitchOrganizationIdpWithResponse -- a different name.
		for symbol := range pinned {
			if callsSelector(file, symbol) {
				callers[symbol][pkgDir] = true
			}
		}
		for _, imp := range file.Imports {
			if imp.Path.Value == orgsPkg {
				orgsImporters[pkgDir] = true
			}
		}
	})

	for symbol, want := range pinned {
		got := sortedKeys(callers[symbol])
		if len(got) != 1 || got[0] != want.allowed {
			t.Errorf("auth.Client.%s is called from %v, want only %q (%s).\n"+
				"A new caller is a new way for qctl to change organizations on its own; "+
				"move an existing auth flow back into %q, or update this allowlist only for a deliberate new command.",
				symbol, got, want.allowed, want.why, want.allowed)
		}
	}

	got := sortedKeys(orgsImporters)
	if len(got) != 1 || got[0] != "commands/get" {
		t.Errorf("internal/orgs is imported by %v, want only \"commands/get\" (qctl get orgs).\n"+
			"Enumerating organizations is a thing a user asks for, never something an\n"+
			"error path does; remove the import unless it is part of the deliberate get-orgs command.", got)
	}
}
