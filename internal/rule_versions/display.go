package rule_versions

import (
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/tags"
)

// RuleRevisionDisplay wraps RuleRevisionTiny with computed fields for table output.
// ID is stored as a string so the table printer renders it properly (the
// underlying openapi_types.UUID is [16]byte which would display as a byte array).
type RuleRevisionDisplay struct {
	CreatedAt            time.Time                   `json:"created_at"`
	CreatedByUser        *api.UserInfoTinyDictSchema `json:"created_by_user,omitempty"`
	Description          *string                     `json:"description"`
	ID                   string                      `json:"id"`
	InteractsWithColumns []string                    `json:"interacts_with_columns"`
	Name                 string                      `json:"name"`
	Release              string                      `json:"release"`
	State                RuleState                   `json:"state"`
	Tags                 string                      `json:"tags"`
	UpdatedAt            time.Time                   `json:"updated_at"`
	ShortID              string                      `json:"short_id"`
}

// ToDisplayList converts API results to display structs with computed ShortID and Tags.
func ToDisplayList(rules []RuleRevisionTiny) []RuleRevisionDisplay {
	// Pre-scan: build set of rule names that have at least one newer-than-default revision.
	namesWithUpgrade := make(map[string]bool)
	for _, r := range rules {
		if r.UpgradeAvailable != nil && *r.UpgradeAvailable {
			namesWithUpgrade[r.Name] = true
		}
	}

	result := make([]RuleRevisionDisplay, len(rules))
	for i, r := range rules {
		newerThanDefault := r.UpgradeAvailable != nil && *r.UpgradeAvailable
		updateAvailable := r.IsDefault && namesWithUpgrade[r.Name]
		result[i] = RuleRevisionDisplay{
			CreatedAt:            r.CreatedAt,
			CreatedByUser:        r.CreatedByUser,
			Description:          r.Description,
			ID:                   r.ID.String(),
			InteractsWithColumns: r.InteractsWithColumns,
			Name:                 r.Name,
			Release:              r.Release,
			State:                r.State,
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
