package catalog

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// The mechanism this file guards.
//
// yaml.v3 reads yaml struct tags and nothing else. It writes a field with no
// yaml tag under its Go name lowercased (FamilyID as familyid), ignores json's
// omitempty, nests an embedded struct where encoding/json promotes its fields,
// and never calls MarshalJSON, so a generated union -- its value kept in an
// unexported json.RawMessage -- prints as {}. The generated api types carry
// json tags only, so a command that hands one to a YAML encoder prints a
// document its own -o json twin contradicts on every key. output.WriteRaw
// encodes a value's JSON form instead.
//
// TestNoHandRolledJSONToYAML and TestNoBareYAMLEncoder look at how a command
// encodes; neither looks at what. So this sweep asks the type checker what
// each encode can be handed. An argument typed interface{} is followed back to
// the callers of the function it is a parameter of, the values assigned to the
// local it names, or the returns of the function whose result it is. The
// sweep does not see a struct stored in an interface{} inside the encoded
// value, or an encode called through an interface method.

const jsonnumPkgPath = "github.com/qlustered/qctl/internal/pkg/jsonnum"

// yamlKeysExempt names, by file and declaration, the encodes whose argument
// was built from its JSON form before it reached the encoder.
var yamlKeysExempt = map[string]string{
	"internal/output/output.go (*Printer).printYAML":       "encodes what secretmask returns: maps keyed by JSON name",
	"internal/output/raw_body.go WriteJSONAsYAML":          "encodes a decoded JSON document",
	"internal/output/stream.go (*Stream).Write":            "encodes what secretmask returns: maps keyed by JSON name",
	"internal/pkg/printer/printer.go (*Printer).printYAML": "encodes what secretmask returns: maps keyed by JSON name",
}

// yamlKeysKnown names, by file and declaration, encodes that write untagged
// fields today, each against the issue that tracks it.
var yamlKeysKnown = map[string]string{}

// yamlPlainConverters are the functions whose interface{} result is maps,
// slices and scalars keyed as encoding/json keys them.
var yamlPlainConverters = map[string]bool{
	"github.com/qlustered/qctl/internal/markdown.ProcessFieldsPlain": true,
}

// yamlKeyFinding is one encode that can write a key encoding/json would not.
type yamlKeyFinding struct {
	file    string // relative to the repository root
	fn      string // the declaration holding the encode
	via     string // the declaration the value was found in, when not fn
	problem string
}

func (f yamlKeyFinding) site() string { return f.file + " " + f.fn }

func (f yamlKeyFinding) key() string {
	if f.via != "" && f.via != f.fn {
		return fmt.Sprintf("%s via %s: %s", f.site(), f.via, f.problem)
	}
	return fmt.Sprintf("%s: %s", f.site(), f.problem)
}

// yamlEncodeName names obj as the failure message writes it when obj is a
// function that YAML-encodes its one argument, and returns "" otherwise.
func yamlEncodeName(obj types.Object) string {
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return ""
	}
	isMethod := fn.Type().(*types.Signature).Recv() != nil
	switch path := fn.Pkg().Path(); {
	case path == jsonnumPkgPath && fn.Name() == "MarshalYAML" && !isMethod:
		return "jsonnum.MarshalYAML"
	case path == jsonnumPkgPath && fn.Name() == "Encode" && isMethod:
		return "(*jsonnum.YAMLEncoder).Encode"
	case yamlV3Paths[path] && fn.Name() == "Marshal" && !isMethod:
		return "yaml.Marshal"
	case yamlV3Paths[path] && fn.Name() == "Encode" && isMethod:
		return "(*yaml.Encoder).Encode"
	}
	return ""
}

// yamlDecl is one top-level declaration and the package it is in.
type yamlDecl struct {
	pkg  *packages.Package
	decl ast.Decl
}

func (d yamlDecl) name() string { return declName(d.pkg.TypesInfo, d.decl) }

// yamlCall is one call and the declaration it is in.
type yamlCall struct {
	yamlDecl
	call *ast.CallExpr
}

// yamlSource is one value that can reach an encode: its static type, or why
// the sweep cannot tell.
type yamlSource struct {
	in         yamlDecl
	typ        types.Type // nil when plain or unresolved
	plain      bool
	unresolved string
}

