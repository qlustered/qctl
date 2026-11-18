package stored_items

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// CorrectedLine is one corrected cell of a file.
type CorrectedLine = api.CorrectedLineSchema

// CorrectedLineList is one page of a file's corrected cells.
type CorrectedLineList = api.CorrectedLineListSchema

// ListCorrectedLines returns one page of the file's corrected cells.
func (c *Client) ListCorrectedLines(accessToken string, storedItemID, page, limit int) (*CorrectedLineList, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListStoredItemCorrectedLinesParams{Page: positiveOrNil(page), Limit: positiveOrNil(limit)}
	resp, err := apiClient.API.ListStoredItemCorrectedLinesWithResponse(context.Background(), c.organizationID, storedItemID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list corrected lines failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list corrected lines failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
