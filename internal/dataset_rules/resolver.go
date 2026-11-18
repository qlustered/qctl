package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/org"
	"github.com/qlustered/qctl/internal/rule_versions"
)

// ResolvedDatasetRule holds the result of name/ID resolution.
type ResolvedDatasetRule struct {
	ID           string // full UUID of the dataset rule
	InstanceName string // instance name
}

// isFullUUID checks if input looks like a full 36-char UUID.
func isFullUUID(input string) bool {
	if len(input) != 36 {
		return false
	}
	return org.IsUUIDLike(input)
}

// ResolveDatasetRule resolves a user-provided name or ID to exactly one dataset rule UUID.
//
// Resolution order:
//  1. Full UUID (36 chars) → return immediately, no further matching needed
//  2. Exact instance_name match (case-sensitive)
//  3. UUID prefix / short ID match (if input looks UUID-like)
//  4. Fuzzy instance_name match (substring, case-insensitive)
func ResolveDatasetRule(rules []DatasetRuleTiny, input string) (*ResolvedDatasetRule, error) {
	if input == "" {
		return nil, fmt.Errorf("table-rule identifier cannot be empty")
	}

	// 1. Full UUID — fast path, no list needed
	if isFullUUID(input) {
		return &ResolvedDatasetRule{ID: input}, nil
	}

	if len(rules) == 0 {
		return nil, fmt.Errorf("no table-rules found for this table")
	}

	// 2. Exact instance_name match (case-sensitive)
	exactMatches := findExactInstanceNameMatches(rules, input)
	if len(exactMatches) == 1 {
		r := exactMatches[0]
		return &ResolvedDatasetRule{ID: r.ID.String(), InstanceName: r.InstanceName}, nil
	}
	if len(exactMatches) > 1 {
		return nil, formatAmbiguousError(input, exactMatches)
	}

	// 3. UUID prefix / short ID match
	if org.IsUUIDLike(input) {
		prefixMatches := findUUIDPrefixMatches(rules, input)
		if len(prefixMatches) == 1 {
			r := prefixMatches[0]
			return &ResolvedDatasetRule{ID: r.ID.String(), InstanceName: r.InstanceName}, nil
		}
		if len(prefixMatches) > 1 {
			return nil, formatAmbiguousError(input, prefixMatches)
		}
	}

	// 4. Fuzzy instance_name match (case-insensitive substring)
	fuzzyMatches := findFuzzyInstanceNameMatches(rules, input)
	if len(fuzzyMatches) == 1 {
		r := fuzzyMatches[0]
		return &ResolvedDatasetRule{ID: r.ID.String(), InstanceName: r.InstanceName}, nil
	}
	if len(fuzzyMatches) > 1 {
		return nil, formatAmbiguousError(input, fuzzyMatches)
	}

	return nil, formatNoMatchError(input, rules)
}

// --- matching helpers ---

func findExactInstanceNameMatches(rules []DatasetRuleTiny, name string) []DatasetRuleTiny {
	var matches []DatasetRuleTiny
	for _, r := range rules {
		if r.InstanceName == name {
			matches = append(matches, r)
		}
	}
	return matches
}

func findUUIDPrefixMatches(rules []DatasetRuleTiny, prefix string) []DatasetRuleTiny {
	var matches []DatasetRuleTiny
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

func findFuzzyInstanceNameMatches(rules []DatasetRuleTiny, input string) []DatasetRuleTiny {
	var matches []DatasetRuleTiny
	lowerInput := strings.ToLower(input)
	for _, r := range rules {
		if strings.Contains(strings.ToLower(r.InstanceName), lowerInput) {
			matches = append(matches, r)
		}
	}
	return matches
}

// --- error formatters ---

func formatAmbiguousError(input string, matches []DatasetRuleTiny) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Multiple table-rules match '%s':\n", input))
	for _, r := range matches {
		msg.WriteString(fmt.Sprintf("  - %s  %s (release %s)\n", rule_versions.ShortID(r.ID.String()), r.InstanceName, r.Release))
	}
	msg.WriteString("\nPlease be more specific.")
	return fmt.Errorf("%s", msg.String())
}

