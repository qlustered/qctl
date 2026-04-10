package rule_families

import (
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/tags"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

// RuleFamilyDisplay is the flat display struct for table output.
// Each RuleFamilyItem produces 1 row (primary revision) or 2 rows
// (primary + secondary revision with "Newer than default" in tags).
type RuleFamilyDisplay struct {
	Slug      string `json:"slug"`
	Release   string `json:"release"`
	State     string `json:"state"`
	Tags      string `json:"tags"`
	Author    string `json:"author"`
	ShortID   string `json:"short_id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ToDisplayList converts API results to display rows.
// Each family emits 1 row for its primary revision, and optionally
// a 2nd row for a secondary (newer-than-default) revision with "Newer than default" tag.
func ToDisplayList(families []RuleFamilyItem) []RuleFamilyDisplay {
	var result []RuleFamilyDisplay
	for _, f := range families {
		hasNewer := f.SecondaryRevision != nil
		if f.PrimaryRevision != nil {
			result = append(result, revisionToDisplay(f.Slug, f.IsBuiltin, f.PrimaryRevision, hasNewer, false))
		}
		if f.SecondaryRevision != nil {
			row := revisionToDisplay(f.Slug, f.IsBuiltin, f.SecondaryRevision, false, true)
			result = append(result, row)
		}
	}
	return result
}

func revisionToDisplay(slug string, isBuiltin bool, rev *RuleFamilyRevisionTiny, updateAvailable bool, newerThanDefault bool) RuleFamilyDisplay {
	return RuleFamilyDisplay{
		Slug:    slug,
		Release: rev.Release,
		State:   string(rev.State),
		Tags: tags.Build(
			tags.TagPair{Label: "Default", Active: rev.IsDefault},
			tags.TagPair{Label: "Built-in", Active: isBuiltin},
			tags.TagPair{Label: "Update available", Active: updateAvailable},
			tags.TagPair{Label: "Newer than default", Active: newerThanDefault},
		),
		Author:    formatAuthor(rev.CreatedByUser),
		ShortID:   shortID(rev.ID.String()),
		CreatedAt: timeutil.FormatRelative(rev.CreatedAt),
		UpdatedAt: timeutil.FormatRelative(rev.UpdatedAt),
	}
}

// formatAuthor formats a user's name for display: "FirstName L." (first name truncated to 10 chars).
func formatAuthor(user *api.UserInfoTinyDictSchema) string {
	if user == nil {
		return "-"
	}
	first := strings.TrimSpace(user.FirstName)
	last := strings.TrimSpace(user.LastName)
	if first == "" && last == "" {
		return "-"
	}
	if len(first) > 10 {
		first = first[:10]
	}
	if last != "" {
		return first + " " + string(last[0]) + "."
	}
	return first
}

// shortID returns the first 8 hex characters of a UUID, stripping dashes.
func shortID(uuid string) string {
	hex := strings.ReplaceAll(uuid, "-", "")
	if len(hex) > 8 {
		return hex[:8]
	}
	return hex
}
