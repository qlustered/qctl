package catalog_test

import (
	"bytes"
	"errors"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Nothing in this repository cites source by line number: not a comment, not
// a resource page, not a sidecar note, not a doc. A line number is true for
// one revision of the file it names — for atlas, a sibling checkout nothing
// here pins — and then goes on reading as authoritative while it points at
// unrelated code. Cite the file and the symbol instead
// (`cettings/.../quarantine.py` `get_quarantine_count_by_status`); grep finds
// it in one step at any revision. A position inside a long function is named
// by what is there — "the 422 raise in `delete_bad_rows_core`" — never by
// where it is.

var (
	// lineCitation is a file name followed by a colon and a line number,
	// range or comma-joined list of them, or by a `#L` anchor.
	lineCitation = regexp.MustCompile(
		`(?:[\w./@-]*\w\.(?:py|go|ts|tsx|js|jsx|md|yaml|yml|json|sql|sh|toml|mod)|\bMakefile)` +
			`(?::|#L)L?\d+(?:[-–]\d+)?(?:,\d+(?:[-–]\d+)?)*`)

	// continuedCitation is a colon and a line number standing alone, the way a
	// citation continues after its file was named once. The leading character
	// keeps Go slicing (`hex[:8]`), JSON (`"count":5`) and clock times out of
	// it; citationsIn checks the trailing one.
	continuedCitation = regexp.MustCompile("(?:^|[\\s(,;`/~]):\\d+(?:[-–]\\d+)?(?:,\\d+(?:[-–]\\d+)?)*")
)

// citationsIn returns p's matches in line. A continuedCitation match counts
// only when no word character follows it, which RE2 cannot say.
func citationsIn(p *regexp.Regexp, line string) []string {
	var out []string
	for _, loc := range p.FindAllStringIndex(line, -1) {
		if p == continuedCitation && loc[1] < len(line) {
			if c := line[loc[1]]; c == '_' || c >= '0' && c <= '9' ||
				c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
				continue
			}
		}
		out = append(out, strings.TrimSpace(line[loc[0]:loc[1]]))
	}
	return out
}

// lineCitationExempt names the tracked files the sweep does not read.
// Generated output is rewritten wholesale by its generator, and go.sum is
// hashes.
func lineCitationExempt(path string) bool {
	return strings.HasSuffix(path, ".gen.go") ||
		strings.HasSuffix(path, ".gen.ts") ||
		path == "go.sum" ||
		strings.HasPrefix(path, "graphify-out/")
}

// trackedFiles lists the repository's git-tracked files, relative to its
// root. It asks git rather than walking the tree, which would also read
// ignored scratch files, build output and local notes. It skips the test when
// the root has no .git, as in a tree exported with git archive.
func trackedFiles(t *testing.T) []string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repositoryRoot, ".git")); errors.Is(err, fs.ErrNotExist) {
		t.Skip("not a git checkout: there are no tracked files to read")
	}
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = repositoryRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" && !lineCitationExempt(f) {
			files = append(files, f)
		}
	}
	if len(files) < 500 {
		t.Fatalf("git ls-files listed only %d files -- the sweep is not reading the repository", len(files))
	}
	readGitIndex(t)
	return files
}

// readGitIndex reads the git index from the test process. go test's result
// cache hashes the files a test opens itself, not what a subprocess reads, so
// without this a newly tracked file never invalidates a cached pass.
func readGitIndex(t *testing.T) {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--git-path", "index")
	cmd.Dir = repositoryRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse --git-path index: %v", err)
	}
	index := strings.TrimSpace(string(out))
	if !filepath.IsAbs(index) {
		index = filepath.Join(repositoryRoot, index)
	}
	if _, err := os.ReadFile(index); err != nil {
		t.Fatalf("read git index: %v", err)
	}
}

// lineCitationHit is one citation, located for the failure message.
type lineCitationHit struct {
	line int
	text string
}

