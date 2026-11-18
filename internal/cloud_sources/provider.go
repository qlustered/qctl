package cloud_sources

// WithThirdParty selects third-party row routes on a copy of the client.
func (c *Client) WithThirdParty(enabled bool) *Client {
	selected := *c
	selected.thirdParty = enabled
	return &selected
}
