package catalog

import (
	"github.com/qlustered/qctl/internal/pkg/fuzzy"
	"github.com/spf13/cobra"
)

// Suggest returns up to three catalog names close to an unknown one, so an
// unknown resource is a redirect rather than a dead end.
func Suggest(root *cobra.Command, name string) []string {
	built, err := Build(root)
	if err != nil {
		return nil
	}
	return suggestFrom(built, name)
}

// suggestFrom is the pure half, so it can be unit-tested without a tree. Every
// name and every alias is a candidate; all of them suggest the canonical
// resource name, which is what the reader has to type next.
//
// The cutoff is disabled (0) deliberately: fuzzy drops the hint entirely once
// more than five things match, which is right for an org's rule names and wrong
// here -- the catalog is a closed list of ~26, and a short query matching six
// resources is exactly when the reader most needs the top three.
func suggestFrom(built []Resource, name string) []string {
	candidates := make([]fuzzy.Candidate, 0, len(built)*2)
	for _, r := range built {
		candidates = append(candidates, fuzzy.Candidate{Match: r.Name, Value: r.Name})
		for _, a := range r.Aliases {
			candidates = append(candidates, fuzzy.Candidate{Match: a, Value: r.Name})
		}
	}
	return fuzzy.SuggestFromN(name, candidates, 0)
}
