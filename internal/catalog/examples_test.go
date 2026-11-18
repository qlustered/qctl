package catalog_test

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
)

// TestExamplesResolve: every example line must name a command that exists and
// flags that are registered on it. This is what stops a page from teaching an
// agent a command qctl does not have.
func TestExamplesResolve(t *testing.T) {
	root := realRoot(t)
	built, err := catalog.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range built {
		for _, ex := range r.Examples {
			if _, err := catalog.ResolveCommandLine(root, ex.Command); err != nil {
				t.Errorf("%s.md: %v", r.Name, err)
			}
		}
	}
}

// TestEveryVerbHasExample: a verb with no example is a capability an agent has
// to guess the shape of.
func TestEveryVerbHasExample(t *testing.T) {
	root := realRoot(t)
	built, err := catalog.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range built {
		covered := map[string]bool{}
		for _, ex := range r.Examples {
			covered[catalog.Verb(ex.Command)] = true
		}
		for _, verb := range r.Verbs {
			if !covered[verb] {
				t.Errorf("%s.md has no EXAMPLES line for the %q verb (verbs: %s)",
					r.Name, verb, strings.Join(r.Verbs, ", "))
			}
		}
	}
}
