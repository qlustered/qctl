package goconstcheck_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	goconst "github.com/jgautheron/goconst"
	"gopkg.in/yaml.v3"
)

// goconst, as golangci-lint runs it, reports a duplicated string at ONE of
// its occurrences, and which one is decided by goroutine scheduling: it walks
// a package's files concurrently and anchors the issue at whichever
// occurrence it counted first. That is always the first occurrence in some
// file, test files included. So `make lint` passes or fails on the same tree
// depending on whether the occurrence it happened to pick is suppressed (a
// _test.go file, a //nolint:goconst, a generated file) or not.
//
// This test enumerates every position goconst could report and fails if any
// of them is unsuppressed, which makes the goconst half of `make lint`
// deterministic: green here means green there on every run.

// goconstSettings mirrors the goconst settings golangci-lint reads from
// .golangci.yml, with golangci-lint's defaults for keys the file leaves out.
type goconstSettings struct {
	MinLen       int      `yaml:"min-len"`
	MinOccur     int      `yaml:"min-occurrences"`
	IgnoreValues []string `yaml:"ignore-string-values"`
	IgnoreCalls  *bool    `yaml:"ignore-calls"`
	Numbers      bool     `yaml:"numbers"`
	NumberMin    int      `yaml:"min"`
	NumberMax    int      `yaml:"max"`
}

type exclusionRule struct {
	Linters    []string `yaml:"linters"`
	Path       string   `yaml:"path"`
	PathExcept string   `yaml:"path-except"`
	Text       string   `yaml:"text"`
	Source     string   `yaml:"source"`
}

type lintConfig struct {
	Run struct {
		Tests *bool `yaml:"tests"`
	} `yaml:"run"`
	Linters struct {
		Settings struct {
			Goconst goconstSettings `yaml:"goconst"`
		} `yaml:"settings"`
		Exclusions struct {
			Rules []exclusionRule `yaml:"rules"`
			Paths []string        `yaml:"paths"`
		} `yaml:"exclusions"`
	} `yaml:"linters"`
}

// excluder decides whether golangci-lint drops a goconst issue at a position
// for reasons other than a nolint directive.
type excluder struct {
	root  string
	paths []*regexp.Regexp
	rules []exclusionRule
}

func (e excluder) excluded(t *testing.T, file string) bool {
	rel, err := filepath.Rel(e.root, file)
	if err != nil {
		t.Fatal(err)
	}
	rel = filepath.ToSlash(rel)
	for _, re := range e.paths {
		if re.MatchString(rel) {
			return true
		}
	}
	for _, r := range e.rules {
		if regexp.MustCompile(r.Path).MatchString(rel) &&
			(r.PathExcept == "" || !regexp.MustCompile(r.PathExcept).MatchString(rel)) {
			return true
		}
	}
	return false
}

func loadConfig(t *testing.T, root string) (*goconst.Config, excluder, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var lc lintConfig
	lc.Linters.Settings.Goconst = goconstSettings{MinLen: 3, MinOccur: 3, NumberMin: 3, NumberMax: 3}
	if err := yaml.Unmarshal(raw, &lc); err != nil {
		t.Fatalf("parse .golangci.yml: %v", err)
	}
	s := lc.Linters.Settings.Goconst
	cfg := &goconst.Config{
		IgnoreStrings:   s.IgnoreValues,
		MinStringLength: s.MinLen,
		MinOccurrences:  s.MinOccur,
		ParseNumbers:    s.Numbers,
		NumberMin:       s.NumberMin,
		NumberMax:       s.NumberMax,
		ExcludeTypes:    map[goconst.Type]bool{goconst.Call: s.IgnoreCalls == nil || *s.IgnoreCalls},
	}

	ex := excluder{root: root}
	for _, p := range lc.Linters.Exclusions.Paths {
		ex.paths = append(ex.paths, regexp.MustCompile(p))
	}
	for _, r := range lc.Linters.Exclusions.Rules {
		if !slices.Contains(r.Linters, "goconst") {
			continue
		}
		if r.Text != "" || r.Source != "" || r.Path == "" {
			t.Fatalf(".golangci.yml has a goconst exclusion rule this test cannot model "+
				"(only path and path-except are supported): %+v", r)
		}
		ex.rules = append(ex.rules, r)
	}
	tests := lc.Run.Tests == nil || *lc.Run.Tests
	return cfg, ex, tests
}

// nolintRange is a span of lines a nolint directive covers for goconst.
type nolintRange struct {
	from, to, col int
}

var nolintPattern = regexp.MustCompile(`^nolint( |:|$)`)

