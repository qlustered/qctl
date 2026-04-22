package orgs

import (
	"strings"

	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

// OrgDisplay is the flat display struct for table output.
type OrgDisplay struct {
	Current   string `json:"current"`
	Name      string `json:"name"`
	ID        string `json:"id"`
	IsActive  bool   `json:"is_active"`
	CreatedAt string `json:"created_at"`
}

// ToDisplayList converts API results to display rows.
// The currentOrgID is marked with a `*` in the current column.
func ToDisplayList(orgs []OrgItem, currentOrgID string) []OrgDisplay {
	result := make([]OrgDisplay, 0, len(orgs))
	for _, o := range orgs {
		marker := ""
		if o.ID.String() == currentOrgID {
			marker = "*"
		}
		result = append(result, OrgDisplay{
			Current:   marker,
			Name:      o.Name,
			ID:        shortID(o.ID.String()),
			IsActive:  o.IsActive,
			CreatedAt: timeutil.FormatRelative(o.CreatedAt),
		})
	}
	return result
}

// shortID returns the first 8 hex characters of a UUID, stripping dashes.
func shortID(uuid string) string {
	hex := strings.ReplaceAll(uuid, "-", "")
	if len(hex) > 8 {
		return hex[:8]
	}
	return hex
}