// yamlFlow answers where an interface{} value came from, over every function
// in the loaded packages.
type yamlFlow struct {
	decls   map[*types.Func]yamlDecl
	callers map[*types.Func][]yamlCall
	values  map[*types.Func]bool // referenced other than as a callee
	seen    map[string]bool
}

func newYAMLFlow(pkgs []*packages.Package) *yamlFlow {
	f := &yamlFlow{
		decls:   map[*types.Func]yamlDecl{},
		callers: map[*types.Func][]yamlCall{},
		values:  map[*types.Func]bool{},
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				d := yamlDecl{pkg, decl}
				if fd, ok := decl.(*ast.FuncDecl); ok {
					if fn, ok := pkg.TypesInfo.Defs[fd.Name].(*types.Func); ok {
						f.decls[fn] = d
					}
				}
				callees := map[*ast.Ident]bool{}
				ast.Inspect(decl, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.CallExpr:
						if id := calleeIdent(n.Fun); id != nil {
							callees[id] = true
							if fn, ok := pkg.TypesInfo.Uses[id].(*types.Func); ok {
								fn = fn.Origin()
								f.callers[fn] = append(f.callers[fn], yamlCall{d, n})
							}
						}
					case *ast.Ident:
						if fn, ok := pkg.TypesInfo.Uses[n].(*types.Func); ok && !callees[n] {
							f.values[fn.Origin()] = true
						}
					}
					return true
				})
			}
		}
	}
	return f
}

// sources returns every value e, in d, can be.
func (f *yamlFlow) sources(d yamlDecl, e ast.Expr) []yamlSource {
	info := d.pkg.TypesInfo
	e = ast.Unparen(e)
	if _, isIface := info.TypeOf(e).Underlying().(*types.Interface); !isIface {
		return []yamlSource{{in: d, typ: info.TypeOf(e)}}
	}
	unresolved := func(why string) []yamlSource {
		return []yamlSource{{in: d, unresolved: why}}
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		if tv, ok := info.Types[x.Fun]; ok && tv.IsType() && len(x.Args) == 1 {
			return f.sources(d, x.Args[0])
		}
		return f.results(d, x, 0)
	case *ast.Ident:
		v, ok := info.Uses[x].(*types.Var)
		if !ok {
			return unresolved("not a variable")
		}
		if fd, ok := d.decl.(*ast.FuncDecl); ok {
			fn, _ := info.Defs[fd.Name].(*types.Func)
			if fn != nil {
				params := fn.Type().(*types.Signature).Params()
				for i := 0; i < params.Len(); i++ {
					if params.At(i) == v {
						return f.params(d, fn, i)
					}
				}
			}
		}
		return f.locals(d, v)
	}
	return unresolved(fmt.Sprintf("a %T", e))
}

// params returns every argument a caller passes as fn's parameter i.
func (f *yamlFlow) params(d yamlDecl, fn *types.Func, i int) []yamlSource {
	key := fmt.Sprintf("param %s %d", fn.FullName(), i)
	if f.seen[key] {
		return nil
	}
	f.seen[key] = true
	unresolved := func(why string) yamlSource {
		return yamlSource{in: d, unresolved: fn.Name() + " " + why}
	}
	if f.values[fn] {
		return []yamlSource{unresolved("is used as a function value")}
	}
	calls := f.callers[fn]
	if len(calls) == 0 {
		return []yamlSource{unresolved("has no caller the sweep can see")}
	}
	sig := fn.Type().(*types.Signature)
	var out []yamlSource
	for _, c := range calls {
		switch {
		case sig.Variadic() && i == sig.Params().Len()-1:
			out = append(out, unresolved("is variadic"))
		case len(c.call.Args) != sig.Params().Len():
			out = append(out, unresolved("is called with a multi-value argument"))
		default:
			out = append(out, f.sources(c.yamlDecl, c.call.Args[i])...)
		}
	}
	return out
}

