package catalog

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// The mechanism this file guards.
//
// encoding/json decodes a number into an empty interface as a float64. yaml.v3
// prints a float64 of seven or more integer digits in exponent form (1234567
// as 1.234567e+06), and any integer past 2^53 has already been rounded by the
// time anything prints it. jsonnum.Unmarshal decodes with UseNumber, so no
// integer literal passes through a float64.
//
// TestNoHandRolledJSONToYAML only sees a function that decodes and encodes in
// one body. A decode that hands its tree to something else -- a RawResponse, a
// Printer, an error message -- passes it, and so does one that never names an
// interface at the call: the interface is inside a struct field's type, or the
// target is a parameter typed any. So this sweep asks the type checker what
// each target can hold.

// jsonnumHome is the package whose decode is the sanctioned one.
const jsonnumHome = "internal/pkg/jsonnum/"

// genericDecodeExemptDirs are the directories the sweep does not report:
// jsonnum itself, and test helpers that never run in the shipped CLI.
var genericDecodeExemptDirs = []string{jsonnumHome, "internal/testutil/"}

// genericDecode is one decode whose target can hold an empty interface, or
// one use of a decode function as a value, which hides its target.
type genericDecode struct {
	file       string     // relative to the repository root
	fn         string     // the enclosing top-level declaration
	call       string     // the decode function, as the failure message writes it
	targetType types.Type // nil for a function value
	target     string     // targetType as the failure message writes it
	path       string     // where under the target the empty interface is
}

// key identifies d without saying where the interface is.
func (d genericDecode) key() string {
	if d.targetType == nil {
		return fmt.Sprintf("%s %s: %s used as a function value", d.file, d.fn, d.call)
	}
	return fmt.Sprintf("%s %s: %s into %s", d.file, d.fn, d.call, d.target)
}

func (d genericDecode) String() string {
	switch {
	case d.targetType == nil:
		return d.key() + ", which hides its target"
	case d.path == "":
		return d.key() + ", itself an empty interface"
	default:
		return d.key() + ", which reaches an empty interface at " + d.path
	}
}

// loadForDecodeSweep type-checks the packages matching patterns from source,
// failing the test on any load or type error rather than sweeping less.
func loadForDecodeSweep(t *testing.T, patterns ...string) []*packages.Package {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:  root,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		t.Fatalf("load %v: %v", patterns, err)
	}
	var problems []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			problems = append(problems, e.Error())
		}
	}
	if len(problems) > 0 {
		t.Fatalf("load %v:\n%s", patterns, strings.Join(problems, "\n"))
	}
	if len(pkgs) == 0 {
		t.Fatalf("load %v matched no packages -- this sweep is checking nothing", patterns)
	}
	return pkgs
}

// genericDecodes returns every call to json.Unmarshal or
// (*json.Decoder).Decode in pkgs whose target can hold an empty interface,
// and every use of either as a function value. With exempt, it skips
// genericDecodeExemptDirs and a Decode on a decoder that sameBlockUseNumber
// accepts. Generated files are skipped either way.
func genericDecodes(t *testing.T, pkgs []*packages.Package, exempt bool) []genericDecode {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var found []genericDecode
	for _, pkg := range pkgs {
		qualify := func(p *types.Package) string {
			if p == pkg.Types {
				return ""
			}
			return p.Name()
		}
		for _, file := range pkg.Syntax {
			abs := pkg.Fset.Position(file.Package).Filename
			rel, err := filepath.Rel(root, abs)
			if err != nil || strings.HasPrefix(rel, "..") {
				t.Fatalf("%s is outside the repository", abs)
			}
			rel = filepath.ToSlash(rel)
			if strings.HasSuffix(rel, ".gen.go") || exempt && hasAnyPrefix(rel, genericDecodeExemptDirs) {
				continue
			}
			for _, decl := range file.Decls {
				fn := declName(pkg.TypesInfo, decl)
				for _, d := range declDecodes(pkg.TypesInfo, decl, exempt) {
					d.file, d.fn = rel, fn
					if d.targetType != nil {
						d.target = types.TypeString(d.targetType, qualify)
					}
					found = append(found, d)
				}
			}
		}
	}
	// Stable across runs: the loader returns packages in no fixed order.
	sort.SliceStable(found, func(i, j int) bool { return found[i].file < found[j].file })
	return found
}

