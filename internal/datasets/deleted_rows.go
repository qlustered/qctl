package datasets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// DeletedRow is one deleted row and the delete that removed it.
type DeletedRow = api.DeletedRowSchema

// DeletedRowFile is one file a deleted row came from.
type DeletedRowFile = api.DeletedRowFileSchema

// DeletedRowList is one page of a table's deleted rows.
type DeletedRowList = api.DeletedRowListSchema

// DeletedRowsFilter narrows ListDeletedRows. A zero field is not sent.
type DeletedRowsFilter struct {
	CloudSourceID int
	FileID        int
	DeleteID      int
	Page          int
	Limit         int
}

// ListDeletedRows returns one page of the table's deleted rows
// (GET /datasets/{id}/deleted-rows).
func (c *Client) ListDeletedRows(accessToken string, datasetID int, f DeletedRowsFilter) (*DeletedRowList, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListDeletedRowsViewParams{
		DataSourceModelID: positiveOrNil(f.CloudSourceID),
		StoredItemID:      positiveOrNil(f.FileID),
		RowDeleteID:       positiveOrNil(f.DeleteID),
		Page:              positiveOrNil(f.Page),
		Limit:             positiveOrNil(f.Limit),
	}
	resp, err := apiClient.API.ListDeletedRowsViewWithResponse(context.Background(), c.organizationID, datasetID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list deleted rows failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list deleted rows failed")
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