// results returns every value call's result i can be.
func (f *yamlFlow) results(d yamlDecl, call *ast.CallExpr, i int) []yamlSource {
	unresolved := func(why string) []yamlSource {
		return []yamlSource{{in: d, unresolved: why}}
	}
	id := calleeIdent(call.Fun)
	if id == nil {
		return unresolved("a call through an expression")
	}
	fn, ok := d.pkg.TypesInfo.Uses[id].(*types.Func)
	if !ok {
		return unresolved("a call through a function value")
	}
	fn = fn.Origin()
	if fn.Pkg() != nil && yamlPlainConverters[fn.Pkg().Path()+"."+fn.Name()] {
		return []yamlSource{{in: d, plain: true}}
	}
	key := fmt.Sprintf("result %s %d", fn.FullName(), i)
	if f.seen[key] {
		return nil
	}
	f.seen[key] = true
	callee, ok := f.decls[fn]
	if !ok || callee.decl.(*ast.FuncDecl).Body == nil {
		return unresolved("a call to " + fn.FullName() + ", whose body the sweep did not load")
	}
	var out []yamlSource
	ast.Inspect(callee.decl.(*ast.FuncDecl).Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			switch {
			case len(n.Results) == 0:
				out = append(out, yamlSource{in: callee, unresolved: "a bare return"})
			case len(n.Results) == 1 && i > 0:
				if c, ok := ast.Unparen(n.Results[0]).(*ast.CallExpr); ok {
					out = append(out, f.results(callee, c, i)...)
				}
			default:
				out = append(out, f.sources(callee, n.Results[i])...)
			}
		}
		return true
	})
	return out
}

// locals returns every value d assigns to the local v.
func (f *yamlFlow) locals(d yamlDecl, v *types.Var) []yamlSource {
	info := d.pkg.TypesInfo
	is := func(e ast.Expr) bool {
		id, ok := ast.Unparen(e).(*ast.Ident)
		return ok && (info.Defs[id] == v || info.Uses[id] == v)
	}
	unresolved := func(why string) yamlSource {
		return yamlSource{in: d, unresolved: v.Name() + " " + why}
	}
	var out []yamlSource
	assigned := false
	// assign records one value given to v: rhs itself, or result i of rhs.
	assign := func(rhs []ast.Expr, i, n int) {
		assigned = true
		switch {
		case len(rhs) == n:
			out = append(out, f.sources(d, rhs[i])...)
		case len(rhs) == 1:
			if c, ok := ast.Unparen(rhs[0]).(*ast.CallExpr); ok {
				out = append(out, f.results(d, c, i)...)
				return
			}
			out = append(out, unresolved("is assigned a comma-ok result"))
		}
	}
	ast.Inspect(d.decl, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.UnaryExpr:
			if n.Op == token.AND && is(n.X) {
				assigned = true
				out = append(out, unresolved("has its address taken"))
			}
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if is(lhs) {
					assign(n.Rhs, i, len(n.Lhs))
				}
			}
		case *ast.ValueSpec:
			for i, name := range n.Names {
				if info.Defs[name] == v && len(n.Values) > 0 {
					assign(n.Values, i, len(n.Names))
				}
			}
		case *ast.RangeStmt:
			if (n.Key != nil && is(n.Key)) || (n.Value != nil && is(n.Value)) {
				assigned = true
				out = append(out, unresolved("is a range variable"))
			}
		}
		return true
	})
	if !assigned {
		out = append(out, unresolved("is never assigned"))
	}
	return out
}

// yamlKeyWalker walks a type the way yaml.v3 encodes a value of it.
type yamlKeyWalker struct {
	qualify  types.Qualifier
	seen     map[types.Type]bool
	problems []string
}

// hasMethod reports whether t's method set has a method called name.
func hasMethod(t types.Type, name string) bool {
	return types.NewMethodSet(t).Lookup(nil, name) != nil
}

