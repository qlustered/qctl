package rule_reviews //nolint:revive // Established package path; renaming would break imports.

import "github.com/qlustered/qctl/internal/comments"

// FetchUsers sweeps the org's membership into a Directory for name→UUID
// resolution and UUID→name display. Fetch once per command invocation.
func (c *Client) FetchUsers(accessToken string) (*Directory, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	return comments.FetchUsers(apiClient, c.organizationID)
}
