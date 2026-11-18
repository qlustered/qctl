package deliverables

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// GetRunDownloadURL mints a download URL and records a download audit.
func (c *Client) GetRunDownloadURL(token string, runID int) (*api.DownloadURLResponse, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetDeliverableRunDownloadURLWithResponse(context.Background(), c.orgID, runID, &api.GetDeliverableRunDownloadURLParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get deliverable run download URL failed", "get_deliverable_run_download_url")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get deliverable run download URL failed", "get_deliverable_run_download_url")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable run download URL response")
	}
	return resp.JSON200, nil
}