// walk records, under path, each place a value of type t makes yaml.v3 write
// something encoding/json would not.
func (w *yamlKeyWalker) walk(path string, t types.Type) {
	t = types.Unalias(t)
	if w.seen[t] {
		return
	}
	w.seen[t] = true
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil && yamlV3Paths[n.Obj().Pkg().Path()] && n.Obj().Name() == "Node" {
		return
	}
	if hasMethod(t, "MarshalYAML") || hasMethod(t, "MarshalText") {
		return
	}
	name := types.TypeString(t, w.qualify)
	at := ""
	if path != "" {
		at = "at " + path + ", "
	}
	if _, isPtr := t.(*types.Pointer); !isPtr && (hasMethod(t, "MarshalJSON") || hasMethod(types.NewPointer(t), "MarshalJSON")) {
		w.problems = append(w.problems, at+name+" has MarshalJSON and no MarshalYAML or MarshalText")
		return
	}
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		w.walk(path, u.Elem())
	case *types.Slice:
		w.walk(path+"[i]", u.Elem())
	case *types.Array:
		w.walk(path+"[i]", u.Elem())
	case *types.Map:
		w.walk(path+"[k]", u.Elem())
	case *types.Struct:
		var untagged []string
		for i := 0; i < u.NumFields(); i++ {
			field := u.Field(i)
			if !field.Exported() && !field.Embedded() {
				continue
			}
			tag, ok := yamlTag(u.Tag(i))
			if tag == "-" {
				continue
			}
			if !ok {
				untagged = append(untagged, field.Name())
				continue
			}
			w.walk(path+"."+field.Name(), field.Type())
		}
		if len(untagged) > 0 {
			w.problems = append(w.problems, at+name+" has no yaml tag on "+nameList(untagged))
		}
	}
}

// yamlTag returns the yaml tag yaml.v3 reads from a struct tag: the yaml key,
// or the whole tag when it has no key: value pairs.
func yamlTag(tag string) (string, bool) {
	if y, ok := reflect.StructTag(tag).Lookup("yaml"); ok {
		return y, true
	}
	if tag != "" && !strings.Contains(tag, ":") {
		return tag, true
	}
	return "", false
}

// nameList joins names, naming at most four.
func nameList(names []string) string {
	if len(names) <= 4 {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:4], ", "), len(names)-4)
}

// yamlKeyFindings returns every YAML encode in pkgs outside jsonnum that can
// write a key encoding/json would not, or whose argument the sweep cannot see,
// except in the declarations yamlKeysExempt names; and every declaration
// holding an encode, exempt or not.
func yamlKeyFindings(t *testing.T, pkgs []*packages.Package) (findings []yamlKeyFinding, sites map[string]bool) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rel := func(pkg *packages.Package, n ast.Node) string {
		r, err := filepath.Rel(root, pkg.Fset.Position(n.Pos()).Filename)
		if err != nil || strings.HasPrefix(r, "..") {
			t.Fatalf("%s is outside the repository", pkg.Fset.Position(n.Pos()).Filename)
		}
		return filepath.ToSlash(r)
	}
	flow := newYAMLFlow(pkgs)
	sites = map[string]bool{}
	for _, pkg := range pkgs {
		qualify := func(p *types.Package) string { return p.Name() }
		for _, file := range pkg.Syntax {
			path := rel(pkg, file)
			if strings.HasSuffix(path, ".gen.go") || strings.HasPrefix(path, yamlEncoderHome) {
				continue
			}
			for _, decl := range file.Decls {
				d := yamlDecl{pkg, decl}
				site := path + " " + d.name()
				callees := map[*ast.Ident]bool{}
				ast.Inspect(decl, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.CallExpr:
						id := calleeIdent(n.Fun)
						if id == nil {
							return true
						}
						callees[id] = true
						if yamlEncodeName(pkg.TypesInfo.Uses[id]) == "" || len(n.Args) != 1 {
							return true
						}
						sites[site] = true
						if yamlKeysExempt[site] != "" {
							return true
						}
						flow.seen = map[string]bool{}
						for _, src := range flow.sources(d, n.Args[0]) {
							finding := yamlKeyFinding{file: path, fn: d.name(), via: src.in.name()}
							switch {
							case src.plain:
								continue
							case src.unresolved != "":
								finding.problem = "cannot tell what it writes: " + src.unresolved
								findings = append(findings, finding)
								continue
							}
							w := &yamlKeyWalker{qualify: qualify, seen: map[types.Type]bool{}}
							w.walk("", src.typ)
							for _, p := range w.problems {
								finding.problem = p
								findings = append(findings, finding)
							}
						}
					case *ast.Ident:
						if !callees[n] && yamlEncodeName(pkg.TypesInfo.Uses[n]) != "" {
							sites[site] = true
							findings = append(findings, yamlKeyFinding{file: path, fn: d.name(),
								problem: yamlEncodeName(pkg.TypesInfo.Uses[n]) + " is used as a function value, which hides what it writes"})
						}
					}
					return true
				})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].key() < findings[j].key() })
	return findings, sites
}

