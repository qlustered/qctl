package datasets

// WithThirdParty selects third-party data and table-name lookup routes on a copy of the client.
func (c *Client) WithThirdParty(enabled bool) *Client {
	selected := *c
	selected.thirdParty = enabled
	return &selected
}
