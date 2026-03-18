package rule_versions

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/qlustered/qctl/internal/org"
)

var (
	// reLC2UC matches a lowercase letter or digit followed by an uppercase letter.
	reLC2UC = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	// reUCRun matches an uppercase run followed by an uppercase+lowercase pair (acronym boundary).
	reUCRun = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
)

// toSlug converts a PascalCase/CamelCase name to a kebab-case slug.
// Mirrors Python's _auto_slugify: "CommissionMathRule" → "commission-math-rule".
func toSlug(input string) string {
	s := reLC2UC.ReplaceAllString(input, "${1}-${2}")
	s = reUCRun.ReplaceAllString(s, "${1}-${2}")
	return strings.ToLower(s)
}

// containsUpper returns true if the input contains any uppercase letter.
func containsUpper(input string) bool {
	for _, r := range input {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// ResolvedRule holds the result of name/ID resolution.
type ResolvedRule struct {
	ID      string // full UUID of a specific revision
	Name    string // rule family name
	Release string // release version
}

// ShortID returns the first 8 hex characters of a UUID, stripping dashes.
// Similar to git's short SHA.
func ShortID(uuid string) string {
	hex := strings.ReplaceAll(uuid, "-", "")
	if len(hex) > 8 {
		return hex[:8]
	}
	return hex
}

// isFullUUID checks if input looks like a full 36-char UUID (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx).
func isFullUUID(input string) bool {
	if len(input) != 36 {
		return false
	}
	return org.IsUUIDLike(input)
}

// ResolveRule resolves a user-provided name or ID to exactly one rule revision UUID.
//
// Resolution order:
//  1. Full UUID (36 chars) → return immediately, no API call needed
//  2. Exact name match (case-sensitive)
//  3. UUID prefix / short ID match (if input looks UUID-like)
//  4. Fuzzy name match (substring, case-insensitive)
//
// When multiple revisions match (same name, different releases), use
// releaseFilter to disambiguate. If releaseFilter is empty and there are
// multiple matches, an error is returned suggesting --release.
func ResolveRule(rules []RuleRevisionTiny, input string, releaseFilter string) (*ResolvedRule, error) {
	if input == "" {
		return nil, fmt.Errorf("rule identifier cannot be empty")
	}

	// 1. Full UUID — fast path, no list needed
	if isFullUUID(input) {
		return &ResolvedRule{ID: input}, nil
	}

	if len(rules) == 0 {
		return nil, fmt.Errorf("no rules found in the current organization")
	}

	// 2. Exact name match (case-sensitive)
	exactMatches := findExactNameMatches(rules, input)
	if len(exactMatches) > 0 {
		return selectFromMatches(exactMatches, input, releaseFilter)
	}

	// 2.5. If input contains uppercase, try slugified form as exact match
	if containsUpper(input) {
		slugified := toSlug(input)
		slugMatches := findExactNameMatches(rules, slugified)
		if len(slugMatches) > 0 {
			return selectFromMatches(slugMatches, slugified, releaseFilter)
		}
	}

	// 3. UUID prefix / short ID match
	if org.IsUUIDLike(input) {
		prefixMatches := findUUIDPrefixMatches(rules, input)
		if len(prefixMatches) == 1 {
			r := prefixMatches[0]
			return &ResolvedRule{ID: r.ID.String(), Name: r.Name, Release: r.Release}, nil
		}
		if len(prefixMatches) > 1 {
			return nil, formatAmbiguousUUIDError(input, prefixMatches)
		}
	}

	// 4. Fuzzy name match (case-insensitive substring)
	fuzzyMatches := findFuzzyNameMatches(rules, input)
	if len(fuzzyMatches) > 0 {
		return selectFromMatches(fuzzyMatches, input, releaseFilter)
	}

	return nil, formatNoMatchError(input, rules)
}

// selectFromMatches picks exactly one revision from a set of matched rules.
// Returns an error when ambiguous, distinguishing between multiple distinct
// rule names and multiple releases of the same rule.
func selectFromMatches(matches []RuleRevisionTiny, input string, releaseFilter string) (*ResolvedRule, error) {
	// If a release filter is specified, narrow down first
	if releaseFilter != "" {
		var filtered []RuleRevisionTiny
		for _, r := range matches {
			if r.Release == releaseFilter {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) == 0 {
			return nil, formatNoReleaseError(input, releaseFilter, matches)
		}
		matches = filtered
	}

	if len(matches) == 1 {
		r := matches[0]
		return &ResolvedRule{ID: r.ID.String(), Name: r.Name, Release: r.Release}, nil
	}

	// Multiple matches — check if they're different rules or same rule with multiple releases
	distinctNames := distinctRuleNames(matches)
	if len(distinctNames) > 1 {
		return nil, formatAmbiguousNameError(input, distinctNames)
	}

	// Same rule name, multiple releases
	return nil, formatMultiReleaseError(input, matches)
}

// --- matching helpers ---

func findExactNameMatches(rules []RuleRevisionTiny, name string) []RuleRevisionTiny {
	var matches []RuleRevisionTiny
	for _, r := range rules {
		if r.Name == name {
			matches = append(matches, r)
		}
	}
	return matches
}

func findUUIDPrefixMatches(rules []RuleRevisionTiny, prefix string) []RuleRevisionTiny {
	var matches []RuleRevisionTiny
	// Normalize: compare against both the raw UUID and the dash-stripped form
	lowerPrefix := strings.ToLower(strings.ReplaceAll(prefix, "-", ""))
	for _, r := range rules {
		id := r.ID.String()
		idHex := strings.ToLower(strings.ReplaceAll(id, "-", ""))
		if strings.HasPrefix(idHex, lowerPrefix) {
			matches = append(matches, r)
		}
	}
	return matches
}

func findFuzzyNameMatches(rules []RuleRevisionTiny, input string) []RuleRevisionTiny {
	var matches []RuleRevisionTiny
	lowerInput := strings.ToLower(input)
	for _, r := range rules {
		if strings.Contains(strings.ToLower(r.Name), lowerInput) {
			matches = append(matches, r)
		}
	}
	return matches
}

func distinctRuleNames(rules []RuleRevisionTiny) []string {
	seen := make(map[string]bool)
	var names []string
	for _, r := range rules {
		if !seen[r.Name] {
			seen[r.Name] = true
			names = append(names, r.Name)
		}
	}
	return names
}

// --- error formatters ---

func formatAmbiguousUUIDError(prefix string, matches []RuleRevisionTiny) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Multiple rules match ID prefix '%s':\n", prefix))
	for _, r := range matches {
		msg.WriteString(fmt.Sprintf("  - %s  %s (release %s)\n", ShortID(r.ID.String()), r.Name, r.Release))
	}
	msg.WriteString("\nPlease provide a longer prefix or use the rule name.")
	return fmt.Errorf("%s", msg.String())
}

func formatAmbiguousNameError(input string, names []string) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Multiple rules match '%s':\n", input))
	for _, name := range names {
		msg.WriteString(fmt.Sprintf("  - %s\n", name))
	}
	msg.WriteString("\nPlease be more specific with the rule name.")
	return fmt.Errorf("%s", msg.String())
}

func formatNoMatchError(input string, rules []RuleRevisionTiny) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("No rule found matching '%s'.\n", input))

	// Deduplicate names for display
	names := distinctRuleNames(rules)

	if len(names) > 0 {
		msg.WriteString("\nAvailable rules:\n")
		limit := 10
		for i, name := range names {
			if i >= limit {
				msg.WriteString(fmt.Sprintf("  ... and %d more\n", len(names)-limit))
				break
			}
			msg.WriteString(fmt.Sprintf("  - %s\n", name))
		}
	}

	msg.WriteString("\nHint: Use 'qctl get rules' to see all available rules.")
	return fmt.Errorf("%s", msg.String())
}

