package datasets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// GetSoftDeletedTables reads the ops report of soft-deleted tables. A nil
// minAgeDays leaves the age threshold to the server.
func (c *Client) GetSoftDeletedTables(token string, minAgeDays *int) (*api.SoftDeletedTablesReportSchema, error) {
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: token,
		Timeout: client.ResolveTimeout(c.timeout), Verbosity: c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	params := &api.GetSoftDeletedTablesParams{MinAgeDays: minAgeDays}
	resp, err := apiClient.API.GetSoftDeletedTablesWithResponse(context.Background(), c.organizationID, params)
	if err != nil {
		return nil, apiClient.HandleError(err, "get soft-deleted tables failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "get soft-deleted tables failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty soft-deleted tables response")
	}
	return resp.JSON200, nil
}
