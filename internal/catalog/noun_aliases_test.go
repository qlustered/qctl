package catalog_test

import (
	"slices"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/spf13/cobra"
)

// TestEveryResourceNounResolvesUnderEveryVerb: under any top-level command
// with a child named after a resource, every noun of that resource reaches a
// child named after it.
func TestEveryResourceNounResolvesUnderEveryVerb(t *testing.T) {
	root := realRoot(t)
	built, err := catalog.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range built {
		nouns := catalog.NounsOf(r.Name, r.Aliases).All()
		for _, verb := range root.Commands() {
			if !slices.ContainsFunc(verb.Commands(), func(c *cobra.Command) bool {
				return slices.Contains(nouns, c.Name())
			}) {
				continue
			}
			for _, noun := range nouns {
				got, _, err := root.Find([]string{verb.Name(), noun})
				switch {
				case err != nil:
					t.Errorf("qctl %s %s (resource %s): %v", verb.Name(), noun, r.Name, err)
				case got.Parent() != verb || !slices.Contains(nouns, got.Name()):
					t.Errorf("qctl %s %s resolves to %q, want a command under %q named after %s (one of %v)",
						verb.Name(), noun, got.CommandPath(), verb.CommandPath(), r.Name, nouns)
				}
			}
		}
	}
}

// TestNoNounNamesTwoResources: a noun is wired onto the commands of the first
// resource that claims it, so a shared one would silently route to the wrong
// resource.
func TestNoNounNamesTwoResources(t *testing.T) {
	built, err := catalog.Build(realRoot(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	owner := map[string]string{}
	for _, r := range built {
		for _, noun := range catalog.NounsOf(r.Name, r.Aliases).All() {
			if prev, ok := owner[noun]; ok && prev != r.Name {
				t.Errorf("noun %q names both %s and %s", noun, prev, r.Name)
				continue
			}
			owner[noun] = r.Name
		}
	}
}

func TestResourceNounAliasesResolve(t *testing.T) {
	root := realRoot(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"get", "dataset", "2"}, "qctl get table"},
		{[]string{"get", "datasets"}, "qctl get tables"},
		{[]string{"describe", "dataset", "2"}, "qctl describe table"},
		{[]string{"get", "rows", "2"}, "qctl get data"},
		{[]string{"get", "data-source"}, "qctl get cloud-source"},
		{[]string{"get", "composite-dataset"}, "qctl get linked-table"},
		{[]string{"get", "dataset-rule"}, "qctl get table-rule"},
		{[]string{"get", "dataset-rules"}, "qctl get table-rules"},
		{[]string{"get", "stored-items"}, "qctl get files"},
		{[]string{"describe", "stored-item"}, "qctl describe file"},
		{[]string{"get", "problem-domains"}, "qctl get table-kinds"},
		{[]string{"get", "problem-domain"}, "qctl get table-kind"},
		{[]string{"get", "notification"}, "qctl get notifications"},
		{[]string{"get", "inbox"}, "qctl get notifications"},
	} {
		got, _, err := root.Find(tc.args)
		if err != nil {
			t.Errorf("qctl %v: %v", tc.args, err)
			continue
		}
		if got.CommandPath() != tc.want {
			t.Errorf("qctl %v resolves to %q, want %q", tc.args, got.CommandPath(), tc.want)
		}
	}
}
