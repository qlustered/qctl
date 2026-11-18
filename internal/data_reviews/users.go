package data_reviews //nolint:revive // Mirrors the backend's data-review arc, like rule_reviews.

import "github.com/qlustered/qctl/internal/comments"

// FetchUsers sweeps the org's membership into a Directory for name→UUID
// resolution and UUID→name display. Fetch once per command invocation: every
// --reviewer / --mention value on the data arc resolves through it, and the
// API only ever receives UUIDs.
func (c *Client) FetchUsers(accessToken string) (*Directory, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	return comments.FetchUsers(apiClient, c.organizationID)
}

// FetchMentionableUsers reads the people a comment on datasetID may mention
// into a Directory.
func (c *Client) FetchMentionableUsers(accessToken string, datasetID int) (*Directory, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	return comments.FetchMentionableUsers(apiClient, c.organizationID, datasetID)
}
