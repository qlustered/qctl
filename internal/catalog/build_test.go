package catalog

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/resources"
	"github.com/spf13/cobra"
)

// buildTree mirrors the shapes Build has to get right: a tree verb, a
// dispatcher-conferred verb with no subcommand anywhere, a create-class
// resource, and a meta resource with no "get".
func buildTree() *cobra.Command {
	root := &cobra.Command{Use: "qctl", Annotations: Parent()}

	get := &cobra.Command{Use: "get", Annotations: Parent()}
	get.AddCommand(&cobra.Command{Use: "tables", Annotations: For("table"), Run: func(*cobra.Command, []string) {}})
	get.AddCommand(&cobra.Command{Use: "table <id>", Annotations: For("table"), Run: func(*cobra.Command, []string) {}})
	root.AddCommand(get)

	apply := &cobra.Command{Use: "apply", Annotations: Dispatcher([]string{"Table", "Rule"})}
	root.AddCommand(apply)

	create := &cobra.Command{Use: "create", Annotations: Parent()}
	create.AddCommand(&cobra.Command{Use: "dry-run-job", Annotations: For("dry-run-job"), Run: func(*cobra.Command, []string) {}})
	root.AddCommand(create)

	auth := &cobra.Command{Use: "auth", Annotations: Parent()}
	auth.AddCommand(&cobra.Command{Use: "login", Annotations: For("session"), Run: func(*cobra.Command, []string) {}})
	root.AddCommand(auth)

	return root
}

// buildPages is the fixture page set. Build() would load the real embedded
// pages — however many of them there are by now — and join them with this
// synthetic tree, so the unit tests use BuildWith and supply their own.
func buildPages() []resources.Page {
	return []resources.Page{
		{Name: "table", Summary: "a dataset", Kind: "Table", Usage: []string{"qctl get tables"}},
		{Name: "rule", Summary: "a rule", Kind: "Rule"},
	}
}

func TestBuildUnionsTreeAndDispatchVerbs(t *testing.T) {
	got := BuildWith(buildTree(), buildPages())
	by := map[string]Resource{}
	for _, r := range got {
		by[r.Name] = r
	}

	// table: get from the tree, apply from the dispatcher's kind.
	if v := strings.Join(by["table"].Verbs, ","); v != "apply,get" {
		t.Errorf("table verbs = %q, want %q", v, "apply,get")
	}
	// rule has NO apply subcommand anywhere; the kind is its only route in.
	if v := strings.Join(by["rule"].Verbs, ","); v != "apply" {
		t.Errorf("rule verbs = %q, want %q", v, "apply")
	}
	// Duplicate verbs (get table, get tables) collapse to one.
	if len(by["table"].Commands) != 2 {
		t.Errorf("table commands = %v, want 2", by["table"].Commands)
	}
}

func TestBuildClassifiesAndOrders(t *testing.T) {
	got := BuildWith(buildTree(), buildPages())
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	// apply class (by kind) alphabetically, then create, then read-only.
	want := []string{"rule", "table", "dry-run-job", "session"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("display order = %v, want %v", names, want)
	}

	by := map[string]Resource{}
	for _, r := range got {
		by[r.Name] = r
	}
	if by["table"].Class != ClassApply || by["table"].PrimaryVerb != "apply" {
		t.Errorf("table class/primary = %v/%q", by["table"].Class, by["table"].PrimaryVerb)
	}
	if by["dry-run-job"].Class != ClassCreate || by["dry-run-job"].PrimaryVerb != "create" {
		t.Errorf("dry-run-job class/primary = %v/%q", by["dry-run-job"].Class, by["dry-run-job"].PrimaryVerb)
	}
	// A read-only resource with no "get" verb reports its first verb, not a
	// fictional "get".
	if by["session"].Class != ClassReadOnly || by["session"].PrimaryVerb != "auth" {
		t.Errorf("session class/primary = %v/%q", by["session"].Class, by["session"].PrimaryVerb)
	}
}

