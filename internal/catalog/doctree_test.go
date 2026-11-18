package catalog

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// treeFixture mirrors the shapes RenderCommandTree has to get right: a plain
// group with children, a generic -f dispatcher that ALSO has children, a leaf
// whose Use carries a positional spec, a bare leaf, a hidden command that must
// not appear, and a grandchild level (qctl's real `cache aliases ...`).
func treeFixture() *cobra.Command {
	root := &cobra.Command{Use: "qctl"}

	get := &cobra.Command{Use: "get", Annotations: Parent()}
	get.AddCommand(&cobra.Command{Use: "tables", Run: func(*cobra.Command, []string) {}})
	get.AddCommand(&cobra.Command{Use: "data <id>", Run: func(*cobra.Command, []string) {}})

	apply := &cobra.Command{Use: "apply", Annotations: Dispatcher([]string{"Table"}), Run: func(*cobra.Command, []string) {}}
	apply.AddCommand(&cobra.Command{Use: "table", Run: func(*cobra.Command, []string) {}})

	cache := &cobra.Command{Use: "cache", Annotations: Parent()}
	aliases := &cobra.Command{Use: "aliases", Annotations: Parent()}
	aliases.AddCommand(&cobra.Command{Use: "clear", Run: func(*cobra.Command, []string) {}})
	aliases.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})
	cache.AddCommand(aliases)

	root.AddCommand(get, apply, cache)
	root.AddCommand(&cobra.Command{Use: "explain RESOURCE[.FIELD]", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{Use: "version", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{Use: "secret", Hidden: true, Run: func(*cobra.Command, []string) {}})
	return root
}

func TestRenderCommandTreeShape(t *testing.T) {
	got := RenderCommandTree(treeFixture())
	// Names pad to the longest child + 2 ("explain"/"version" = 7, so 9), and
	// cobra sorts children alphabetically — hence "data, tables", not the order
	// they were added.
	want := strings.Join([]string{
		"qctl",
		"├── apply    -f <file>, table",
		"├── cache    aliases (clear, list)",
		"├── explain  RESOURCE[.FIELD]",
		"├── get      data, tables",
		"└── version",
		"",
	}, "\n")
	if got != want {
		t.Errorf("RenderCommandTree =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderCommandTreeOmitsHiddenAndHelp(t *testing.T) {
	root := treeFixture()
	root.InitDefaultHelpCmd()
	got := RenderCommandTree(root)
	if strings.Contains(got, "secret") {
		t.Error("hidden command rendered into the tree")
	}
	if strings.Contains(got, "help") {
		t.Error("cobra's generated help command rendered into the tree")
	}
}

// The tree is a file docs/patterns.md carries verbatim, so a long row must
// wrap at a fixed column and continue under the cell, never widen the block.
func TestRenderCommandTreeWrapsLongRows(t *testing.T) {
	root := &cobra.Command{Use: "qctl"}
	get := &cobra.Command{Use: "get", Annotations: Parent()}
	for _, n := range []string{
		"alpha-resources", "bravo-resources", "charlie-resources", "delta-resources",
		"echo-resources", "foxtrot-resources", "golf-resources", "hotel-resources",
	} {
		get.AddCommand(&cobra.Command{Use: n, Run: func(*cobra.Command, []string) {}})
	}
	// A second child so `get` is not the last row: the last row's continuation
	// is indented with spaces, and it is the branch rail this test is about.
	root.AddCommand(get, &cobra.Command{Use: "version", Run: func(*cobra.Command, []string) {}})

	lines := strings.Split(strings.TrimRight(RenderCommandTree(root), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected the row to wrap, got %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if utf8.RuneCountInString(l) > treeWrapWidth {
			t.Errorf("line exceeds treeWrapWidth (%d): %q", treeWrapWidth, l)
		}
	}
	// A continuation line hangs under the cell, aligned, with the branch rail.
	if !strings.HasPrefix(lines[2], "│   ") {
		t.Errorf("continuation line does not carry the branch rail: %q", lines[2])
	}
	firstCell := utf8.RuneCountInString(strings.SplitN(lines[1], "alpha-resources", 2)[0])
	continuedCell := utf8.RuneCountInString(lines[2]) - utf8.RuneCountInString(strings.TrimLeft(lines[2], "│ "))
	if continuedCell != firstCell {
		t.Errorf("continuation cell starts at column %d, want %d: %q", continuedCell, firstCell, lines[2])
	}
	if strings.TrimLeft(lines[2], "│ ") == "" {
		t.Errorf("continuation line is empty: %q", lines[2])
	}
}

// Rendering must be a pure function of the tree: the same tree must produce
// the same bytes, or -update would churn the doc on every run.
func TestRenderCommandTreeIsDeterministic(t *testing.T) {
	if a, b := RenderCommandTree(treeFixture()), RenderCommandTree(treeFixture()); a != b {
		t.Errorf("RenderCommandTree is not deterministic:\n%s\n---\n%s", a, b)
	}
}

func TestRenderGuideTopicsPadsToLongestName(t *testing.T) {
	got := RenderGuideTopics([]GuideTopic{
		{Name: "discover-capabilities", Summary: "Find out what qctl can do"},
		{Name: "run-jobs", Summary: "Trigger and watch jobs"},
	})
	want := "discover-capabilities  Find out what qctl can do\nrun-jobs               Trigger and watch jobs\n"
	if got != want {
		t.Errorf("RenderGuideTopics =\n%q\nwant\n%q", got, want)
	}
}

func TestVisibleChildrenSkipsHiddenAndDeprecated(t *testing.T) {
	root := &cobra.Command{Use: "qctl"}
	root.AddCommand(&cobra.Command{Use: "real", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{Use: "hidden", Hidden: true, Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{Use: "old", Deprecated: "use real", Run: func(*cobra.Command, []string) {}})

	got := VisibleChildren(root)
	if len(got) != 1 || got[0].Name() != "real" {
		names := make([]string, len(got))
		for i, c := range got {
			names[i] = c.Name()
		}
		t.Errorf("VisibleChildren = %v, want [real]", names)
	}
}