// declDecodes finds the generic decodes in one top-level declaration, leaving
// file, fn and target for the caller.
func declDecodes(info *types.Info, decl ast.Decl, exempt bool) []genericDecode {
	var found []genericDecode
	callees := map[*ast.Ident]bool{}
	ast.Inspect(decl, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			id := calleeIdent(n.Fun)
			if id == nil {
				return true
			}
			callees[id] = true
			call, target, recv := jsonDecodeCall(info, id, n)
			if call == "" {
				return true
			}
			typ := info.TypeOf(target)
			path, ok := (&reacher{seen: map[types.Type]bool{}}).reach(typ)
			if !ok || exempt && recv != nil && sameBlockUseNumber(info, decl, recv, n.Pos()) {
				return true
			}
			found = append(found, genericDecode{call: call, targetType: typ, path: path})
		case *ast.Ident:
			if callees[n] {
				return true
			}
			if name := jsonDecodeFunc(info.Uses[n]); name != "" {
				found = append(found, genericDecode{call: name})
			}
		}
		return true
	})
	return found
}

// calleeIdent returns the identifier naming the function a call calls:
// Unmarshal in json.Unmarshal(...), Decode in dec.Decode(...).
func calleeIdent(fun ast.Expr) *ast.Ident {
	switch f := ast.Unparen(fun).(type) {
	case *ast.Ident:
		return f
	case *ast.SelectorExpr:
		return f.Sel
	}
	return nil
}

// jsonDecodeFunc names obj as the failure message writes it when obj is
// encoding/json's Unmarshal or (*Decoder).Decode, and returns "" otherwise.
func jsonDecodeFunc(obj types.Object) string {
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "encoding/json" {
		return ""
	}
	recv := fn.Type().(*types.Signature).Recv()
	switch {
	case fn.Name() == "Unmarshal" && recv == nil:
		return "json.Unmarshal"
	case fn.Name() == "Decode" && recv != nil:
		return "(*json.Decoder).Decode"
	}
	return ""
}

// jsonDecodeCall returns, for a call to json.Unmarshal or
// (*json.Decoder).Decode, the decode's name, its target argument and, for
// Decode, the decoder expression.
func jsonDecodeCall(info *types.Info, callee *ast.Ident, call *ast.CallExpr) (name string, target, recv ast.Expr) {
	name = jsonDecodeFunc(info.Uses[callee])
	switch {
	case name == "json.Unmarshal" && len(call.Args) == 2:
		return name, call.Args[1], nil
	case name == "(*json.Decoder).Decode" && len(call.Args) == 1:
		sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if sel == nil {
			return "", nil, nil
		}
		return name, call.Args[0], sel.X
	}
	return "", nil, nil
}

// reacher walks a decode target's type the way encoding/json fills it.
type reacher struct {
	seen map[types.Type]bool
}

// reach reports whether decoding JSON into a value of type t can store a
// number in an empty interface, and where: a path of .Field, [i] for an
// element and [k] for a map value, empty when t is itself the interface.
//
// A type parameter counts: its instantiations are not visible here. reach
// does not descend into a value encoding/json hands to an UnmarshalJSON or
// UnmarshalText, nor into a map's keys, which encoding/json refuses to decode
// into an interface. A non-empty interface does not count: encoding/json
// decodes a number, object or array into one only through a non-nil pointer
// it already holds.
func (r *reacher) reach(t types.Type) (string, bool) {
	t = types.Unalias(t)
	if r.seen[t] {
		return "", false
	}
	r.seen[t] = true
	switch u := t.(type) {
	case *types.TypeParam:
		return "", true
	case *types.Pointer:
		if decodesItself(u) {
			return "", false
		}
		return r.reach(u.Elem())
	case *types.Named:
		// encoding/json takes the address of a named type's value.
		if decodesItself(types.NewPointer(u)) {
			return "", false
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Interface:
		return "", u.Empty()
	case *types.Pointer:
		return r.reach(u.Elem())
	case *types.Slice:
		return r.under("[i]", u.Elem())
	case *types.Array:
		return r.under("[i]", u.Elem())
	case *types.Map:
		return r.under("[k]", u.Elem())
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if f := u.Field(i); decodedField(f, u.Tag(i)) {
				if p, ok := r.under("."+f.Name(), f.Type()); ok {
					return p, true
				}
			}
		}
	}
	return "", false
}

