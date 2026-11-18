package stored_items

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// DroppedLine is one line of a file that did not load, and the delete that
// dropped it.
type DroppedLine = api.DroppedLineSchema

// DroppedLineValue is one displayed value of the deleted row.
type DroppedLineValue = api.DroppedLineValueSchema

// DroppedLineList is one page of a file's dropped lines.
type DroppedLineList = api.DroppedLineListSchema

// ListDroppedLines returns one page of the file's dropped lines
// (GET /stored-items/{id}/dropped-lines). A page or limit of zero is not sent.
// A non-2xx answer is an *APIError.
func (c *Client) ListDroppedLines(accessToken string, storedItemID, page, limit int) (*DroppedLineList, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListStoredItemDroppedLinesParams{Page: positiveOrNil(page), Limit: positiveOrNil(limit)}
	resp, err := apiClient.API.ListStoredItemDroppedLinesWithResponse(context.Background(), c.organizationID, storedItemID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list dropped lines failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list dropped lines failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

func positiveOrNil(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}
