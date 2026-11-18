package cmdutil

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/spf13/cobra"
)

func suggestNoop(*cobra.Command, []string) error { return nil }

// suggestLeaf builds a runnable command acting on resource ("" for none).
func suggestLeaf(use, resource string, aliases ...string) *cobra.Command {
	c := &cobra.Command{Use: use, Aliases: aliases, RunE: suggestNoop}
	if resource != "" {
		c.Annotations = catalog.For(resource)
	}
	return c
}

// suggestGroup builds a dispatch parent routed through SubcommandRequired.
func suggestGroup(use string, children ...*cobra.Command) *cobra.Command {
	c := &cobra.Command{Use: use, Annotations: catalog.Parent(), RunE: SubcommandRequired}
	c.AddCommand(children...)
	return c
}

// suggestRoot mounts groups under a root named qctl, so they are non-root
// parents as every real group is.
func suggestRoot(groups ...*cobra.Command) *cobra.Command {
	root := &cobra.Command{Use: "qctl", Annotations: catalog.Parent(), RunE: SubcommandRequired}
	root.AddCommand(groups...)
	return root
}

// assertOneTrailer fails unless msg ends with APIResourcesHint and carries no
// other "Run '...'" line.
func assertOneTrailer(t *testing.T, msg string) {
	t.Helper()
	if !strings.HasSuffix(msg, APIResourcesHint) {
		t.Errorf("message does not end with the api-resources hint:\n%s", msg)
	}
	if n := strings.Count(msg, "Run '"); n != 1 {
		t.Errorf("message carries %d \"Run '\" trailers, want 1:\n%s", n, msg)
	}
}

// requireBadUsage is assertBadUsage that also stops the test on a nil error.
func requireBadUsage(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	assertBadUsage(t, err)
}

// A word that names no resource and is near no child gets no suggestion
// block: the header, a blank line and the one trailer.
func TestSubcommandRequiredWithNoSuggestion(t *testing.T) {
	get := suggestGroup("get", suggestLeaf("rule <id>", "rule"))
	suggestRoot(get)

	err := SubcommandRequired(get, []string{"bogusresource"})
	requireBadUsage(t, err)
	const want = "unknown command \"bogusresource\" for \"qctl get\"\n\n" +
		"Run 'qctl api-resources' to see all resources and how to manage them."
	if err.Error() != want {
		t.Errorf("message =\n%q\nwant\n%q", err.Error(), want)
	}
}

// "rulf" is Levenshtein distance 1 from "rule" and is not a prefix of it, so
// only the distance branch of SuggestionsFor can produce this suggestion.
// Cobra leaves SuggestionsMinimumDistance at 0 unless findSuggestions runs,
// and SubcommandRequired calls SuggestionsFor directly, so without an explicit
// default the branch is dead code.
func TestSubcommandRequiredSuggestsNearMissesAsCommandLines(t *testing.T) {
	get := suggestGroup("get", suggestLeaf("rule <id>", "rule"))
	suggestRoot(get)

	err := SubcommandRequired(get, []string{"rulf"})
	requireBadUsage(t, err)
	const want = "unknown command \"rulf\" for \"qctl get\"\n\n" +
		"Did you mean this?\n" +
		"  qctl get rule <id>\n\n" +
		"Run 'qctl api-resources' to see all resources and how to manage them."
	if err.Error() != want {
		t.Errorf("message =\n%q\nwant\n%q", err.Error(), want)
	}
}

// SuggestionsFor reads names only, so a typo of an alias would otherwise
// suggest only the name-matched sibling. Name matches come first.
func TestSubcommandRequiredMatchesAliases(t *testing.T) {
	config := suggestGroup("config",
		suggestLeaf("list-contexts", "", "get-contexts"),
		suggestLeaf("set-context [name]", ""),
	)
	suggestRoot(config)

	err := SubcommandRequired(config, []string{"get-contexs"})
	requireBadUsage(t, err)
	const block = "Did you mean this?\n" +
		"  qctl config set-context [name]\n" +
		"  qctl config list-contexts\n\n"
	if !strings.Contains(err.Error(), block) {
		t.Errorf("message does not carry\n%q\ngot\n%q", block, err.Error())
	}
}

// A resource noun suggests that resource's commands: get first, then
// describe, then the rest in walk order. Dispatch parents and Levenshtein
// neighbours ("enable" is distance 2 from "table") are left out.
func TestSubcommandRequiredSuggestsTheNamedResource(t *testing.T) {
	root := suggestRoot(
		suggestGroup("apply", suggestLeaf("table -f <file.yaml>", "table")),
		suggestGroup("describe", suggestLeaf("table [id-or-name]", "table")),
		suggestGroup("enable", suggestLeaf("rule <id>", "rule")),
		suggestGroup("get", suggestLeaf("table <id-or-name>", "table"), suggestLeaf("tables", "table")),
	)

	for _, word := range []string{"table", "tables", "dataset"} {
		t.Run(word, func(t *testing.T) {
			err := SubcommandRequired(root, []string{word})
			requireBadUsage(t, err)
			want := "unknown command \"" + word + "\" for \"qctl\"\n\n" +
				"Did you mean this?\n" +
				"  qctl get table <id-or-name>\n" +
				"  qctl get tables\n" +
				"  qctl describe table [id-or-name]\n" +
				"  qctl apply table -f <file.yaml>\n\n" +
				APIResourcesHint
			if err.Error() != want {
				t.Errorf("message =\n%q\nwant\n%q", err.Error(), want)
			}
		})
	}
}

