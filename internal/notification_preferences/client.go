// Package notification_preferences reads and replaces the caller's
// notification email preferences in one organization.
package notification_preferences //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// Preferences is the server's answer to both routes: the effective email
// setting and the role default for every notification type that has one.
type Preferences = api.NotificationEmailPreferencesResponse

// Client handles HTTP requests for notification email preferences.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a notification email preferences client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
}

func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	return apiClient, nil
}

// Get returns the caller's effective email preferences and role defaults.
func (c *Client) Get(accessToken string) (*Preferences, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetMyNotificationEmailPreferencesWithResponse(
		context.Background(), c.organizationID, &api.GetMyNotificationEmailPreferencesParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get notification email preferences failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get notification email preferences failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Put replaces the caller's stored overrides with overrides and returns the
// resulting preferences. A nil or empty map sends {}, which restores every
// role default.
func (c *Client) Put(accessToken string, overrides map[string]bool) (*Preferences, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	if overrides == nil {
		overrides = map[string]bool{}
	}
	resp, err := apiClient.API.PutMyNotificationEmailPreferencesWithResponse(
		context.Background(), c.organizationID, &api.PutMyNotificationEmailPreferencesParams{},
		api.NotificationEmailPreferencesRequest{Preferences: overrides})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "update notification email preferences failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "update notification email preferences failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
