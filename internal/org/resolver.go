// Package org provides organization resolution utilities for qctl.
package org

import (
	"fmt"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Match represents a matched organization
type Match struct {
	ID   string
	Name string
}

// Resolver handles organization name/ID resolution against a list of organizations.
type Resolver struct {
	orgIDs   []string
	orgNames []string
}

// NewResolver creates a resolver from parallel slices of org IDs and names.
// The slices must be the same length with corresponding indices.
func NewResolver(orgIDs, orgNames []string) *Resolver {
	return &Resolver{
		orgIDs:   orgIDs,
		orgNames: orgNames,
	}
}

// Resolve resolves an input string to an organization ID and name.
// Resolution order:
//  1. Exact name match (case-sensitive)
//  2. Full UUID match (case-insensitive)
//  3. UUID prefix match (if input looks like a UUID)
//  4. Fuzzy name match (case-insensitive substring)
//
// Returns (orgID, orgName, error). On ambiguous or no match, returns an error
// with helpful suggestions.
func (r *Resolver) Resolve(input string) (string, string, error) {
	if input == "" {
		return "", "", qerrors.NewBadUsage("organization identifier cannot be empty")
	}

	// 1. Try exact name match (case-sensitive)
	for i, name := range r.orgNames {
		if name == input && i < len(r.orgIDs) {
			return r.orgIDs[i], name, nil
		}
	}

	// 2. Try full UUID match (case-insensitive)
	for i, id := range r.orgIDs {
		if strings.EqualFold(id, input) {
			name := ""
			if i < len(r.orgNames) {
				name = r.orgNames[i]
			}
			return id, name, nil
		}
	}

	// 3. Try UUID prefix match (if input looks like a UUID)
	if IsUUIDLike(input) {
		matches := r.FindUUIDMatches(input)
		if len(matches) == 1 {
			return matches[0].ID, matches[0].Name, nil
		} else if len(matches) > 1 {
			return "", "", FormatAmbiguousUUIDError(input, matches)
		}
		// If no UUID matches, continue to fuzzy matching
	}

	// 4. Try fuzzy name matching (substring, case-insensitive)
	matches := r.FindFuzzyNameMatches(input)
	switch len(matches) {
	case 0:
		return "", "", FormatNoMatchError(input)
	case 1:
		// Single fuzzy match - return it (caller can prompt for confirmation if needed)
		return matches[0].ID, matches[0].Name, nil
	default:
		return "", "", FormatAmbiguousFuzzyError(input, matches)
	}
}

// IsUUIDLike checks if the input looks like a UUID or UUID prefix.
// Returns true if the input contains only hex characters and dashes, with at
// least one hex character.
func IsUUIDLike(input string) bool {
	hasHex := false
	for _, r := range input {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			hasHex = true
		case r == '-':
		default:
			return false
		}
	}
	return hasHex
}

// FindUUIDMatches finds organizations where the UUID starts with the given prefix.
func (r *Resolver) FindUUIDMatches(prefix string) []Match {
	var matches []Match
	lowerPrefix := strings.ToLower(prefix)

	for i, id := range r.orgIDs {
		if strings.HasPrefix(strings.ToLower(id), lowerPrefix) {
			name := ""
			if i < len(r.orgNames) {
				name = r.orgNames[i]
			}
			matches = append(matches, Match{ID: id, Name: name})
		}
	}

	return matches
}

// FindFuzzyNameMatches finds organizations where the name contains the input (case-insensitive).
func (r *Resolver) FindFuzzyNameMatches(substring string) []Match {
	var matches []Match
	lowerSubstring := strings.ToLower(substring)

	for i, name := range r.orgNames {
		if strings.Contains(strings.ToLower(name), lowerSubstring) && i < len(r.orgIDs) {
			matches = append(matches, Match{ID: r.orgIDs[i], Name: name})
		}
	}

	return matches
}

// FormatAmbiguousUUIDError formats an error for ambiguous UUID prefix match.
func FormatAmbiguousUUIDError(prefix string, matches []Match) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Multiple organizations match UUID prefix '%s':\n", prefix))
	for _, match := range matches {
		if match.Name != "" {
			msg.WriteString(fmt.Sprintf("  - %s (%s)\n", match.ID, match.Name))
		} else {
			msg.WriteString(fmt.Sprintf("  - %s\n", match.ID))
		}
	}
	msg.WriteString("\nPlease provide a longer prefix.")
	return qerrors.NewBadUsage(msg.String())
}

// FormatAmbiguousFuzzyError formats an error for ambiguous fuzzy name match.
func FormatAmbiguousFuzzyError(input string, matches []Match) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Multiple organizations match '%s':\n", input))
	for _, match := range matches {
		msg.WriteString(fmt.Sprintf("  - %s\n", match.Name))
	}
	msg.WriteString("\nPlease be more specific with the organization name.")
	return qerrors.NewBadUsage(msg.String())
}

// FormatNoMatchError formats an error when no match is found.
func FormatNoMatchError(input string) error {
	return qerrors.NewBadUsagef("No organization found matching %q in the local cache.\n"+
		"Ask the user which organization to use, then pass --org <full-organization-UUID>.", input)
}