// Under a non-root parent, the parent's own children acting on the resource
// come first, whether named after it or only annotated with it.
func TestSubcommandRequiredPrefersTheParentsOwnResourceCommands(t *testing.T) {
	rows := suggestGroup("rows",
		suggestLeaf("hold <table>", "data"),
		suggestLeaf("rule-ignores <table>", "rule"),
	)
	suggestRoot(rows, suggestGroup("get", suggestLeaf("data <table>", "data")))

	err := SubcommandRequired(rows, []string{"data"})
	requireBadUsage(t, err)
	const block = "Did you mean this?\n" +
		"  qctl rows hold <table>\n" +
		"  qctl get data <table>\n\n"
	if !strings.Contains(err.Error(), block) {
		t.Errorf("message does not carry\n%q\ngot\n%q", block, err.Error())
	}
}

// A dispatch parent named after the resource is not runnable advice, even
// when it is the parent's own child.
func TestSubcommandRequiredSkipsDispatchParents(t *testing.T) {
	cache := suggestGroup("cache", suggestGroup("alert"))
	suggestRoot(
		suggestGroup("alert", suggestLeaf("resolve <id>", "alert")),
		suggestGroup("get", suggestLeaf("alerts", "alert")),
		cache,
	)

	err := SubcommandRequired(cache, []string{"alerts"})
	requireBadUsage(t, err)
	const block = "Did you mean this?\n" +
		"  qctl get alerts\n" +
		"  qctl alert resolve <id>\n\n"
	if !strings.Contains(err.Error(), block) {
		t.Errorf("message does not carry\n%q\ngot\n%q", block, err.Error())
	}
}

func TestSubcommandRequiredCapsSuggestionsAtFive(t *testing.T) {
	get := suggestGroup("get")
	for _, name := range []string{"rule-a", "rule-b", "rule-c", "rule-d", "rule-e", "rule-f"} {
		get.AddCommand(suggestLeaf(name, ""))
	}
	suggestRoot(get)

	err := SubcommandRequired(get, []string{"rule-"})
	requireBadUsage(t, err)
	if n := strings.Count(err.Error(), "\n  qctl get rule-"); n != 5 {
		t.Errorf("got %d suggestion lines, want 5:\n%s", n, err.Error())
	}
	assertOneTrailer(t, err.Error())
}

// Under root, a word near no command falls back to the closest resources.
func TestSubcommandRequiredFallsBackToCatalogUnderRoot(t *testing.T) {
	root := suggestRoot(suggestGroup("get", suggestLeaf("table <id-or-name>", "table")))

	err := SubcommandRequired(root, []string{"tabels"})
	requireBadUsage(t, err)
	if !strings.Contains(err.Error(), "  qctl get table <id-or-name>\n") {
		t.Errorf("no catalog fallback for a misspelled resource:\n%s", err.Error())
	}
}

// An empty word is a prefix of every name; it gets no suggestion block.
func TestSubcommandRequiredBlankWordSuggestsNothing(t *testing.T) {
	root := suggestRoot(suggestGroup("get", suggestLeaf("table <id-or-name>", "table")))

	err := SubcommandRequired(root, []string{""})
	requireBadUsage(t, err)
	if strings.Contains(err.Error(), "Did you mean this?") {
		t.Errorf("blank word produced suggestions:\n%s", err.Error())
	}
}

func TestSubcommandRequiredBareParent(t *testing.T) {
	small := suggestGroup("config", suggestLeaf("use-context <name>", ""), suggestLeaf("current-context", ""),
		&cobra.Command{Use: "hidden", Hidden: true, RunE: suggestNoop})
	large := suggestGroup("get")
	for i := 0; i <= maxListedSubcommands; i++ {
		large.AddCommand(suggestLeaf(string(rune('a'+i))+"-thing", ""))
	}
	suggestRoot(small, large)

	for _, tc := range []struct {
		cmd  *cobra.Command
		want string
	}{
		{small, "qctl config needs a subcommand: current-context, use-context"},
		{large, "qctl get needs a subcommand.\n" + APIResourcesHint},
	} {
		t.Run(tc.cmd.Name(), func(t *testing.T) {
			var out bytes.Buffer
			tc.cmd.SetOut(&out)
			err := SubcommandRequired(tc.cmd, []string{})
			requireBadUsage(t, err)
			if err.Error() != tc.want {
				t.Errorf("message =\n%q\nwant\n%q", err.Error(), tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("bare parent wrote to stdout:\n%s", out.String())
			}
		})
	}
}

func TestSubcommandRequiredBareRootPrintsHelp(t *testing.T) {
	root := suggestRoot(suggestGroup("get", suggestLeaf("rule <id>", "")))
	var out bytes.Buffer
	root.SetOut(&out)

	if err := SubcommandRequired(root, []string{}); err != nil {
		t.Fatalf("bare root returned %v", err)
	}
	if !strings.Contains(out.String(), "Available Commands:") {
		t.Errorf("bare root did not print help:\n%s", out.String())
	}
}
