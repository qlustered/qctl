package table_series

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// SetLocked changes a member's period lock.
func (c *Client) SetLocked(token string, seriesID uuid.UUID, datasetID int, locked bool) (*Member, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(map[string]bool{"is_locked": locked})
	if err != nil {
		return nil, fmt.Errorf("encode period lock: %w", err)
	}
	resp, err := apiClient.API.PatchTableSeriesMemberWithBodyWithResponse(context.Background(), c.orgID,
		seriesID, datasetID, &api.PatchTableSeriesMemberParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "change period lock failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "change period lock failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty period lock response")
	}
	return resp.JSON200, nil
}
