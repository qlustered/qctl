package catalog_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
)

// parentFilesHeading opens the one section of docs/commands.md that
// parentCommandRows reads.
const parentFilesHeading = "## Parent Command Files"

// parentRow is one data row of the Parent Command Files table.
type parentRow struct {
	line      int    // 1-based line in the doc, for the failure message
	parent    string // first cell without its backticks: "alert", or "root"
	registers string // third cell as written
}

// parentCommandRows returns the data rows of the table under the "Parent
// Command Files" heading, and nothing from the file's other tables: unlike
// fileMapCommands it reads the third cell, and the Domain Client Packages
// table directly above has backticked first cells too.
//
// It is strict where fileMapCommands is lenient. That helper can afford noise
// because its test only asks whether a real path is present; this one reads
// cell contents, so a row it cannot parse is an error, never a skipped line.
func parentCommandRows(doc string) ([]parentRow, error) {
	lines := strings.Split(doc, "\n")
	start := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, parentFilesHeading) })
	if start < 0 {
		return nil, fmt.Errorf("no %q heading", parentFilesHeading)
	}

	var rows []parentRow
	tableLine := 0 // table lines seen so far: 1 is the header, 2 the separator
	for i := start + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "## ") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			// Markdown makes the leading pipe optional, so a row written
			// without one still renders as a row -- and skipping it would drop
			// it from the check with nothing to say so.
			if strings.Contains(line, " | ") {
				return nil, fmt.Errorf("line %d: a table row must start with \"|\": %s", i+1, line)
			}
			continue
		}
		tableLine++
		if tableLine <= 2 {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|"), "|")
		if len(cells) != 3 {
			return nil, fmt.Errorf("line %d: want 3 cells (Parent | File | Registers), got %d: %s", i+1, len(cells), line)
		}
		parent := strings.TrimSpace(cells[0])
		if len(parent) < 3 || !strings.HasPrefix(parent, "`") || !strings.HasSuffix(parent, "`") {
			return nil, fmt.Errorf("line %d: the Parent cell must be one backticked command name, got %q", i+1, parent)
		}
		rows = append(rows, parentRow{
			line:      i + 1,
			parent:    strings.Trim(parent, "`"),
			registers: strings.TrimSpace(cells[2]),
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no table rows under %q", parentFilesHeading)
	}
	return rows, nil
}

// parentRowProblems checks rows against the tree, where children maps every
// command's name as the table spells it ("root" for qctl itself, "cache
// aliases" for a nested group) to its visible subcommands, and required names
// the parents that must have a row.
//
// A Registers cell beginning "All " is prose and is exempt from the
// comparison. Any other cell is an enumeration and must name exactly the
// parent's visible subcommands, in any order: the doc lists verbs in an order
// chosen by hand, and cobra returns them alphabetically. Requiring the doc to
// be alphabetized would trade that ordering for nothing. A verb listed twice
// still fails.
func parentRowProblems(rows []parentRow, children map[string][]string, required []string) []string {
	var problems []string
	has := map[string]bool{}
	for _, r := range rows {
		has[r.parent] = true
		kids, ok := children[r.parent]
		if !ok {
			// Checked before the exemption: an "All foo subcommands" row for
			// a foo that no longer exists is the same stale list.
			problems = append(problems, fmt.Sprintf("line %d: row `%s` names no command in the tree — fix the name or delete the row",
				r.line, r.parent))
			continue
		}
		if strings.HasPrefix(r.registers, "All ") {
			continue
		}
		var doc []string
		for _, name := range strings.Split(r.registers, ",") {
			doc = append(doc, strings.TrimSpace(name))
		}
		want := slices.Sorted(slices.Values(kids))
		got := slices.Sorted(slices.Values(doc))
		if slices.Equal(got, want) {
			continue
		}
		problems = append(problems, fmt.Sprintf("line %d: row `%s` says %q, but the tree has: %s%s",
			r.line, r.parent, r.registers, strings.Join(want, ", "), setDelta(got, want)))
	}
	for _, p := range required {
		if !has[p] {
			problems = append(problems, fmt.Sprintf("`%s` registers subcommands (%s) but has no row",
				p, strings.Join(slices.Sorted(slices.Values(children[p])), ", ")))
		}
	}
	return problems
}

// setDelta renders what an enumeration is missing, has extra, and repeats, so
// the row can be fixed from the message alone. Both inputs are sorted.
func setDelta(doc, tree []string) string {
	var missing, extra, repeated []string
	for _, t := range tree {
		if !slices.Contains(doc, t) {
			missing = append(missing, t)
		}
	}
	for i, d := range doc {
		if !slices.Contains(tree, d) {
			extra = append(extra, d)
		}
		if i > 0 && doc[i-1] == d && (len(repeated) == 0 || repeated[len(repeated)-1] != d) {
			repeated = append(repeated, d)
		}
	}
	var b strings.Builder
	if len(missing) > 0 {
		b.WriteString("; the row is missing: " + strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		b.WriteString("; not in the tree: " + strings.Join(extra, ", "))
	}
	if len(repeated) > 0 {
		b.WriteString("; listed more than once: " + strings.Join(repeated, ", "))
	}
	return b.String()
}

// TestParentCommandFilesMatchTheTree: CLAUDE.md step 5 sends every session
// that adds a subcommand to docs/commands.md's Parent Command Files table, and
// its Registers column is a hand-transcribed list of what the CLI has.
// warning's row still read "submit, retract" after the commit that registered
// `warning resolve`, with no test objecting, because fileMapCommands reads
// first cells only; it was caught by hand before the PR merged.
//
// Every top-level command with subcommands must have a row, every row must
// name a real command, and every enumerating row must list its parent's
// subcommands exactly. There is no -update: whether a row enumerates or says
// "All ..." is a human's choice, and a rewrite would turn prose into a list
// and call that a fix.
func TestParentCommandFilesMatchTheTree(t *testing.T) {
	raw, err := os.ReadFile(commandsPath)
	if err != nil {
		t.Fatalf("read %s: %v", commandsPath, err)
	}
	rows, err := parentCommandRows(string(raw))
	if err != nil {
		// Fatal, never a pass over zero rows: a renamed heading would
		// otherwise switch this whole check off at exit 0.
		t.Fatalf("%s: %v (if the section was renamed, update parentFilesHeading)", commandsPath, err)
	}

	root := realRoot(t)
	children := map[string][]string{}
	for _, n := range catalog.Walk(root) {
		name := strings.TrimPrefix(n.Path, "qctl ")
		if n.Cmd == root {
			name = "root"
		}
		children[name] = []string{}
		for _, c := range catalog.VisibleChildren(n.Cmd) {
			children[name] = append(children[name], c.Name())
		}
	}
	required := []string{"root"}
	for _, c := range catalog.VisibleChildren(root) {
		if len(catalog.VisibleChildren(c)) > 0 {
			required = append(required, c.Name())
		}
	}

	if problems := parentRowProblems(rows, children, required); len(problems) > 0 {
		slices.Sort(problems)
		t.Errorf("docs/commands.md's Parent Command Files table drifted from the command tree:\n  %s\n\n"+
			"Fix the row by hand. An enumeration may list verbs in any order; a Registers cell "+
			"beginning \"All \" is not compared.", strings.Join(problems, "\n  "))
	}
}

// TestParentRowProblems pins the comparison on inputs the live doc may stop
// exercising. Today five enumerating rows are out of alphabetical order, which
// is what proves the check compares sets — but nothing keeps them that way.
func TestParentRowProblems(t *testing.T) {
	children := map[string][]string{
		"root":  {"alert", "get"},
		"alert": {"resolve", "retract", "submit"},
		"get":   {"alerts", "tables"},
	}
	required := []string{"root", "alert", "get"}
	row := func(parent, registers string) parentRow {
		return parentRow{line: 7, parent: parent, registers: registers}
	}

	for _, tc := range []struct {
		name string
		rows []parentRow
		want []string // substrings of the single problem; nil means no problems
	}{
		{
			name: "an enumeration in its own order passes",
			rows: []parentRow{row("root", "All top-level verbs"), row("alert", "submit, resolve, retract"), row("get", "All get subcommands")},
		},
		{
			name: "an All row is not compared",
			rows: []parentRow{row("root", "All top-level verbs"), row("alert", "All alert subcommands"), row("get", "All of them, eventually")},
		},
		{
			name: "a subcommand missing from the row",
			rows: []parentRow{row("root", "All top-level verbs"), row("alert", "submit, retract"), row("get", "All get subcommands")},
			want: []string{"line 7", "`alert`", `"submit, retract"`, "the tree has: resolve, retract, submit", "missing: resolve"},
		},
		{
			name: "a row naming a subcommand the tree lacks",
			rows: []parentRow{row("root", "All top-level verbs"), row("alert", "resolve, submit, retract, delete"), row("get", "All get subcommands")},
			want: []string{"`alert`", "the tree has: resolve, retract, submit", "not in the tree: delete"},
		},
		{
			name: "a subcommand listed twice",
			rows: []parentRow{row("root", "All top-level verbs"), row("alert", "resolve, submit, resolve, retract"), row("get", "All get subcommands")},
			want: []string{"`alert`", "listed more than once: resolve"},
		},
		{
			name: "an All row whose parent is gone",
			rows: []parentRow{row("root", "All top-level verbs"), row("alert", "resolve, retract, submit"), row("get", "All get subcommands"), row("warning", "All warning subcommands")},
			want: []string{"line 7", "`warning`", "names no command"},
		},
		{
			name: "a parent with no row",
			rows: []parentRow{row("root", "All top-level verbs"), row("get", "All get subcommands")},
			want: []string{"`alert`", "resolve, retract, submit", "has no row"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parentRowProblems(tc.rows, children, required)
			if tc.want == nil {
				if len(got) > 0 {
					t.Fatalf("want no problems, got %q", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want exactly one problem, got %q", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got[0], w) {
					t.Errorf("problem %q does not contain %q", got[0], w)
				}
			}
		})
	}
}

// TestParentCommandRowsReadsOnlyItsSection: the table above this one in the
// real doc has backticked first cells too, and a parser that wandered into it
// would compare a package name against the tree.
func TestParentCommandRowsReadsOnlyItsSection(t *testing.T) {
	const doc = "## Domain Client Packages\n\n" +
		"| Package | Path |\n|---|---|\n| `alerts` | `internal/alerts/client.go` |\n\n" +
		"## Parent Command Files (register subcommands)\n\n" +
		"Each verb has a parent file that registers all subcommands:\n\n" +
		"| Parent | File | Registers |\n|---|---|---|\n" +
		"| `root` | `internal/commands/root/root.go` | All top-level verbs |\n" +
		"| `alert` | `internal/commands/alert/alert.go` | resolve, submit, retract |\n\n" +
		"## Later\n\n| `later` | `x.go` | y |\n"

	rows, err := parentCommandRows(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []parentRow{
		{line: 13, parent: "root", registers: "All top-level verbs"},
		{line: 14, parent: "alert", registers: "resolve, submit, retract"},
	}
	if !slices.Equal(rows, want) {
		t.Errorf("got %+v, want %+v", rows, want)
	}

	for name, bad := range map[string]string{
		"no heading":                 "## Domain Client Packages\n\n| `alerts` | `a.go` | b |\n",
		"no rows":                    "## Parent Command Files\n\nprose only\n\n## Later\n",
		"a missing cell":             "## Parent Command Files\n\n| Parent | File | Registers |\n|---|---|---|\n| `alert` | resolve |\n",
		"a row with no leading pipe": "## Parent Command Files\n\n| Parent | File | Registers |\n|---|---|---|\n`alert` | `a.go` | resolve |\n",
		"an unquoted cell":           "## Parent Command Files\n\n| Parent | File | Registers |\n|---|---|---|\n| alert | `a.go` | resolve |\n",
	} {
		t.Run(name, func(t *testing.T) {
			if rows, err := parentCommandRows(bad); err == nil {
				t.Errorf("want an error, got rows %+v", rows)
			}
		})
	}
}
