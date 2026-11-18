// Package stats wraps the organization's stats endpoints. Today that is
// menu-stats, the small payload cosmosview polls for its alert bell: unresolved
// counts plus a pointer to the one alert worth looking at.
//
// It is deliberately not a view over the alert list. The two apply different
// server-side filters, so their counts legitimately disagree and must never be
// compared with each other.
package stats

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// MenuStats is the menu-stats payload.
type MenuStats = api.MenuStatsSchema

// Client handles HTTP requests for stats operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new stats client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
}

// GetMenuStats fetches the stats for one table.
//
// datasetID is required. The backend never populates latest_alert_item_id in
// the org-wide branch for a non-external role
// (get_menu_stats, capi/capi/controllers/stats_controller.py), so an unscoped
// call would answer "no alert" to a user who has several.
func (c *Client) GetMenuStats(accessToken string, datasetID int) (*MenuStats, error) {
	return c.GetMenuStatsContext(context.Background(), accessToken, datasetID)
}

// GetMenuStatsContext is GetMenuStats with ctx controlling the request.
func (c *Client) GetMenuStatsContext(ctx context.Context, accessToken string, datasetID int) (*MenuStats, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	params := &api.GetMenuStatsViewParams{DatasetID: &datasetID}
	resp, err := apiClient.API.GetMenuStatsViewWithResponse(ctx, c.organizationID, params)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
