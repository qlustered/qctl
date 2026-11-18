package alerts

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// ResolveResult is the backend's resolve response: frontend instructions
// (redirect, refresh, upload URL) and, when a keyword action staged its rule
// change instead of applying it, the staged suggestion's id. It carries no
// status and no job id.
type ResolveResult = api.AlertResolveResponseSchema

// Resolve posts an alert resolution request.
func (c *Client) Resolve(accessToken string, alertID int, body api.AlertResolveSchema) (*ResolveResult, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.ResolveAlertWithResponse(
		context.Background(), c.organizationID, alertID, &api.ResolveAlertParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "resolve alert failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "resolve alert failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}
