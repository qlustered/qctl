package change_proposals //nolint:revive // Established package-path style in this repo (rule_reviews, privilege_requests).

import "github.com/qlustered/qctl/internal/comments"

// FetchUsers sweeps the org's membership into a Directory for UUID→name
// display. Fetch once per command invocation.
func (c *Client) FetchUsers(accessToken string) (*comments.Directory, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	return comments.FetchUsers(apiClient, c.organizationID)
}
