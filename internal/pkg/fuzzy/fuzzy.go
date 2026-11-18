// Package fuzzy suggests near misses for a query that matched nothing, so an
// empty result is a redirect rather than a dead end.
//
// It exists because the obvious matcher does not work here. The most likely
// user error on a two-word name is writing the words in the wrong order --
// "field_required" for "required_field" -- and that pair has a Levenshtein
// distance of 10 with neither string containing the other, so substring and
// edit distance both miss it. Rotation is therefore the top-ranked rule.
package fuzzy

import (
	"sort"
	"strings"
)

// Match ranks, best first. They are the whole matching policy.
const (
	rankRotation  = iota // same tokens, any order
	rankSubstring        // either string contains the other
	rankTypo             // small edit distance over the whole string
)

const (
	// maxEditDistance is what still reads as a typo rather than a different word.
	maxEditDistance = 3
	// maxCandidates is where a hint stops being a hint. Five near misses are a
	// list to scan; a dozen is a second search, so we say nothing instead.
	maxCandidates = 5
	// maxSuggestions caps what is printed: three is a scannable "did you mean"
	// list, not a second search result page.
	maxSuggestions = 3
)

// Candidate pairs a string to match against with the value to suggest when it
// matches. The two differ whenever a thing is searchable by one name and
// addressable by another -- a rule family matches on its name or its slug, but
// only the slug can be passed back to --search.
type Candidate struct {
	Match string
	Value string
}

// Suggest returns up to three candidates close to query, best match first. It
// is SuggestFrom for callers whose match and value are the same string.
func Suggest(query string, candidates []string) []string {
	pairs := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		pairs = append(pairs, Candidate{Match: c, Value: c})
	}
	return SuggestFrom(query, pairs)
}

// SuggestFrom returns up to three candidate values close to query, best match
// first, ties broken alphabetically. Several candidates may share a value; it
// counts and ranks once per value. It uses the default "too many matches, say
// nothing" cutoff; see SuggestFromN to control that.
//
// It returns nil both when nothing is close and when too many things are.
func SuggestFrom(query string, candidates []Candidate) []string {
	return SuggestFromN(query, candidates, maxCandidates)
}

// SuggestFromN is SuggestFrom with the "too many matches" cutoff under the
// caller's control; maxMatches <= 0 disables it. The cutoff is right for an
// unbounded, server-side name list, where six near misses mean the query was
// too vague to hint about. It is wrong for a closed, in-binary list like the
// resource catalog, which wants its top three either way.
//
//nolint:gocyclo // Ranking loop uses switch over match strategies; splitting would deviate from spec.
func SuggestFromN(query string, candidates []Candidate, maxMatches int) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	qKey := tokenKey(q)

	best := make(map[string]int, len(candidates))
	var values []string
	for _, c := range candidates {
		m := strings.ToLower(strings.TrimSpace(c.Match))
		if m == "" || c.Value == "" {
			continue
		}
		rank := -1
		switch {
		case tokenKey(m) == qKey:
			rank = rankRotation
		case strings.Contains(m, q) || strings.Contains(q, m):
			rank = rankSubstring
		case EditDistance(m, q) <= maxEditDistance:
			rank = rankTypo
		}
		if rank < 0 {
			continue
		}
		if prev, seen := best[c.Value]; !seen {
			best[c.Value] = rank
			values = append(values, c.Value)
		} else if rank < prev {
			best[c.Value] = rank
		}
	}

	if len(values) == 0 || (maxMatches > 0 && len(values) > maxMatches) {
		return nil
	}
	sort.SliceStable(values, func(i, j int) bool {
		if best[values[i]] != best[values[j]] {
			return best[values[i]] < best[values[j]]
		}
		return values[i] < values[j]
	})
	if len(values) > maxSuggestions {
		values = values[:maxSuggestions]
	}
	return values
}

// tokenKey reduces a name to its sorted tokens, so word order stops mattering:
// "field_required" and "required_field" share a key.
func tokenKey(s string) string {
	tokens := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ' '
	})
	sort.Strings(tokens)
	return strings.Join(tokens, "\x00")
}

// EditDistance is the Levenshtein distance between a and b, by byte.
func EditDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(min(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