func TestBuildSynthesizesUsageForPagelessResources(t *testing.T) {
	// session and dry-run-job have no page: Build must still list them (the
	// user is never blocked) with usage derived from the tree.
	got := BuildWith(buildTree(), buildPages())
	for _, r := range got {
		if r.Name != "session" {
			continue
		}
		if len(r.Usage) != 1 || r.Usage[0] != "qctl auth login" {
			t.Errorf("session usage = %v, want [qctl auth login]", r.Usage)
		}
	}
}

// TestLookupResolvesAliasesNoCommandSurfaceCovers guards alias pairs that no
// command surface exercises. table-schema is read-only (no apply Kind), so
// `qctl create migration` never redirects and no other command prints the
// resolved canonical name for these two aliases. The deleted
// resources.Lookup had TestLookupByNameAndAlias for this; catalog.Lookup
// needs its own direct coverage or these mappings (real page frontmatter:
// internal/resources/pages/table-schema.md's `aliases: [migration,
// table-migration]`) can drift silently.
func TestLookupResolvesAliasesNoCommandSurfaceCovers(t *testing.T) {
	root := &cobra.Command{Use: "qctl"} // Lookup reads the real embedded pages via Build; the tree itself is irrelevant here.
	for _, tc := range []struct{ alias, want string }{
		{"migration", "table-schema"},
		{"table-migration", "table-schema"},
	} {
		r, ok := Lookup(root, tc.alias)
		if !ok {
			t.Fatalf("Lookup(%q) not found", tc.alias)
		}
		if r.Name != tc.want {
			t.Errorf("Lookup(%q).Name = %q, want %q", tc.alias, r.Name, tc.want)
		}
	}
}

func TestResolveCommandLine(t *testing.T) {
	root := buildTree()
	root.PersistentFlags().StringP("output", "o", "table", "")
	tableCmd, _, _ := root.Find([]string{"get", "table"})
	tableCmd.Flags().Int("limit", 20, "")

	if _, err := ResolveCommandLine(root, "qctl get table orders -o json"); err != nil {
		t.Errorf("valid line rejected: %v", err)
	}
	if _, err := ResolveCommandLine(root, "qctl get table orders --limit 5"); err != nil {
		t.Errorf("valid line rejected: %v", err)
	}
	// A clustered shorthand must not panic pflag's one-character lookup.
	root.PersistentFlags().CountP("verbose", "v", "")
	if _, err := ResolveCommandLine(root, "qctl get table orders -vvv"); err != nil {
		t.Errorf("clustered shorthand rejected: %v", err)
	}
	if _, err := ResolveCommandLine(root, "qctl get bogus-thing"); err == nil {
		t.Error("unknown command accepted")
	}
	if _, err := ResolveCommandLine(root, "qctl get table orders --nope"); err == nil {
		t.Error("unknown flag accepted")
	}
	if _, err := ResolveCommandLine(root, "qctl get"); err == nil {
		t.Error("a dispatch parent is not a runnable example")
	}
	// Every real parent has a RunE (cmdutil.SubcommandRequired), so cobra
	// calls it runnable; an unknown subcommand beneath it must still fail
	// rather than pass as the parent's positional argument.
	review := &cobra.Command{Use: "review", Annotations: Parent(), RunE: func(*cobra.Command, []string) error { return nil }}
	review.AddCommand(&cobra.Command{Use: "approve <id>", Annotations: For("rule-review"), Run: func(*cobra.Command, []string) {}})
	root.AddCommand(review)
	if _, err := ResolveCommandLine(root, "qctl review approve 1"); err != nil {
		t.Errorf("valid line under a runnable parent rejected: %v", err)
	}
	for line, want := range map[string]string{
		// The error names the typo, not the parent it stopped at.
		"qctl review frobnicate 1": `"qctl review frobnicate 1": "frobnicate" is not a subcommand of "qctl review"`,
		"qctl review":              `"qctl review": "qctl review" is a dispatch parent, not a runnable command`,
		"qctl review -o json":      `"qctl review -o json": "qctl review" is a dispatch parent, not a runnable command`,
	} {
		_, err := ResolveCommandLine(root, line)
		if err == nil {
			t.Errorf("%q accepted: a parent with a RunE is still a dispatch parent", line)
		} else if err.Error() != want {
			t.Errorf("ResolveCommandLine(%q) error = %q, want %q", line, err, want)
		}
	}
	// A bare root name must return an error, not panic indexing fields[1].
	if _, err := ResolveCommandLine(root, "qctl"); err == nil {
		t.Error("a bare root name names no command")
	}
}

