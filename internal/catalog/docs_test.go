package catalog_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/commands/explain"
	"github.com/qlustered/qctl/internal/commands/guide"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/schema"
)

// TestColumnsHelpListsComeFromRowTypes keeps advertised columns tied to the
// struct the printer validates and renders.
func TestColumnsHelpListsComeFromRowTypes(t *testing.T) {
	err := filepath.WalkDir("../commands", func(path string, entry os.DirEntry, walkErr error) error {
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
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 3 {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || method.Sel.Name != "String" {
				return true
			}
			name, ok := call.Args[0].(*ast.BasicLit)
			if !ok || name.Value != `"columns"` {
				return true
			}

			hasList := false
			hasDerivedList := false
			ast.Inspect(call.Args[2], func(part ast.Node) bool {
				switch part := part.(type) {
				case *ast.BasicLit:
					if part.Kind == token.STRING {
						value, err := strconv.Unquote(part.Value)
						if err == nil && strings.Contains(value, "Available:") {
							hasList = true
							if strings.TrimSpace(strings.SplitN(value, "Available:", 2)[1]) != "" {
								t.Errorf("%s: --columns help contains a literal Available: list", path)
							}
						}
					}
				case *ast.CallExpr:
					if len(part.Args) == 0 {
						break
					}
					join, ok := part.Fun.(*ast.SelectorExpr)
					if !ok || join.Sel.Name != "Join" {
						break
					}
					pkg, ok := join.X.(*ast.Ident)
					if !ok || pkg.Name != "strings" {
						break
					}
					columns, ok := part.Args[0].(*ast.CallExpr)
					if !ok {
						break
					}
					available, ok := columns.Fun.(*ast.SelectorExpr)
					if !ok || (available.Sel.Name != "AvailableColumns" && available.Sel.Name != "DeclareColumns") {
						break
					}
					pkg, ok = available.X.(*ast.Ident)
					hasDerivedList = ok && ((pkg.Name == "output" && available.Sel.Name == "AvailableColumns") ||
						(pkg.Name == "cmdutil" && available.Sel.Name == "DeclareColumns"))
				}
				return true
			})
			if hasList && !hasDerivedList {
				t.Errorf("%s: --columns Available: list must derive from output.AvailableColumns or cmdutil.DeclareColumns", path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

const (
	patternsPath   = "../../docs/patterns.md"
	commandsPath   = "../../docs/commands.md"
	repositoryRoot = "../.."
)

// readGeneratedBlock returns the body between the BEGIN/END markers for name.
// A missing marker is an error and never an empty block: silently treating it
// as empty is how -update once "re-stamped" a file it had not written (see
// internal/catalog/pages_test.go's note on that failure).
func readGeneratedBlock(doc, name string) (string, error) {
	begin := "<!-- BEGIN GENERATED: " + name + " -->\n"
	end := "<!-- END GENERATED: " + name + " -->"
	i := strings.Index(doc, begin)
	if i < 0 {
		return "", fmt.Errorf("no %q marker", strings.TrimSuffix(begin, "\n"))
	}
	rest := doc[i+len(begin):]
	j := strings.Index(rest, end)
	if j < 0 {
		return "", fmt.Errorf("no %q marker after the BEGIN marker", end)
	}
	return rest[:j], nil
}

// fileMapRowCommand matches a markdown table row whose first cell is a
// backticked command line.
var fileMapRowCommand = regexp.MustCompile("^\\| *`([^`]+)`")

var (
	inlineCodeSpan  = regexp.MustCompile("`([^`\\n]+)`")
	pathPlaceholder = regexp.MustCompile(`<[^/<>]+>`)
)

type documentedPath struct {
	value string
	line  int
}

// documentedRepositoryPaths returns inline-code references that use one of
// the repository's top-level source or documentation directories.
func documentedRepositoryPaths(doc string) []documentedPath {
	var out []documentedPath
	for lineNumber, line := range strings.Split(doc, "\n") {
		for _, match := range inlineCodeSpan.FindAllStringSubmatch(line, -1) {
			path := match[1]
			if !strings.HasPrefix(path, "cmd/") &&
				!strings.HasPrefix(path, "docs/") &&
				!strings.HasPrefix(path, "internal/") &&
				!strings.HasPrefix(path, "scripts/") &&
				!strings.HasPrefix(path, "tools/") {
				continue
			}
			out = append(out, documentedPath{value: path, line: lineNumber + 1})
		}
	}
	return out
}

// resolveDocumentedPath expands the limited patterns used by commands.md and
// returns the repository entries they name. Angle-bracket path components are
// documentation placeholders; globs retain their usual filepath semantics.
func resolveDocumentedPath(root, path string) ([]string, error) {
	pattern := pathPlaceholder.ReplaceAllString(path, "*")
	pattern = filepath.Join(root, filepath.FromSlash(pattern))
	var matches []string
	var err error
	if strings.ContainsAny(pattern, "*?[") {
		matches, err = filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
	} else if _, err = os.Stat(pattern); err == nil {
		matches = []string{pattern}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, os.ErrNotExist
	}
	return matches, nil
}

func TestDocumentedRepositoryPathParser(t *testing.T) {
	doc := "See `internal/example/file.go`, `docs/*.md`, and " +
		"`tools/<name>/`; ignore `history.RunFlow` and `/api/items`."
	want := []documentedPath{
		{value: "internal/example/file.go", line: 1},
		{value: "docs/*.md", line: 1},
		{value: "tools/<name>/", line: 1},
	}
	if got := documentedRepositoryPaths(doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("documentedRepositoryPaths() = %#v, want %#v", got, want)
	}
}

// TestCommandsDocumentedRepositoryPathsResolve keeps commands.md's jump
// targets honest. It deliberately owns only literal repository references;
// the generated-block and leaf-coverage tests remain the command inventory
// boundary, while package-qualified Go symbols remain prose rather than paths.
func TestCommandsDocumentedRepositoryPathsResolve(t *testing.T) {
	raw, err := os.ReadFile(commandsPath)
	if err != nil {
		t.Fatalf("read %s: %v", commandsPath, err)
	}
	references := documentedRepositoryPaths(string(raw))
	if len(references) == 0 {
		t.Fatalf("%s yielded no repository paths; the parser is broken, not the doc", commandsPath)
	}
	for _, reference := range references {
		if _, err := resolveDocumentedPath(repositoryRoot, reference.value); err != nil {
			t.Errorf("%s:%d: `%s` does not resolve in the repository: %v",
				commandsPath, reference.line, reference.value, err)
		}
	}
}

// fileMapCommands returns the command paths docs/commands.md's tables claim to
// map, keyed without the "qctl " prefix. Argument and flag tokens are dropped
// at the first one that appears, so `get table <id> --job-activity` documents
// `get table` and `cache aliases clear [--all-servers]` documents
// `cache aliases clear`.
//
// The file's non-command tables (domain package names, the discovery-surface
// table) contribute harmless extra keys — the test only ever asks whether a
// real command path is present, never the reverse, so noise cannot hide a gap.
func fileMapCommands(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		m := fileMapRowCommand.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var kept []string
		for _, tok := range strings.Fields(m[1]) {
			if strings.HasPrefix(tok, "<") || strings.HasPrefix(tok, "[") || strings.HasPrefix(tok, "-") {
				break
			}
			kept = append(kept, tok)
		}
		if len(kept) > 0 {
			out[strings.Join(kept, " ")] = true
		}
	}
	return out, nil
}

// TestEveryLeafCommandHasAFileMapRow: docs/commands.md is the command-to-file
// map CLAUDE.md tells every session to read before exploring, so a command
// missing from it is a command the next session is told does not exist.
//
// Leaves only — a group like `get` is covered by its subcommands' rows, and the
// map's job is to point at an implementation file, which a dispatch parent
// barely has. There is no -update: a row names a file, a domain client and a
// test file, and only a human knows those.
func TestEveryLeafCommandHasAFileMapRow(t *testing.T) {
	documented, err := fileMapCommands(commandsPath)
	if err != nil {
		t.Fatalf("read %s: %v", commandsPath, err)
	}
	if len(documented) == 0 {
		t.Fatalf("%s yielded no table rows; the parser is broken, not the doc", commandsPath)
	}

	var missing []string
	for _, n := range catalog.Walk(realRoot(t)) {
		if n.Path == "qctl" || n.Cmd.Name() == "help" || !n.Cmd.IsAvailableCommand() {
			continue
		}
		if len(catalog.VisibleChildren(n.Cmd)) > 0 {
			continue
		}
		path := strings.TrimPrefix(n.Path, "qctl ")
		if !documented[path] {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("%d command(s) have no row in docs/commands.md: %v\n\n"+
			"Add a table row naming the implementation file (and the domain client and test file "+
			"where the section's table has those columns) under the matching section.",
			len(missing), missing)
	}
}

// replaceGeneratedBlock returns doc with the named block's body replaced.
func replaceGeneratedBlock(doc, name, body string) (string, error) {
	begin := "<!-- BEGIN GENERATED: " + name + " -->\n"
	end := "<!-- END GENERATED: " + name + " -->"
	i := strings.Index(doc, begin)
	if i < 0 {
		return "", fmt.Errorf("no %q marker", strings.TrimSuffix(begin, "\n"))
	}
	head := doc[:i+len(begin)]
	rest := doc[i+len(begin):]
	j := strings.Index(rest, end)
	if j < 0 {
		return "", fmt.Errorf("no %q marker after the BEGIN marker", end)
	}
	return head + body + rest[j:], nil
}

// fenced wraps a rendered block in the code fence the doc carries.
func fenced(body string) string { return "```\n" + body + "```\n" }

// tableRuleManifestFields renders the kind: TableRule field lists that
// docs/patterns.md used to carry by hand. Both halves are DERIVED, not
// transcribed: a spec key added to the display manifest, or a new key taught
// to BuildPatchMap, moves this block without anyone remembering the doc
// exists. That is the point -- the hand-written version was missing
// produced_fields and rule_revision_id, still named an is_enabled that had
// not been a patch key for some time, and no test could see any of it.
func tableRuleManifestFields(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("get/describe table-rule -o yaml writes these spec keys:\n")
	for _, k := range yamlKeys(reflect.TypeOf(dataset_rules.TableRuleSpec{})) {
		b.WriteString("  " + k + "\n")
	}
	b.WriteString("\napply -f / diff -f turn these spec keys into a PATCH body:\n")
	for _, k := range patchableSpecKeys(t) {
		b.WriteString("  " + k + "\n")
	}
	return b.String()
}

// yamlKeys returns a struct's yaml tag names in declaration order, without
// the tag options.
func yamlKeys(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		out = append(out, tag)
	}
	return out
}

// patchableSpecKeys asks BuildPatchMap itself which spec keys it understands,
// by setting one field at a time on an otherwise-empty manifest and seeing
// whether a body key comes out. Probing per field rather than filling the
// whole struct once is deliberate: a field that is added to the struct but
// NOT read by BuildPatchMap has to stay off this list, and a fully-populated
// fixture cannot tell those two apart.
//
// spec.rule, spec.release and spec.column are absent for that reason. They
// resolve into rule_revision_id and column_mapping in the apply command
// before the patch body is built, so BuildPatchMap never reads them.
func patchableSpecKeys(t *testing.T) []string {
	t.Helper()
	specType := reflect.TypeOf(dataset_rules.TableRuleApplyManifest{}.Spec)
	out := []string{}
	for i := 0; i < specType.NumField(); i++ {
		var m dataset_rules.TableRuleApplyManifest
		field := reflect.ValueOf(&m.Spec).Elem().Field(i)
		if !setNonZero(field) {
			// Silently leaving it zero would drop a real patch key from the
			// doc and re-stamp green. Fail instead, and teach setNonZero the
			// kind.
			t.Fatalf("spec.%s is a %s, which setNonZero cannot populate -- "+
				"add the kind before trusting this block", specType.Field(i).Name, field.Kind())
		}
		if len(dataset_rules.BuildPatchMap(&m)) == 0 {
			continue
		}
		tag, _, _ := strings.Cut(specType.Field(i).Tag.Get("yaml"), ",")
		out = append(out, "spec."+tag)
	}
	return out
}

// setNonZero gives v a value that BuildPatchMap's "is this set?" test
// accepts, and reports whether it knew how.
func setNonZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		return setNonZero(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() && !setNonZero(v.Field(i)) {
				return false
			}
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		m.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), reflect.Zero(v.Type().Elem()))
		v.Set(m)
	case reflect.String:
		v.SetString("x")
	case reflect.Int:
		v.SetInt(1)
	case reflect.Bool:
		v.SetBool(true)
	default:
		return false
	}
	return true
}

// explainSchemaSources renders the explain-specific enumeration in
// docs/commands.md from both registries the command actually reads. The
// manifest names are qctl-side schemas; the composite names are the resources
// mapped to the backend's apply-manifest component.
func explainSchemaSources(t *testing.T) string {
	t.Helper()
	manifestNames := explain.ManifestSchemaNames()
	if len(manifestNames) == 0 {
		t.Fatal("explain.ManifestSchemaNames() returned no names")
	}

	const compositeSchema = "CompositeDatasetApplyManifest"
	var compositeNames []string
	for name, component := range schema.SchemaMapping {
		if component == compositeSchema {
			compositeNames = append(compositeNames, name)
		}
	}
	if len(compositeNames) == 0 {
		t.Fatalf("schema.SchemaMapping has no resources for %s", compositeSchema)
	}
	sort.Strings(compositeNames)

	quoted := func(names []string) string {
		out := make([]string, len(names))
		for i, name := range names {
			out[i] = "`" + name + "`"
		}
		return strings.Join(out, ", ")
	}
	return fmt.Sprintf("- `explain` supports, in addition to the API-backed resources, "+
		"the manifest kinds served by static offline schemas — the source of truth is "+
		"`manifestSchemas` in `internal/commands/explain/manifests.go` (%s), reachable as "+
		"`explain.ManifestSchemaNames()` — plus %s (`%s`).\n",
		quoted(manifestNames), quoted(compositeNames), compositeSchema)
}

type generatedBlock struct {
	name string
	want string
}

func testGeneratedBlocks(t *testing.T, path, updateTest string, blocks []generatedBlock) {
	t.Helper()
	for _, tc := range blocks {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			doc := string(raw)
			got, err := readGeneratedBlock(doc, tc.name)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if got == tc.want {
				return
			}
			if !*update {
				t.Errorf("%s's %q block drifted from the code.\n--- doc has ---\n%s\n--- code says ---\n%s\n"+
					"The block is generated. Do not hand-edit it; re-stamp with:\n"+
					"    go test ./internal/catalog -run %s -update", path, tc.name, got, tc.want, updateTest)
				return
			}
			out, err := replaceGeneratedBlock(doc, tc.name, tc.want)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			// Prove the write landed. -update reporting success on a file it
			// did not change is the exact failure this repo has already had
			// once; a re-read is what makes the log line trustworthy.
			reread, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("re-read %s: %v", path, err)
			}
			after, err := readGeneratedBlock(string(reread), tc.name)
			if err != nil {
				t.Fatalf("re-read %s: %v", path, err)
			}
			if after != tc.want {
				t.Fatalf("-update wrote %s but its %q block still differs", path, tc.name)
			}
			t.Logf("re-stamped %s block in %s", tc.name, path)
		})
	}
}