// goLineCitations finds citations in a Go file's comments and string
// literals. Code itself is not read: `args[:1]` is a slice expression.
// A string literal is checked for the path form only, since a bare colon and
// number in one is as likely data as prose.
func goLineCitations(src []byte) []lineCitationHit {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, scanner.ScanComments)
	var hits []lineCitationHit
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return hits
		}
		var patterns []*regexp.Regexp
		switch tok {
		case token.COMMENT:
			patterns = []*regexp.Regexp{lineCitation, continuedCitation}
		case token.STRING:
			patterns = []*regexp.Regexp{lineCitation}
		default:
			continue
		}
		start := fset.Position(pos).Line
		for i, line := range strings.Split(lit, "\n") {
			for _, p := range patterns {
				for _, m := range citationsIn(p, line) {
					hits = append(hits, lineCitationHit{start + i, m})
				}
			}
		}
	}
}

// textLineCitations finds citations anywhere in a non-Go text file.
func textLineCitations(src []byte) []lineCitationHit {
	var hits []lineCitationHit
	for i, line := range strings.Split(string(src), "\n") {
		for _, p := range []*regexp.Regexp{lineCitation, continuedCitation} {
			for _, m := range citationsIn(p, line) {
				hits = append(hits, lineCitationHit{i + 1, m})
			}
		}
	}
	return hits
}

// TestNoLineNumberCitations fails on every line-number citation in a tracked
// file. There is no allowlist and no -update: the fix is always to name the
// symbol.
func TestNoLineNumberCitations(t *testing.T) {
	for _, path := range trackedFiles(t) {
		src, err := os.ReadFile(filepath.Join(repositoryRoot, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.IndexByte(src, 0) >= 0 {
			continue // binary
		}
		var hits []lineCitationHit
		if strings.HasSuffix(path, ".go") {
			hits = goLineCitations(src)
		} else {
			hits = textLineCitations(src)
		}
		for _, h := range hits {
			t.Errorf("%s:%d: line-number citation %q -- cite the file and the symbol, not a line",
				path, h.line, h.text)
		}
	}
}

// spellCitation spells a citation with @ in place of the colon, so this file's own
// examples do not trip the sweep that reads it.
func spellCitation(s string) string { return strings.ReplaceAll(s, "@", ":") }

// TestLineCitationPatterns pins what the sweep counts as a citation, both
// ways: a pattern that stopped matching would pass every tree.
func TestLineCitationPatterns(t *testing.T) {
	for _, comment := range []string{
		spellCitation("// see data_view_core.py@2563"),
		spellCitation("// cettings/cettings/controllers/quarantine.py@3321-3324 counts them"),
		spellCitation("// cobra's command.go@1150 runs it"),
		spellCitation("// the raise (`@2317`) fires first"),
		spellCitation("// cited as data_view_core.py and then @2587, too"),
		spellCitation("// summary.go@228,248,256 each read it"),
		spellCitation("// the loop (~@737-745) runs it"),
		spellCitation("// the deleted file (Makefile@105)"),
		"// src/client/sdk.gen.ts" + "#L891",
	} {
		if len(goLineCitations([]byte(comment+"\n"))) == 0 {
			t.Errorf("goLineCitations(%q) found nothing", comment)
		}
	}
	for _, text := range []string{
		spellCitation("- the route (`capi/capi/views/rule_view.py@918`) deletes rules"),
		spellCitation("unrepresentable: the filter at @2684 and the count at @3321"),
	} {
		if len(textLineCitations([]byte(text))) == 0 {
			t.Errorf("textLineCitations(%q) found nothing", text)
		}
	}
	if len(goLineCitations([]byte(spellCitation("var s = \"see quarantine.py@12\"\n")))) == 0 {
		t.Error("a string literal's path citation went unseen")
	}

	for _, src := range []string{
		"x := hex[:8]\ny := args[:1]\n",
		"// server: http://localhost:8000\n",
		"// parsed \"yaml: line 3: ...\" from line 7\n",
		`body := ` + "`" + `{"count":5,"at":"2026-09-15T12:00:00Z"}` + "`\n",
		"// cite `quarantine.py` `get_quarantine_count_by_status`\n",
		"s := \"at :8 past\"\n",
	} {
		if hits := goLineCitations([]byte(src)); len(hits) != 0 {
			t.Errorf("goLineCitations(%q) = %v, want nothing", src, hits)
		}
	}
}