func formatNoMatchError(input string, rules []DatasetRuleTiny) error {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("No table-rule found matching '%s'.\n", input))

	if len(rules) > 0 {
		msg.WriteString("\nAvailable table-rules:\n")
		limit := 10
		for i, r := range rules {
			if i >= limit {
				msg.WriteString(fmt.Sprintf("  ... and %d more\n", len(rules)-limit))
				break
			}
			msg.WriteString(fmt.Sprintf("  - %s  %s\n", rule_versions.ShortID(r.ID.String()), r.InstanceName))
		}
	}

	msg.WriteString("\nHint: Use 'qctl get table-rules --table-id <id>' to see all table rules.")
	return fmt.Errorf("%s", msg.String())
}

// ResolveManifestRule expands the manifest's rule identifier to the full UUID
// the API needs, in place on spec.rule_revision_id, so every downstream use --
// the details fetch, the confirmation block, the patch body, the request path
// -- sees the resolved value. It mirrors resolveRuleSpecs in
// create/dry_run_job.go.
//
// The rule may be named two ways, mutually exclusive: spec.rule_revision_id
// (a full UUID, a SHORT-ID or a slug), or spec.rule (a name or slug) paired
// with an optional spec.release when the name has several releases.
//
// It is mode-agnostic: `qctl apply -f` uses it for both instantiate and the
// patch-mode rebind, and `qctl diff -f` uses it so its preview is built from
// the same resolved body apply would send. source names the manifest for
// error messages -- apply passes doc.Source, diff the file path.
//
// Returns the identifier the manifest actually carried when it was not already
// a full UUID, so the confirmation can show both; "" when nothing was resolved.
func ResolveManifestRule(rvClient *rule_versions.Client, token string, manifest *TableRuleApplyManifest, source string) (string, error) {
	spec := &manifest.Spec
	if spec.Rule != "" && spec.RuleRevisionID != "" {
		return "", qerrors.NewBadUsagef("spec.rule and spec.rule_revision_id are mutually exclusive in %s; set one or the other", source)
	}
	if spec.Rule == "" && spec.Release != "" {
		return "", qerrors.NewBadUsagef("spec.release needs spec.rule in %s; a rule_revision_id already names one release", source)
	}

	input, release := spec.RuleRevisionID, ""
	if spec.Rule != "" {
		input, release = spec.Rule, spec.Release
	}
	if _, err := uuid.Parse(input); err == nil {
		spec.RuleRevisionID = input
		return "", nil
	}

	resolvedID, err := rvClient.ResolveRuleID(token, input, release)
	if err != nil {
		return "", fmt.Errorf("%s: %w", source, err)
	}
	spec.RuleRevisionID = resolvedID
	shown := input
	if release != "" {
		shown = input + "@" + release
	}
	return shown, nil
}

// ResolvePatchRebind resolves a patch manifest's rebind target when it names
// one, and is a no-op when it does not, so a patch that touches no rule never
// needs the rule-revision catalog. It is the single entry point both
// `qctl apply -f` and `qctl diff -f` use for the patch path; the resolved
// spec.rule_revision_id is what BuildPatchMap then puts on the wire. Returns
// the original non-UUID spelling so apply can annotate its confirmation; diff
// needs only the rewritten manifest and discards it.
func ResolvePatchRebind(rvClient *rule_versions.Client, token string, manifest *TableRuleApplyManifest, source string) (string, error) {
	if manifest.Spec.Rule == "" && manifest.Spec.Release == "" && manifest.Spec.RuleRevisionID == "" {
		return "", nil
	}
	return ResolveManifestRule(rvClient, token, manifest, source)
}
