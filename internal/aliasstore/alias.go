// Package aliasstore persists permanent, server-scoped aliases for UUID-keyed
// resources (e.g. dataset-rule UUID -> "r-1") in an embedded bbolt key-value store.
package aliasstore

import (
	"fmt"
	"strconv"
	"strings"
)

// ResourceType identifies the kind of resource a UUID belongs to.
type ResourceType string

const (
	// ResourceDatasetRule is the dataset-rule (CLI "table-rule") resource; alias prefix "r".
	ResourceDatasetRule ResourceType = "dataset_rule"

	// ResourceRuleRevision is the rule-revision resource. It is deliberately
	// absent from resourcePrefixes: rule revisions are never given an ordinal
	// alias, only cached by short ID (see shortid.go).
	ResourceRuleRevision ResourceType = "rule_revision"
)

// resourcePrefixes is the single source of truth for resource <-> alias-prefix mapping.
// Adding a new aliased resource (e.g. users as "u") means adding one row here.
var resourcePrefixes = []struct {
	rt     ResourceType
	prefix string
}{
	{ResourceDatasetRule, "r"},
}

// prefixFor returns the alias prefix for a resource type.
func prefixFor(rt ResourceType) (string, error) {
	for _, p := range resourcePrefixes {
		if p.rt == rt {
			return p.prefix, nil
		}
	}
	return "", fmt.Errorf("no alias prefix registered for resource type %q", rt)
}

// ResourceTypeForPrefix returns the resource type for an alias prefix.
func ResourceTypeForPrefix(prefix string) (ResourceType, error) {
	for _, p := range resourcePrefixes {
		if p.prefix == prefix {
			return p.rt, nil
		}
	}
	return "", fmt.Errorf("unknown alias prefix %q", prefix)
}

// ParseAlias splits "<prefix>-<ordinal>" into its parts. The ordinal must be a
// positive integer. It does NOT check that the prefix is registered.
func ParseAlias(alias string) (prefix string, ordinal int, err error) {
	parts := strings.Split(alias, "-")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("malformed alias %q: expected form <prefix>-<number>", alias)
	}
	prefix = parts[0]
	if prefix == "" {
		return "", 0, fmt.Errorf("malformed alias %q: empty prefix", alias)
	}
	n, convErr := strconv.Atoi(parts[1])
	if convErr != nil || n < 1 {
		return "", 0, fmt.Errorf("malformed alias %q: ordinal must be a positive integer", alias)
	}
	return prefix, n, nil
}

// LooksLikeAlias reports whether input has the shape of an alias (<prefix>-<positive int>).
// It is a cheap syntactic check used to decide whether to attempt alias resolution.
func LooksLikeAlias(input string) bool {
	_, _, err := ParseAlias(input)
	return err == nil
}
