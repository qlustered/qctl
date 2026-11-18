package stored_items

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// ReplacedFile is the loaded file an uploaded file would replace in its
// period.
type ReplacedFile = api.StoredItemReplacedFileSchema

// GetReplacedFile reads the loaded file an uploaded, unpublished file would
// replace with GET /stored-items/{id}/replaced-file. It returns nil when the
// server answers null. A non-2xx answer is an *APIError carrying the
// response's error_code.
func (c *Client) GetReplacedFile(accessToken string, storedItemID int) (*ReplacedFile, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetStoredItemReplacedFileWithResponse(
		context.Background(), c.organizationID, storedItemID, &api.GetStoredItemReplacedFileParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if bytes.Equal(bytes.TrimSpace(resp.Body), []byte("null")) {
		return nil, nil
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