// under is reach for a value at step below the current one.
func (r *reacher) under(step string, t types.Type) (string, bool) {
	p, ok := r.reach(t)
	return step + p, ok
}

// decodesItself reports whether pointer type p has an UnmarshalJSON or
// UnmarshalText method, which encoding/json calls instead of filling the value
// p points to.
func decodesItself(p types.Type) bool {
	ms := types.NewMethodSet(p)
	for _, name := range []string{"UnmarshalJSON", "UnmarshalText"} {
		if sel := ms.Lookup(nil, name); sel != nil {
			sig := sel.Type().(*types.Signature)
			if sig.Params().Len() == 1 && sig.Results().Len() == 1 &&
				types.TypeString(sig.Params().At(0).Type(), nil) == "[]byte" &&
				types.TypeString(sig.Results().At(0).Type(), nil) == "error" {
				return true
			}
		}
	}
	return false
}

// decodedField reports whether encoding/json decodes into field f: an
// exported field not tagged "-", or an embedded struct whose exported fields
// it promotes even when the struct's own name is unexported.
func decodedField(f *types.Var, tag string) bool {
	if reflect.StructTag(tag).Get("json") == "-" {
		return false
	}
	if f.Exported() {
		return true
	}
	if !f.Embedded() {
		return false
	}
	t := f.Type()
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	_, isStruct := t.Underlying().(*types.Struct)
	return isStruct
}

// sameBlockUseNumber reports whether recv is a local variable that decl
// assigns only json.NewDecoder(...) results to, each followed in its own
// block, and before the Decode at pos, by recv.UseNumber().
func sameBlockUseNumber(info *types.Info, decl ast.Decl, recv ast.Expr, pos token.Pos) bool {
	id, ok := ast.Unparen(recv).(*ast.Ident)
	if !ok {
		return false
	}
	v, ok := info.Uses[id].(*types.Var)
	if !ok || v.Pkg() == nil || v.Parent() == v.Pkg().Scope() {
		return false
	}
	// Every statement that assigns v, wherever it is: one in an if or for
	// header is in no block with a UseNumber, so it is never covered.
	assignments, covered := 0, 0
	ast.Inspect(decl, func(n ast.Node) bool {
		if s, ok := n.(ast.Stmt); ok {
			if _, assigns := assignedTo(info, s, v); assigns {
				assignments++
			}
		}
		var list []ast.Stmt
		switch b := n.(type) {
		case *ast.BlockStmt:
			list = b.List
		case *ast.CaseClause:
			list = b.Body
		case *ast.CommClause:
			list = b.Body
		}
		for i, s := range list {
			value, assigns := assignedTo(info, s, v)
			if assigns && isCallTo(info, value, "encoding/json", "NewDecoder") && usesNumberBefore(info, list[i+1:], v, pos) {
				covered++
			}
		}
		return true
	})
	return assignments > 0 && covered == assignments
}

// assignedTo returns the value statement s assigns to v, and whether it
// assigns v at all.
func assignedTo(info *types.Info, s ast.Stmt, v *types.Var) (ast.Expr, bool) {
	refers := func(e ast.Expr) bool {
		id, ok := ast.Unparen(e).(*ast.Ident)
		return ok && (info.Defs[id] == v || info.Uses[id] == v)
	}
	switch s := s.(type) {
	case *ast.AssignStmt:
		for i, lhs := range s.Lhs {
			if refers(lhs) {
				if len(s.Lhs) == len(s.Rhs) {
					return s.Rhs[i], true
				}
				return nil, true
			}
		}
	case *ast.RangeStmt:
		if s.Key != nil && refers(s.Key) || s.Value != nil && refers(s.Value) {
			return nil, true
		}
	case *ast.DeclStmt:
		gen, ok := s.Decl.(*ast.GenDecl)
		if !ok {
			return nil, false
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if refers(name) {
					if len(vs.Names) == len(vs.Values) {
						return vs.Values[i], true
					}
					return nil, true
				}
			}
		}
	}
	return nil, false
}

// usesNumberBefore reports whether one of stmts, starting before pos, is
// v.UseNumber().
func usesNumberBefore(info *types.Info, stmts []ast.Stmt, v *types.Var, pos token.Pos) bool {
	for _, s := range stmts {
		if s.Pos() >= pos {
			return false
		}
		es, ok := s.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok || !isCallTo(info, call, "encoding/json", "UseNumber") {
			continue
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if id, ok := ast.Unparen(sel.X).(*ast.Ident); ok && info.Uses[id] == v {
				return true
			}
		}
	}
	return false
}