// TestPatternsGeneratedBlocks is the tripwire this whole change exists for:
// docs/patterns.md's command tree and guide index are generated from the real
// cobra tree and the real guide registry, so a command or topic that ships
// without them turns the suite red instead of drifting silently. CLAUDE.md
// sends every new session to this file before it explores the codebase — a
// stale tree tells that session commands which exist do not.
//
// -update rewrites the blocks in place, matching TestPageSignatures.
func TestPatternsGeneratedBlocks(t *testing.T) {
	root := realRoot(t)
	testGeneratedBlocks(t, patternsPath, "TestPatternsGeneratedBlocks", []generatedBlock{
		{"command-tree", fenced(catalog.RenderCommandTree(root))},
		{"guide-topics", fenced(catalog.RenderGuideTopics(guide.Index()))},
		{"table-rule-manifest-fields", fenced(tableRuleManifestFields(t))},
	})
}

// TestCommandsGeneratedBlocks pins enumerations in the command-to-file map to
// the registries they describe. -update rewrites only the marked block.
func TestCommandsGeneratedBlocks(t *testing.T) {
	testGeneratedBlocks(t, commandsPath, "TestCommandsGeneratedBlocks", []generatedBlock{
		{"explain-schema-sources", explainSchemaSources(t)},
	})
}
