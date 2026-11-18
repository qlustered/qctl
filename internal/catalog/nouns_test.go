package catalog

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/resources"
	"github.com/spf13/cobra"
)

func TestPlural(t *testing.T) {
	cases := map[string]string{
		"table":              "tables",
		"cloud-source":       "cloud-sources",
		"data":               "data",
		"history":            "history",
		"backup-settings":    "backup-settings",
		"table-series":       "table-series",
		"copy-history":       "copy-histories",
		"dataset-copy":       "dataset-copies",
		"data-review-policy": "data-review-policies",
		"key":                "keys",
		"inbox":              "inboxes",
		"alias":              "aliases",
		"class":              "classes",
		"buzz":               "buzzes",
		"batch":              "batches",
		"push":               "pushes",
	}
	for in, want := range cases {
		if got := Plural(in); got != want {
			t.Errorf("Plural(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNounsOf(t *testing.T) {
	cases := []struct {
		name           string
		aliases        []string
		singular, plur []string
	}{
		{"table", []string{"dataset"}, []string{"table", "dataset"}, []string{"tables", "datasets"}},
		// An alias ending in "s" is a plural: its singular is derived, not pluralized again.
		{"notification", []string{"notifications", "inbox"}, []string{"notification", "inbox"}, []string{"notifications", "inboxes"}},
		{"rule-merge", []string{"rule-merges"}, []string{"rule-merge"}, []string{"rule-merges"}},
		{"table-copy", []string{"dataset-copies"}, []string{"table-copy", "dataset-copy"}, []string{"table-copies", "dataset-copies"}},
		{"widget", []string{"boxes", "glasses"}, []string{"widget", "box", "glass"}, []string{"widgets", "boxes", "glasses"}},
		// An uncountable name or alias is in both lists, even one ending in "s".
		{"data", []string{"rows"}, []string{"data", "row"}, []string{"data", "rows"}},
		{"table-series", nil, []string{"table-series"}, []string{"table-series"}},
		{"widget", []string{"backup-settings"}, []string{"widget", "backup-settings"}, []string{"widgets", "backup-settings"}},
		// Aliases are trimmed and lowercased; an empty one is dropped.
		{"table", []string{" Dataset ", ""}, []string{"table", "dataset"}, []string{"tables", "datasets"}},
	}
	for _, tc := range cases {
		got := NounsOf(tc.name, tc.aliases)
		if !reflect.DeepEqual(got.Singular, tc.singular) || !reflect.DeepEqual(got.Plural, tc.plur) {
			t.Errorf("NounsOf(%q, %q) = %v / %v, want %v / %v",
				tc.name, tc.aliases, got.Singular, got.Plural, tc.singular, tc.plur)
		}
	}

	all := NounsOf("data", []string{"rows"}).All()
	if want := []string{"data", "row", "rows"}; !reflect.DeepEqual(all, want) {
		t.Errorf("All() = %v, want %v (singular first, no repeats)", all, want)
	}
}

// TestPageAliasPluralsRoundTrip: NounsOf derives a singular from every page
// alias ending in "s". The derivation is a heuristic ("aliases" would come out
// as "aliase"), so each real alias must survive the round trip back.
func TestPageAliasPluralsRoundTrip(t *testing.T) {
	pages, err := resources.Pages()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		for _, a := range p.Aliases {
			if !strings.HasSuffix(a, "s") || uncountable[a] {
				continue
			}
			if back := Plural(singular(a)); back != a {
				t.Errorf("%s.md alias %q reads as singular %q, whose plural is %q", p.Name, a, singular(a), back)
			}
		}
	}
}

func nounPages() []resources.Page {
	return []resources.Page{
		{Name: "widget", Aliases: []string{"gadget"}},
		{Name: "box", Aliases: []string{"crates"}},
	}
}

func leaf(use string) *cobra.Command {
	return &cobra.Command{Use: use, Run: func(*cobra.Command, []string) {}}
}

func group(use string, children ...*cobra.Command) *cobra.Command {
	g := &cobra.Command{Use: use}
	g.AddCommand(children...)
	return g
}

func find(t *testing.T, root *cobra.Command, args ...string) string {
	t.Helper()
	c, _, err := root.Find(args)
	if err != nil {
		return "error: " + err.Error()
	}
	return c.CommandPath()
}

func aliasesOf(t *testing.T, root *cobra.Command, args ...string) []string {
	t.Helper()
	c, _, err := root.Find(args)
	if err != nil || c.Name() != args[len(args)-1] {
		t.Fatalf("no command %q", strings.Join(args, " "))
	}
	return c.Aliases
}

func TestWireAliasesSplitsAPairByNumber(t *testing.T) {
	root := group("qctl", group("get", leaf("widget <id>"), leaf("widgets")))
	wireAliasesWith(root, nounPages())

	if got := aliasesOf(t, root, "get", "widget"); !reflect.DeepEqual(got, []string{"gadget"}) {
		t.Errorf("get widget aliases = %v, want [gadget]", got)
	}
	if got := aliasesOf(t, root, "get", "widgets"); !reflect.DeepEqual(got, []string{"gadgets"}) {
		t.Errorf("get widgets aliases = %v, want [gadgets]", got)
	}
	if got := find(t, root, "get", "gadget", "7"); got != "qctl get widget" {
		t.Errorf("get gadget 7 -> %q, want qctl get widget", got)
	}
	if got := find(t, root, "get", "gadgets"); got != "qctl get widgets" {
		t.Errorf("get gadgets -> %q, want qctl get widgets", got)
	}
}

func TestWireAliasesGivesALoneCommandBothNumbers(t *testing.T) {
	root := group("qctl",
		group("describe", leaf("widget <id>")),
		group("get", leaf("boxes")),
		group("cache", group("store", leaf("widgets"))),
	)
	wireAliasesWith(root, nounPages())

	for _, tc := range []struct {
		path []string
		want []string
	}{
		{[]string{"describe", "widget"}, []string{"gadget", "widgets", "gadgets"}},
		{[]string{"get", "boxes"}, []string{"box", "crate", "crates"}},
		// Any depth below a top-level command qualifies.
		{[]string{"cache", "store", "widgets"}, []string{"widget", "gadget", "gadgets"}},
	} {
		if got := aliasesOf(t, root, tc.path...); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s aliases = %v, want %v", strings.Join(tc.path, " "), got, tc.want)
		}
	}
}

func TestWireAliasesSkipsATakenNoun(t *testing.T) {
	legacy := leaf("legacy")
	legacy.Aliases = []string{"crate"}
	boxes := leaf("boxes")
	boxes.Aliases = []string{"box"}
	root := group("qctl", group("get", boxes, legacy))
	wireAliasesWith(root, nounPages())

	// "crate" is the sibling's alias and "box" is already this command's own.
	if got := aliasesOf(t, root, "get", "boxes"); !reflect.DeepEqual(got, []string{"box", "crates"}) {
		t.Errorf("get boxes aliases = %v, want [box crates]", got)
	}
	if got := find(t, root, "get", "crate"); got != "qctl get legacy" {
		t.Errorf("get crate -> %q, want qctl get legacy", got)
	}
}

func TestWireAliasesLeavesTopLevelCommandsAlone(t *testing.T) {
	root := group("qctl", group("widget", leaf("list")), group("get", leaf("widget")))
	wireAliasesWith(root, nounPages())

	if got := aliasesOf(t, root, "widget"); len(got) != 0 {
		t.Errorf("top-level widget aliases = %v, want none", got)
	}
	if got := aliasesOf(t, root, "get", "widget"); len(got) == 0 {
		t.Error("get widget gained no aliases; the fixture no longer exercises wiring")
	}
}

func TestWireAliasesIsIdempotent(t *testing.T) {
	root := group("qctl",
		group("get", leaf("widget"), leaf("widgets"), leaf("boxes")),
		group("describe", leaf("widget")),
	)
	snapshot := func() map[string][]string {
		out := map[string][]string{}
		for _, n := range Walk(root) {
			out[n.Path] = append([]string(nil), n.Cmd.Aliases...)
		}
		return out
	}
	wireAliasesWith(root, nounPages())
	first := snapshot()
	wireAliasesWith(root, nounPages())
	if second := snapshot(); !reflect.DeepEqual(first, second) {
		t.Errorf("second call changed aliases:\n first: %v\nsecond: %v", first, second)
	}
}

func TestResourceNamedIn(t *testing.T) {
	built := []Resource{
		{Page: resources.Page{Name: "table", Aliases: []string{"dataset"}}},
		{Page: resources.Page{Name: "data", Aliases: []string{"rows"}}},
	}
	for _, tc := range []struct{ word, want string }{
		{"table", "table"}, {"tables", "table"}, {" Datasets ", "table"},
		{"data", "data"}, {"row", "data"}, {"rows", "data"},
	} {
		r, ok := resourceNamedIn(built, tc.word)
		if !ok || r.Name != tc.want {
			t.Errorf("resourceNamedIn(%q) = %q, %v; want %q", tc.word, r.Name, ok, tc.want)
		}
	}
	if _, ok := resourceNamedIn(built, "widget"); ok {
		t.Error("resourceNamedIn(widget) matched; want no resource")
	}
}

// listShowTree uses real resource names: ListCommand and ShowCommand read the
// embedded pages.
func listShowTree() *cobra.Command {
	return group("qctl",
		group("get", leaf("table <id>"), leaf("tables"), leaf("files"), leaf("data <table>"),
			leaf("backup-settings"), leaf("value-recommendations")),
		group("describe", leaf("table <id>"), leaf("file <id>"), leaf("row <id>"), leaf("backup-settings <id>")),
		group("delete", leaf("file <id>")),
	)
}

func pathOf(c *cobra.Command) string {
	if c == nil {
		return "<nil>"
	}
	return c.CommandPath()
}

func TestListCommand(t *testing.T) {
	root := listShowTree()
	for _, tc := range []struct {
		from []string
		want string
	}{
		{[]string{"get", "table"}, "qctl get tables"},
		{[]string{"describe", "table"}, "qctl get tables"},
		{[]string{"delete", "file"}, "qctl get files"},
		{[]string{"describe", "row"}, "qctl get data"},
		{[]string{"describe", "backup-settings"}, "qctl get backup-settings"},
		{[]string{"get", "tables"}, "<nil>"}, // never cmd itself
		{[]string{"get", "data"}, "<nil>"},
		{[]string{"get", "value-recommendations"}, "<nil>"}, // not a resource noun
	} {
		cmd, _, err := root.Find(tc.from)
		if err != nil {
			t.Fatal(err)
		}
		if got := pathOf(ListCommand(cmd)); got != tc.want {
			t.Errorf("ListCommand(%s) = %s, want %s", cmd.CommandPath(), got, tc.want)
		}
	}
}

func TestShowCommand(t *testing.T) {
	root := listShowTree()
	for _, tc := range []struct {
		from []string
		want string
	}{
		{[]string{"get", "tables"}, "qctl get table"},
		{[]string{"describe", "table"}, "qctl get table"},
		// No `get file`, so describe is the fallback.
		{[]string{"get", "files"}, "qctl describe file"},
		{[]string{"delete", "file"}, "qctl describe file"},
		// `get data` names both numbers, so it is not a show command.
		{[]string{"get", "data"}, "qctl describe row"},
		{[]string{"get", "backup-settings"}, "qctl describe backup-settings"},
		{[]string{"get", "table"}, "qctl describe table"}, // never cmd itself
		{[]string{"describe", "file"}, "<nil>"},
		{[]string{"get", "value-recommendations"}, "<nil>"},
	} {
		cmd, _, err := root.Find(tc.from)
		if err != nil {
			t.Fatal(err)
		}
		if got := pathOf(ShowCommand(cmd)); got != tc.want {
			t.Errorf("ShowCommand(%s) = %s, want %s", cmd.CommandPath(), got, tc.want)
		}
	}
}

// A plural command that cannot run without arguments does not list anything.
func TestListCommandSkipsAPluralThatNeedsArguments(t *testing.T) {
	needsIDs := leaf("comment-threads <id>...")
	needsIDs.Args = cobra.MinimumNArgs(1)
	root := group("qctl",
		group("get", needsIDs),
		group("describe", leaf("comment-thread <id>")),
	)
	cmd, _, err := root.Find([]string{"describe", "comment-thread"})
	if err != nil {
		t.Fatal(err)
	}
	if got := pathOf(ListCommand(cmd)); got != "<nil>" {
		t.Errorf("ListCommand(%s) = %s, want <nil>", cmd.CommandPath(), got)
	}
	needsIDs.Args = cobra.ArbitraryArgs
	if got := pathOf(ListCommand(cmd)); got != "qctl get comment-threads" {
		t.Errorf("with ArbitraryArgs, ListCommand(%s) = %s, want qctl get comment-threads", cmd.CommandPath(), got)
	}
}
