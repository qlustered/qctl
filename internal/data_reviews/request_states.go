package data_reviews //nolint:revive // Match the established package.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

type RequestStateCounts = api.RequestStateCountsResponseSchema

// RequestStateCounts fetches the approver and sender counts for one group.
func (c *Client) RequestStateCounts(accessToken string, datasetID int, groupID string) (*RequestStateCounts, error) {
	groupUUID, err := parseUUID("review group id", groupID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetRequestStateCountsWithResponse(
		context.Background(), c.organizationID, datasetID, &api.GetRequestStateCountsParams{ReviewGroupID: groupUUID},
	)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "count request states failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "count request states failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
