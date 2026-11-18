// Package slack_config reads and replaces an organization's Slack use case
// config.
package slack_config //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// Config is an organization's effective Slack config, as both routes answer it.
type Config = api.OrgSlackConfig

// AlertTypeEntry is one alert type's change to its tier.
type AlertTypeEntry = api.SlackAlertTypeConfig

// Client handles HTTP requests for an organization's Slack config.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
}

// NewClient creates a Slack config client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{baseURL: baseURL, organizationID: orgUUID, verbosity: verbosity}
}

func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	return apiClient, nil
}

// Get returns the organization's effective Slack config.
func (c *Client) Get(accessToken string) (*Config, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetOrgSlackConfigWithResponse(
		context.Background(), c.organizationID, &api.GetOrgSlackConfigParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get Slack config failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get Slack config failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Put replaces the organization's Slack config with config and returns the
// effective config.
func (c *Client) Put(accessToken string, config Config) (*Config, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PutOrgSlackConfigWithResponse(
		context.Background(), c.organizationID, &api.PutOrgSlackConfigParams{}, config)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "update Slack config failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "update Slack config failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
