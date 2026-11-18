package rule_families //nolint:revive // Established package path; renaming would break imports.

import (
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ResolveSlug finds a family's ID by its exact slug across catalog pages.
func (c *Client) ResolveSlug(accessToken, slug string) (uuid.UUID, error) {
	params := GetRuleFamiliesParams{OrderBy: "slug", Limit: 1000}
	if utf8.RuneCountInString(slug) >= 3 {
		params.SearchQuery = &slug
	}
	seen := 0
	for page := 1; ; page++ {
		params.Page = page
		resp, err := c.GetRuleFamilies(accessToken, params)
		if err != nil {
			return uuid.Nil, fmt.Errorf("failed to resolve rule slug %q: %w", slug, err)
		}
		for _, family := range resp.Results {
			if family.Slug == slug {
				return family.FamilyID, nil
			}
		}
		seen += len(resp.Results)
		if len(resp.Results) == 0 || (resp.TotalRows != nil && seen >= *resp.TotalRows) ||
			(resp.TotalRows == nil && resp.Next == nil) {
			return uuid.Nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("no rule family with slug %q in this organization; use 'qctl get rules' to list rule slugs", slug)))
		}
	}
}
