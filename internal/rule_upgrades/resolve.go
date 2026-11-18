//nolint:revive // Established package-path style; internal/rule_families and internal/rule_versions do the same.
package rule_upgrades

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/rule_versions"
)

// ResolvedFamily is a rule family plus every release of it, which is what the
// two rollout routes need and what the user cannot supply: upgrade-details
// takes a FAMILY id, and blast takes the family's DEFAULT revision id, while
// `qctl get rules` and `qctl get rule-revisions` both print REVISION ids.
type ResolvedFamily struct {
	ID   string
	Name string
	// RevisionID is the revision the input resolved THROUGH, which is not
	// necessarily one the caller wanted: a family name lands on an arbitrary
	// release. `rule-upgrade notify` compares it to the revision it is about to
	// blast so a pasted non-default revision id is not substituted in silence.
	RevisionID string
	Releases   []api.RuleRevisionTinySchema
}

// Default returns the family's default revision, or nil when it has none.
// A family with no default is a real state: upgrade-details answers
// new_default_revision_id: null for it, with no outdated items.
func (f *ResolvedFamily) Default() *api.RuleRevisionTinySchema {
	for i := range f.Releases {
		if f.Releases[i].IsDefault {
			return &f.Releases[i]
		}
	}
	return nil
}

// ByRelease returns the revision with this release string, or nil.
func (f *ResolvedFamily) ByRelease(release string) *api.RuleRevisionTinySchema {
	for i := range f.Releases {
		if f.Releases[i].Release == release {
			return &f.Releases[i]
		}
	}
	return nil
}

// ResolveFamily turns the positional argument every rule command accepts — a
// family name, a rule-revision short ID, or a rule-revision UUID — into the
// family behind it.
//
// The LENIENT resolver is deliberate. ResolveRuleID would reject a family with
// several releases for want of --release, but both questions this package asks
// are family-scoped, so any release of the family is an equally good landing
// point. A family UUID is NOT accepted: it resolves as a rule revision and
// fails, which the resource page documents, because inventing a second id
// vocabulary for one command is worse than the gap.
func ResolveFamily(rv *rule_versions.Client, accessToken, input string) (*ResolvedFamily, error) {
	revisionID, err := rv.ResolveRuleIDAny(accessToken, input)
	if err != nil {
		return nil, err
	}
	family, err := rv.GetRuleRevisionAllReleases(accessToken, revisionID)
	if err != nil {
		return nil, fmt.Errorf("failed to look up the rule family for %q: %w", input, err)
	}
	return &ResolvedFamily{
		ID:         family.FamilyID.String(),
		Name:       family.Name,
		RevisionID: revisionID,
		Releases:   family.Results,
	}, nil
}