func formatMultiReleaseError(input string, matches []RuleRevisionTiny) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Rule '%s' has multiple releases:\n", input))
	for _, r := range matches {
		msg.WriteString(fmt.Sprintf("  - %s (release %s, state: %s)\n", ShortID(r.ID.String()), r.Release, r.State))
	}
	msg.WriteString("\nSpecify which release with --release, e.g.:\n")
	msg.WriteString(fmt.Sprintf("  qctl <command> rule %s --release %s", input, matches[0].Release))
	return fmt.Errorf("%s", msg.String())
}

// ResolveRuleAny resolves a user-provided name or ID to any matching rule revision UUID.
// Unlike ResolveRule, it does NOT error when multiple releases of the same rule exist —
// it returns the first match instead. It still errors on genuinely ambiguous cases
// (multiple distinct rule names, ambiguous UUID prefix).
func ResolveRuleAny(rules []RuleRevisionTiny, input string) (*ResolvedRule, error) {
	if input == "" {
		return nil, fmt.Errorf("rule identifier cannot be empty")
	}

	// 1. Full UUID — fast path, no list needed
	if isFullUUID(input) {
		return &ResolvedRule{ID: input}, nil
	}

	if len(rules) == 0 {
		return nil, fmt.Errorf("no rules found in the current organization")
	}

	// 2. Exact name match (case-sensitive)
	exactMatches := findExactNameMatches(rules, input)
	if len(exactMatches) > 0 {
		return selectAnyFromMatches(exactMatches, input)
	}

	// 2.5. If input contains uppercase, try slugified form as exact match
	if containsUpper(input) {
		slugified := toSlug(input)
		slugMatches := findExactNameMatches(rules, slugified)
		if len(slugMatches) > 0 {
			return selectAnyFromMatches(slugMatches, slugified)
		}
	}

	// 3. UUID prefix / short ID match
	if org.IsUUIDLike(input) {
		prefixMatches := findUUIDPrefixMatches(rules, input)
		if len(prefixMatches) == 1 {
			r := prefixMatches[0]
			return &ResolvedRule{ID: r.ID.String(), Name: r.Name, Release: r.Release}, nil
		}
		if len(prefixMatches) > 1 {
			return nil, formatAmbiguousUUIDError(input, prefixMatches)
		}
	}

	// 4. Fuzzy name match (case-insensitive substring)
	fuzzyMatches := findFuzzyNameMatches(rules, input)
	if len(fuzzyMatches) > 0 {
		return selectAnyFromMatches(fuzzyMatches, input)
	}

	return nil, formatNoMatchError(input, rules)
}

