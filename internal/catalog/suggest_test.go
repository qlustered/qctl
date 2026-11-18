package catalog

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/resources"
)

func TestSuggestFindsNearMisses(t *testing.T) {
	built := []Resource{
		{Page: resources.Page{Name: "table", Aliases: []string{"dataset"}}},
		{Page: resources.Page{Name: "table-schema", Aliases: []string{"migration"}}},
		{Page: resources.Page{Name: "dry-run-job"}},
		{Page: resources.Page{Name: "session"}},
	}
	cases := map[string]string{
		"tabel":     "table",
		"tables":    "table",
		"datasets":  "table",
		"dry-run":   "dry-run-job",
		"sessions":  "session",
		"migration": "table-schema",
	}
	for in, want := range cases {
		got := suggestFrom(built, in)
		if len(got) == 0 || !strings.Contains(strings.Join(got, ","), want) {
			t.Errorf("suggestFrom(%q) = %v, want it to include %q", in, got, want)
		}
	}
	if got := suggestFrom(built, "zzzzzzzzzz"); len(got) != 0 {
		t.Errorf("suggestFrom(nonsense) = %v, want none", got)
	}
	if got := suggestFrom(built, ""); len(got) != 0 {
		t.Errorf("suggestFrom(empty) = %v, want none", got)
	}
}

// TestSuggestRanksRotationsFirst is the whole reason catalog.Suggest was folded
// into internal/pkg/fuzzy: "rule-table" and "table-rule" share no substring and
// are far apart by edit distance, so the old matcher dead-ended on the single
// most likely two-word mistake.
func TestSuggestRanksRotationsFirst(t *testing.T) {
	built := []Resource{
		{Page: resources.Page{Name: "table-rule"}},
		{Page: resources.Page{Name: "table"}},
		{Page: resources.Page{Name: "rule"}},
	}
	got := suggestFrom(built, "rule-table")
	if len(got) == 0 || got[0] != "table-rule" {
		t.Errorf("suggestFrom(rule-table) = %v, want table-rule first", got)
	}
}

// TestSuggestKeepsHintingWhenManyMatch: fuzzy's default is to drop the hint once
// more than five candidates match. The catalog is a closed list of a few dozen
// resource names, and a short query matching six of them is exactly when the
// reader needs the top three, so catalog opts out of that cutoff.
func TestSuggestKeepsHintingWhenManyMatch(t *testing.T) {
	built := []Resource{
		{Page: resources.Page{Name: "rule"}},
		{Page: resources.Page{Name: "rule-review"}},
		{Page: resources.Page{Name: "rule-review-policy"}},
		{Page: resources.Page{Name: "rule-revision"}},
		{Page: resources.Page{Name: "rule-suggestion"}},
		{Page: resources.Page{Name: "table-rule"}},
	}
	if got := suggestFrom(built, "rul"); len(got) != 3 {
		t.Errorf("suggestFrom(rul) = %v, want three suggestions, not silence", got)
	}
}

// TestSuggestReturnsCanonicalNamesForAliasHits: a resource is searchable by its
// aliases but addressable only by its name, which is what fuzzy.Candidate's
// Match/Value split is for.
func TestSuggestReturnsCanonicalNamesForAliasHits(t *testing.T) {
	built := []Resource{
		{Page: resources.Page{Name: "table-schema", Aliases: []string{"migration", "table-migration"}}},
	}
	got := suggestFrom(built, "migrations")
	if len(got) != 1 || got[0] != "table-schema" {
		t.Errorf("suggestFrom(migrations) = %v, want [table-schema] once", got)
	}
}
