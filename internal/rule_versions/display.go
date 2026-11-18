package rule_versions

import (
	"fmt"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/tags"
)

// RuleRevisionDisplay wraps RuleRevisionTiny with computed fields for table output.
// ID is stored as a string so the table printer renders it properly (the
// underlying openapi_types.UUID is [16]byte which would display as a byte array).
type RuleRevisionDisplay struct {
	CreatedAt       time.Time                   `json:"created_at"`
	CreatedByUser   *api.UserInfoTinyDictSchema `json:"created_by_user,omitempty"`
	Description     *string                     `json:"description"`
	ID              string                      `json:"id"`
	AffectedColumns []string                    `json:"affected_columns"`
	Slug            string                      `json:"slug"`
	Release         string                      `json:"release"`
	State           RuleState                   `json:"state"`
	Tags            string                      `json:"tags"`
	UpdatedAt       time.Time                   `json:"updated_at"`
	ShortID         string                      `json:"short_id"`
}

// RuleRevisionMatchDisplay adds table match details to a revision row.
type RuleRevisionMatchDisplay struct {
	CreatedAt       time.Time                   `json:"created_at"`
	CreatedByUser   *api.UserInfoTinyDictSchema `json:"created_by_user,omitempty"`
	Description     *string                     `json:"description"`
	ID              string                      `json:"id"`
	AffectedColumns []string                    `json:"affected_columns"`
	Slug            string                      `json:"slug"`
	Release         string                      `json:"release"`
	State           RuleState                   `json:"state"`
	Tags            string                      `json:"tags"`
	UpdatedAt       time.Time                   `json:"updated_at"`
	ShortID         string                      `json:"short_id"`
	Match           api.RuleMatchKind           `json:"match"`
	StandardFields  string                      `json:"standard_fields"`
	Missing         string                      `json:"missing"`
}

// MatchToDisplayList adds match details to the ordinary revision projection.
func MatchToDisplayList(matches []api.RuleRevisionMatchSchema) []RuleRevisionMatchDisplay {
	result := make([]RuleRevisionMatchDisplay, len(matches))
	for i, match := range matches {
		base := ToDisplayList([]RuleRevisionTiny{{
			ID: match.ID, Slug: match.Slug, Release: match.Release,
			State: match.State, IsDefault: match.IsDefault,
			IsBuiltin: match.IsBuiltin, IsCaf: match.IsCaf,
			Description: match.Description, AffectedColumns: match.AffectedColumns,
			CreatedAt: match.CreatedAt, CreatedByUser: match.CreatedByUser,
			UpdatedAt:        match.UpdatedAt,
			UpgradeAvailable: match.UpgradeAvailable,
		}})[0]
		missing := make([]string, len(match.MissingConcepts))
		for j, concept := range match.MissingConcepts {
			missing[j] = concept.Slug
		}
		result[i] = RuleRevisionMatchDisplay{
			CreatedAt: base.CreatedAt, CreatedByUser: base.CreatedByUser,
			Slug: base.Slug, Release: base.Release, State: base.State,
			Tags: base.Tags, ShortID: base.ShortID, ID: base.ID,
			Description: base.Description, AffectedColumns: base.AffectedColumns,
			UpdatedAt:      base.UpdatedAt,
			Match:          match.MatchKind,
			StandardFields: fmt.Sprintf("%d/%d", match.MatchedConceptCount, match.RequiredConceptCount),
			Missing:        strings.Join(missing, ", "),
		}
	}
	return result
}

// ToDisplayList converts API results to display structs with computed ShortID and Tags.
func ToDisplayList(rules []RuleRevisionTiny) []RuleRevisionDisplay {
	// Pre-scan: build set of rule names that have at least one newer-than-default revision.
	namesWithUpgrade := make(map[string]bool)
	for _, r := range rules {
		if r.UpgradeAvailable != nil && *r.UpgradeAvailable {
			namesWithUpgrade[r.Slug] = true
		}
	}

	result := make([]RuleRevisionDisplay, len(rules))
	for i, r := range rules {
		newerThanDefault := r.UpgradeAvailable != nil && *r.UpgradeAvailable
		updateAvailable := r.IsDefault && namesWithUpgrade[r.Slug]
		result[i] = RuleRevisionDisplay{
			CreatedAt:       r.CreatedAt,
			CreatedByUser:   r.CreatedByUser,
			Description:     r.Description,
			ID:              r.ID.String(),
			AffectedColumns: r.AffectedColumns,
			Slug:            r.Slug,
			Release:         r.Release,
			State:           r.State,
			Tags: tags.Build(
				tags.TagPair{Label: "Default", Active: r.IsDefault},
				tags.TagPair{Label: "Built-in", Active: r.IsBuiltin},
				tags.TagPair{Label: "CAF", Active: r.IsCaf},
				tags.TagPair{Label: "Update available", Active: updateAvailable},
				tags.TagPair{Label: "Newer than default", Active: newerThanDefault},
			),
			UpdatedAt: r.UpdatedAt,
			ShortID:   ShortID(r.ID.String()),
		}
	}
	return result
}
