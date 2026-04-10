package dataset_kinds

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/org"
)

// ResolvedDatasetKind holds the result of slug/UUID resolution.
type ResolvedDatasetKind struct {
	ID   string
	Slug string
	Name string
}

// ResolveDatasetKind resolves a user-provided input to exactly one dataset kind.
//
// Resolution order:
//  1. Full UUID (36 chars) -> fast path
//  2. Exact slug match (case-sensitive)
//  3. UUID prefix match
//  4. Fuzzy match (case-insensitive substring on slug and name)
func ResolveDatasetKind(kinds []DatasetKindTiny, input string) (*ResolvedDatasetKind, error) {
	if input == "" {
		return nil, fmt.Errorf("table kind identifier cannot be empty")
	}

	// 1. Full UUID — fast path
	if isFullUUID(input) {
		return &ResolvedDatasetKind{ID: input}, nil
	}

	if len(kinds) == 0 {
		return nil, fmt.Errorf("no table kinds found in the current organization")
	}

	// 2. Exact slug match (case-sensitive)
	exactMatches := findExactSlugMatches(kinds, input)
	if len(exactMatches) == 1 {
		k := exactMatches[0]
		return &ResolvedDatasetKind{ID: k.ID.String(), Slug: k.Slug, Name: k.Name}, nil
	}
	if len(exactMatches) > 1 {
		return nil, formatAmbiguousError(input, exactMatches)
	}

	// 3. UUID prefix match
	if org.IsUUIDLike(input) {
		prefixMatches := findUUIDPrefixMatches(kinds, input)
		if len(prefixMatches) == 1 {
			k := prefixMatches[0]
			return &ResolvedDatasetKind{ID: k.ID.String(), Slug: k.Slug, Name: k.Name}, nil
		}
		if len(prefixMatches) > 1 {
			return nil, formatAmbiguousError(input, prefixMatches)
		}
	}

	// 4. Fuzzy match (case-insensitive substring on slug and name)
	fuzzyMatches := findFuzzyMatches(kinds, input)
	if len(fuzzyMatches) == 1 {
		k := fuzzyMatches[0]
		return &ResolvedDatasetKind{ID: k.ID.String(), Slug: k.Slug, Name: k.Name}, nil
	}
	if len(fuzzyMatches) > 1 {
		return nil, formatAmbiguousError(input, fuzzyMatches)
	}

	return nil, formatNoMatchError(input, kinds)
}

// isFullUUID checks if input looks like a full 36-char UUID.
func isFullUUID(input string) bool {
	if len(input) != 36 {
		return false
	}
	return org.IsUUIDLike(input)
}

func findExactSlugMatches(kinds []DatasetKindTiny, input string) []DatasetKindTiny {
	var matches []DatasetKindTiny
	for _, k := range kinds {
		if k.Slug == input || k.Name == input {
			matches = append(matches, k)
		}
	}
	return matches
}

func findUUIDPrefixMatches(kinds []DatasetKindTiny, prefix string) []DatasetKindTiny {
	var matches []DatasetKindTiny
	lowerPrefix := strings.ToLower(strings.ReplaceAll(prefix, "-", ""))
	for _, k := range kinds {
		idHex := strings.ToLower(strings.ReplaceAll(k.ID.String(), "-", ""))
		if strings.HasPrefix(idHex, lowerPrefix) {
			matches = append(matches, k)
		}
	}
	return matches
}

func findFuzzyMatches(kinds []DatasetKindTiny, input string) []DatasetKindTiny {
	var matches []DatasetKindTiny
	lowerInput := strings.ToLower(input)
	for _, k := range kinds {
		if strings.Contains(strings.ToLower(k.Slug), lowerInput) || strings.Contains(strings.ToLower(k.Name), lowerInput) {
			matches = append(matches, k)
		}
	}
	return matches
}

func formatAmbiguousError(input string, matches []DatasetKindTiny) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Multiple table kinds match '%s':\n", input))
	for _, k := range matches {
		msg.WriteString(fmt.Sprintf("  - %s  %s (%s)\n", ShortID(k.ID.String()), k.Slug, k.Name))
	}
	msg.WriteString("\nPlease be more specific.")
	return fmt.Errorf("%s", msg.String())
}

func formatNoMatchError(input string, kinds []DatasetKindTiny) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("No table kind found matching '%s'.\n", input))

	if len(kinds) > 0 {
		msg.WriteString("\nAvailable table kinds:\n")
		limit := 10
		for i, k := range kinds {
			if i >= limit {
				msg.WriteString(fmt.Sprintf("  ... and %d more\n", len(kinds)-limit))
				break
			}
			msg.WriteString(fmt.Sprintf("  - %s\n", k.Slug))
		}
	}

	msg.WriteString("\nHint: Use 'qctl get table-kinds' to see all available table kinds.")
	return fmt.Errorf("%s", msg.String())
}