// TestYAMLEncodesWriteYAMLTaggedFields is the tripwire for a YAML encode of a
// type yaml.v3 and encoding/json write differently.
func TestYAMLEncodesWriteYAMLTaggedFields(t *testing.T) {
	pkgs := loadForDecodeSweep(t, "./internal/...", "./cmd/...")
	findings, sites := yamlKeyFindings(t, pkgs)

	known := map[string]bool{}
	var failures []string
	for _, f := range findings {
		if issue := yamlKeysKnown[f.site()]; issue != "" {
			known[f.site()] = true
			continue
		}
		failures = append(failures, f.key())
	}
	for _, f := range failures {
		t.Error(f)
	}
	if len(failures) > 0 {
		t.Errorf("%d YAML encodes above can write a key -o json would not.\n"+
			"yaml.v3 writes a field with no yaml tag under its lowercased Go name\n"+
			"(FamilyID as familyid), ignores json's omitempty, and never calls MarshalJSON,\n"+
			"so a generated union prints {}. For a generated api type, call\n"+
			"output.WriteRaw(w, format, v), which encodes its JSON form; for a type the CLI\n"+
			"owns, give every field a yaml tag. An interface{} the sweep cannot follow wants\n"+
			"a typed argument.", len(failures))
	}
	for site, issue := range yamlKeysKnown {
		if !known[site] {
			t.Errorf("%s no longer writes an untagged field; drop it from yamlKeysKnown and close %s if nothing else is left of it", site, issue)
		}
	}
	for site := range yamlKeysExempt {
		if !sites[site] {
			t.Errorf("%s is exempt but encodes no YAML; drop it from yamlKeysExempt", site)
		}
	}

	// Harness-alive control: a detector that has stopped matching encodes
	// would pass the sweep above over the whole module.
	if len(sites) < 20 {
		t.Fatalf("found YAML encodes in only %d declarations -- this sweep is not running", len(sites))
	}
}

// TestYAMLKeysControl runs the detector over a package with a known answer:
// each Flagged* function once, and no Clean* function.
func TestYAMLKeysControl(t *testing.T) {
	pkgs := loadForDecodeSweep(t, "./internal/catalog/testdata/yamlkeys")
	findings, _ := yamlKeyFindings(t, pkgs)
	const file = "internal/catalog/testdata/yamlkeys/yamlkeys.go "
	want := []string{
		"FlaggedBareYAML: yamlkeys.jsonOnly has no yaml tag on FamilyID, Name",
		"FlaggedBareYAMLEncoder: yamlkeys.untagged has no yaml tag on Name",
		"FlaggedEmbedded: yamlkeys.embedding has no yaml tag on jsonOnly",
		"FlaggedFuncValue: jsonnum.MarshalYAML is used as a function value, which hides what it writes",
		"FlaggedJSONOnly: yamlkeys.jsonOnly has no yaml tag on FamilyID, Name",
		"FlaggedMarshalJSON: at .Value, yamlkeys.union has MarshalJSON and no MarshalYAML or MarshalText",
		"FlaggedNested: at .Items[k][i], yamlkeys.jsonOnly has no yaml tag on FamilyID, Name",
		"FlaggedThroughLocal: yamlkeys.jsonOnly has no yaml tag on FamilyID, Name",
		"FlaggedThroughMultiReturn via buildTwo: yamlkeys.jsonOnly has no yaml tag on FamilyID, Name",
		"FlaggedThroughReturn via build: yamlkeys.untagged has no yaml tag on Name",
		"FlaggedUnresolved: cannot tell what it writes: a *ast.SelectorExpr",
		"FlaggedUntagged: yamlkeys.untagged has no yaml tag on Name",
		"encodeAny via FlaggedThroughParameter: yamlkeys.jsonOnly has no yaml tag on FamilyID, Name",
	}
	var got []string
	for _, f := range findings {
		got = append(got, strings.TrimPrefix(f.key(), file))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detector over the control package:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
}