// isCallTo reports whether e calls the function or method name declared in
// package pkgPath.
func isCallTo(info *types.Info, e ast.Expr, pkgPath, name string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id := calleeIdent(call.Fun)
	if id == nil {
		return false
	}
	fn, ok := info.Uses[id].(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == pkgPath && fn.Name() == name
}

// declName names a top-level declaration for the failure message: F, T.M or
// (*T).M, or "package scope" for anything but a function.
func declName(info *types.Info, decl ast.Decl) string {
	fd, ok := decl.(*ast.FuncDecl)
	if !ok {
		return "package scope"
	}
	fn, ok := info.Defs[fd.Name].(*types.Func)
	if !ok {
		return fd.Name.Name
	}
	recv := fn.Type().(*types.Signature).Recv()
	if recv == nil {
		return fn.Name()
	}
	t := types.Unalias(recv.Type())
	if p, ok := t.(*types.Pointer); ok {
		if n, ok := types.Unalias(p.Elem()).(*types.Named); ok {
			return "(*" + n.Obj().Name() + ")." + fn.Name()
		}
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name() + "." + fn.Name()
	}
	return fn.Name()
}

// hasAnyPrefix reports whether s starts with one of prefixes.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// sourceFilesUnder lists every non-test .go file below the given directories
// of the repository, relative to its root, skipping testdata.
func sourceFilesUnder(t *testing.T, dirs ...string) []string {
	t.Helper()
	var files []string
	for _, dir := range dirs {
		err := filepath.Walk(filepath.Join("../..", dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() && info.Name() == "testdata" {
				return filepath.SkipDir
			}
			if !info.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, filepath.ToSlash(strings.TrimPrefix(path, "../../")))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	sort.Strings(files)
	return files
}

// loadedFiles lists the files pkgs were type-checked from, relative to the
// repository root.
func loadedFiles(t *testing.T, pkgs []*packages.Package) []string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			rel, err := filepath.Rel(root, pkg.Fset.Position(file.Package).Filename)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, filepath.ToSlash(rel))
		}
	}
	sort.Strings(files)
	return files
}