func TestResolveCommandLinePreservesFlagErrorTextAndOrder(t *testing.T) {
	root := buildTree()
	root.PersistentFlags().CountP("verbose", "v", "")

	tests := []struct {
		line string
		want string
	}{
		{
			line: "qctl get table orders --unknown=value",
			want: `"qctl get table orders --unknown=value": qctl get table has no --unknown flag`,
		},
		{
			line: "qctl get table orders -vx",
			want: `"qctl get table orders -vx": qctl get table has no -x flag`,
		},
	}

	for _, tt := range tests {
		_, err := ResolveCommandLine(root, tt.line)
		if err == nil || err.Error() != tt.want {
			t.Errorf("ResolveCommandLine(%q) error = %v, want %q", tt.line, err, tt.want)
		}
	}

	for _, line := range []string{
		"qctl get table -1",
		"qctl get table -- -1",
	} {
		if _, err := ResolveCommandLine(root, line); err != nil {
			t.Errorf("ResolveCommandLine(%q) rejected non-flag token: %v", line, err)
		}
	}
}

const stampMe = `---
summary: rows of a table
aliases:
kind:
schema:
guide:
signature: 0000000
---

## USAGE

    qctl get data <old>

## EXAMPLES

    # curated by hand — must survive a re-stamp
    qctl get data orders

## GOTCHAS

- hand-written, must survive [backend]

## SEE ALSO

- ` + "`qctl guide inspect-data`" + `
`

func TestRestampRewritesOnlyUsageAndSignature(t *testing.T) {
	got := Restamp(stampMe, []string{"qctl get data <table-id-or-name>"}, "abc1234")

	if !strings.Contains(got, "signature: abc1234") {
		t.Error("signature was not re-stamped")
	}
	if strings.Contains(got, "qctl get data <old>") {
		t.Error("stale USAGE line survived")
	}
	if !strings.Contains(got, "    qctl get data <table-id-or-name>") {
		t.Error("new USAGE line missing or unindented")
	}
	for _, keep := range []string{
		"# curated by hand — must survive a re-stamp",
		"qctl get data orders",
		"- hand-written, must survive [backend]",
		"`qctl guide inspect-data`",
		"summary: rows of a table",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("re-stamp destroyed %q", keep)
		}
	}
	// Re-stamping is idempotent.
	if again := Restamp(got, []string{"qctl get data <table-id-or-name>"}, "abc1234"); again != got {
		t.Error("Restamp is not idempotent")
	}
}

// TestRestampInsertsMissingSignature: a hand-written page, or a merge that
// dropped the line, leaves a page with no signature: at all. Restamp used to
// have nothing to replace, so -update logged a re-stamp and wrote the file
// back unchanged — leaving the page failing two tests that both prescribe the
// command that had just no-op'd. The line goes inside the frontmatter block,
// where ParsePage will read it.
func TestRestampInsertsMissingSignature(t *testing.T) {
	noSignature := strings.Replace(stampMe, "signature: 0000000\n", "", 1)
	if noSignature == stampMe {
		t.Fatal("fixture assumption broken: no signature line was removed")
	}

	got := Restamp(noSignature, []string{"qctl get data <table-id-or-name>"}, "abc1234")

	p, err := resources.ParsePage("data", []byte(got))
	if err != nil {
		t.Fatalf("the re-stamped page no longer parses: %v", err)
	}
	if p.Signature != "abc1234" {
		t.Errorf("signature = %q, want %q (it must land inside the frontmatter)", p.Signature, "abc1234")
	}
	if len(p.Gotchas) != 1 || len(p.SeeAlso) != 1 || len(p.Examples) != 1 {
		t.Errorf("insertion disturbed the body: gotchas=%v seealso=%v examples=%v", p.Gotchas, p.SeeAlso, p.Examples)
	}
	// Inserting once is enough: a second stamp replaces rather than re-inserts.
	if again := Restamp(got, []string{"qctl get data <table-id-or-name>"}, "abc1234"); again != got {
		t.Error("Restamp is not idempotent after inserting a signature")
	}
}
