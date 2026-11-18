package datasets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// GetTablesOverview reads one page of standalone tables and series aggregates.
func (c *Client) GetTablesOverview(token string, params api.GetTablesOverviewParams) (*api.TablesOverviewListSchema, error) {
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: token,
		Timeout: client.ResolveTimeout(c.timeout), Verbosity: c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	resp, err := apiClient.API.GetTablesOverviewWithResponse(context.Background(), c.organizationID, &params)
	if err != nil {
		return nil, apiClient.HandleError(err, "get tables overview failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "get tables overview failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty tables overview response")
	}
	return resp.JSON200, nil
}