// otherPlatformDecodeFree returns files minus those whose build constraints
// exclude this platform and that do not import encoding/json, which therefore
// hold no decode this sweep could miss.
func otherPlatformDecodeFree(t *testing.T, files []string) []string {
	t.Helper()
	var out []string
	for _, f := range files {
		path := filepath.Join("../..", filepath.FromSlash(f))
		match, err := build.Default.MatchFile(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			t.Fatal(err)
		}
		if !match {
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(parsed.Imports, func(i *ast.ImportSpec) bool { return i.Path.Value == `"encoding/json"` }) {
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

// setDiff returns the strings in a that are not in b.
func setDiff(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}

// decodeKeys returns each decode's key, sorted.
func decodeKeys(ds []genericDecode) []string {
	keys := make([]string, 0, len(ds))
	for _, d := range ds {
		keys = append(keys, d.key())
	}
	sort.Strings(keys)
	return keys
}

// TestNoGenericJSONDecode is the tripwire for a decode that turns integers
// into float64s.
func TestNoGenericJSONDecode(t *testing.T) {
	pkgs := loadForDecodeSweep(t, "./internal/...", "./cmd/...")

	// A file the loader did not type-check is a file this sweep cannot see:
	// a build constraint for another platform, or a directory go list skips.
	// Another platform's file passes only when it imports no encoding/json.
	// The walk also opens every directory from the test process, which is
	// what go test's result cache records; the loader lists them from a child
	// go list, so without the walk a new file would not invalidate a cached
	// pass.
	walked, loaded := sourceFilesUnder(t, "internal", "cmd"), loadedFiles(t, pkgs)
	if len(walked) < 100 {
		t.Fatalf("walked only %d source files under internal/ and cmd/ -- the sweep is not running", len(walked))
	}
	if missing, extra := otherPlatformDecodeFree(t, setDiff(walked, loaded)), setDiff(loaded, walked); len(missing)+len(extra) > 0 {
		t.Fatalf("the loader did not type-check exactly the files under internal/ and cmd/, so this sweep cannot see all of them\nnot type-checked: %v\nnot found by the walk: %v", missing, extra)
	}

	found := genericDecodes(t, pkgs, true)
	for _, d := range found {
		t.Error(d.String())
	}
	if len(found) > 0 {
		t.Errorf("%d decodes above can store a JSON number in an empty interface.\n"+
			"encoding/json makes that number a float64, so yaml.v3 prints 1234567 as\n"+
			"1.234567e+06 and an integer past 2^53 is already rounded.\n"+
			"Call jsonnum.Unmarshal(data, v) (internal/pkg/jsonnum) instead. A\n"+
			"json.Decoder that must stay a decoder needs UseNumber() in the same block\n"+
			"as its json.NewDecoder, and its json.Numbers must reach only an encoder\n"+
			"that handles them: encoding/json does, yaml.v3 quotes them unless\n"+
			"jsonnum.YAMLNumbers converts them first.", len(found))
	}

	// Harness-alive control: jsonnum's own decode, with every exemption lifted,
	// must still be found, or a detector that has stopped matching anything
	// would pass the sweep above over the whole module.
	var home []*packages.Package
	for _, p := range pkgs {
		if strings.HasSuffix(p.PkgPath, "/"+strings.TrimSuffix(jsonnumHome, "/")) {
			home = append(home, p)
		}
	}
	want := []string{jsonnumHome + "jsonnum.go Unmarshal: (*json.Decoder).Decode into interface{}"}
	if got := decodeKeys(genericDecodes(t, home, false)); !reflect.DeepEqual(got, want) {
		t.Fatalf("with its exemptions lifted, expected the detector to find exactly %v, found %v -- this sweep is checking nothing", want, got)
	}
}

// TestGenericJSONDecodeControl runs the detector over a package with a known
// answer: every Flagged* function once, and the Exempt* decodes only when the
// UseNumber exemption is lifted.
func TestGenericJSONDecodeControl(t *testing.T) {
	pkgs := loadForDecodeSweep(t, "./internal/catalog/testdata/genericdecode")
	const file = "internal/catalog/testdata/genericdecode/genericdecode.go "
	flagged := []string{
		"FlaggedChainedDecoder: (*json.Decoder).Decode into *any",
		"FlaggedConditionalUseNumber: (*json.Decoder).Decode into *any",
		"FlaggedDecoderParam: (*json.Decoder).Decode into *map[string]interface{}",
		"FlaggedEmbedded: json.Unmarshal into *outerExported",
		"FlaggedFuncValue: json.Unmarshal used as a function value",
		"FlaggedInClosure: json.Unmarshal into *any",
		"FlaggedInlinePromotion: json.Unmarshal into *inlineHolder",
		"FlaggedInterface: json.Unmarshal into *interface{}",
		"FlaggedMap: json.Unmarshal into *map[string]any",
		"FlaggedNested: json.Unmarshal into *page",
		"FlaggedParam: json.Unmarshal into any",
		"FlaggedRecursive: json.Unmarshal into *tree",
		"FlaggedSlice: json.Unmarshal into *[]any",
		"FlaggedStructField: json.Unmarshal into *withAny",
		"FlaggedTypeParam: json.Unmarshal into *T",
		"FlaggedUnexportedEmbedded: json.Unmarshal into *outerUnexported",
		"FlaggedUseNumberTooLate: (*json.Decoder).Decode into *any",
	}
	exempted := []string{
		"ExemptSameBlockUseNumber: (*json.Decoder).Decode into *any",
		"ExemptSameBlockUseNumber: (*json.Decoder).Decode into *any",
		"ExemptVarDeclUseNumber: (*json.Decoder).Decode into *map[string]any",
	}
	expect := func(lines ...[]string) []string {
		var out []string
		for _, l := range lines {
			for _, s := range l {
				out = append(out, file+s)
			}
		}
		sort.Strings(out)
		return out
	}
	if got, want := decodeKeys(genericDecodes(t, pkgs, true)), expect(flagged); !reflect.DeepEqual(got, want) {
		t.Errorf("detector over the control package:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
	if got, want := decodeKeys(genericDecodes(t, pkgs, false)), expect(flagged, exempted); !reflect.DeepEqual(got, want) {
		t.Errorf("detector over the control package, exemptions lifted:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
}