// coversGoconst reports whether one comment, as golangci-lint parses it,
// suppresses goconst.
func coversGoconst(text string) bool {
	text = strings.TrimLeft(text, "/ ")
	if !nolintPattern.MatchString(text) {
		return false
	}
	if strings.HasPrefix(text, "nolint:all") || !strings.HasPrefix(text, "nolint:") {
		return true
	}
	text, _, _ = strings.Cut(text, "//")
	for item := range strings.SplitSeq(strings.TrimPrefix(text, "nolint:"), ",") {
		if name := strings.ToLower(strings.TrimSpace(item)); name == "goconst" || name == "all" {
			return true
		}
	}
	return false
}

// nolintRanges returns the line spans in f where golangci-lint drops a
// goconst issue: each comment group holding a matching directive, widened to
// the whole node that starts on the next line at the group's column.
func nolintRanges(fset *token.FileSet, f *ast.File) []nolintRange {
	var inline []nolintRange
	for _, g := range f.Comments {
		for _, c := range g.List {
			if coversGoconst(c.Text) {
				p := fset.Position(g.Pos())
				inline = append(inline, nolintRange{from: p.Line, to: fset.Position(g.End()).Line, col: p.Column})
				break
			}
		}
	}
	if len(inline) == 0 {
		return nil
	}
	ranges := slices.Clone(inline)
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		start := fset.Position(n.Pos())
		end := fset.Position(n.End()).Line
		for _, r := range inline {
			if r.to == start.Line-1 && r.col == start.Column {
				ranges = append(ranges, nolintRange{from: r.from, to: max(r.to, end), col: r.col})
				break
			}
		}
		return true
	})
	return ranges
}

type pkgFiles struct {
	Dir         string
	GoFiles     []string
	TestGoFiles []string
}

// listPackages returns every package's non-test and in-package test files.
// External test packages are left out: every file in one is a _test.go file.
func listPackages(t *testing.T, root string) []pkgFiles {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", `{{.Dir}}|{{join .GoFiles ","}}|{{join .TestGoFiles ","}}`, "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	var pkgs []pkgFiles
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "|")
		p := pkgFiles{Dir: parts[0]}
		if parts[1] != "" {
			p.GoFiles = strings.Split(parts[1], ",")
		}
		if parts[2] != "" {
			p.TestGoFiles = strings.Split(parts[2], ",")
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

func TestEveryGoconstCandidateIsSuppressed(t *testing.T) {
	root := moduleRoot(t)
	cfg, ex, withTests := loadConfig(t, root)
	perFile := *cfg
	perFile.MinOccurrences = 1

	var failures []string
	for _, pkg := range listPackages(t, root) {
		names := pkg.GoFiles
		if withTests {
			names = append(slices.Clone(names), pkg.TestGoFiles...)
		}
		if len(names) == 0 {
			continue
		}
		fset := token.NewFileSet()
		files := make([]*ast.File, 0, len(names))
		for _, name := range names {
			f, err := parser.ParseFile(fset, filepath.Join(pkg.Dir, name), nil, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}

		issues, err := goconst.Run(files, fset, nil, cfg)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, is := range issues {
			if is.OccurrencesCount > 0 {
				counts[is.Str] = is.OccurrencesCount
			}
		}
		if len(counts) == 0 {
			continue
		}

		unsuppressed := map[string][]string{}
		for _, f := range files {
			name := fset.Position(f.Pos()).Filename
			if strings.HasSuffix(name, "_test.go") || ast.IsGenerated(f) || ex.excluded(t, name) {
				continue
			}
			firsts, err := goconst.Run([]*ast.File{f}, fset, nil, &perFile)
			if err != nil {
				t.Fatal(err)
			}
			ranges := nolintRanges(fset, f)
			for _, is := range firsts {
				if _, flagged := counts[is.Str]; !flagged {
					continue
				}
				if slices.ContainsFunc(ranges, func(r nolintRange) bool {
					return r.from <= is.Pos.Line && is.Pos.Line <= r.to
				}) {
					continue
				}
				rel, _ := filepath.Rel(root, is.Pos.Filename)
				unsuppressed[is.Str] = append(unsuppressed[is.Str], fmt.Sprintf("%s:%d", rel, is.Pos.Line))
			}
		}
		for str, at := range unsuppressed {
			failures = append(failures, fmt.Sprintf("string %q has %d occurrences in %s; goconst can report it at\n\t%s",
				str, counts[str], must(filepath.Rel(root, pkg.Dir)), strings.Join(at, "\n\t")))
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		t.Errorf("make lint fails on some runs and not others: goconst reports each duplicated string "+
			"at the first occurrence in whichever file it happened to scan first, test files included.\n"+
			"Put //nolint:goconst on each line below, or replace the literal with a constant.\n\n%s",
			strings.Join(failures, "\n\n"))
	}
}

func must(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}