// selectAnyFromMatches picks one revision from a set of matched rules.
// When multiple releases of the same rule match, returns the first one (no error).
// Errors only when multiple distinct rule names match.
func selectAnyFromMatches(matches []RuleRevisionTiny, input string) (*ResolvedRule, error) {
	if len(matches) == 1 {
		r := matches[0]
		return &ResolvedRule{ID: r.ID.String(), Name: r.Name, Release: r.Release}, nil
	}

	// Multiple matches — check if they're different rules or same rule with multiple releases
	distinctNames := distinctRuleNames(matches)
	if len(distinctNames) > 1 {
		return nil, formatAmbiguousNameError(input, distinctNames)
	}

	// Same rule name, multiple releases — return first match
	r := matches[0]
	return &ResolvedRule{ID: r.ID.String(), Name: r.Name, Release: r.Release}, nil
}

// BoolPtr returns a pointer to the given bool value.
func BoolPtr(b bool) *bool { return &b }

func formatNoReleaseError(input string, release string, matches []RuleRevisionTiny) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Rule '%s' has no release '%s'.\n", input, release))
	msg.WriteString("\nAvailable releases:\n")
	for _, r := range matches {
		msg.WriteString(fmt.Sprintf("  - %s (state: %s)\n", r.Release, r.State))
	}
	return fmt.Errorf("%s", msg.String())
}
