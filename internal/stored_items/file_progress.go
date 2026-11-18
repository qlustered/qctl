package stored_items

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// FileProgress is one file with each step of its way to its table, what stops
// it and how it ended.
type FileProgress = api.FileProgressSchema

// GetFileProgress reads a file's progress with GET /stored-items/{id}/progress.
// A non-2xx answer is an *APIError carrying the response's error_code.
func (c *Client) GetFileProgress(accessToken string, storedItemID int) (*FileProgress, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetStoredItemProgressWithResponse(
		context.Background(), c.organizationID, storedItemID, &api.GetStoredItemProgressParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// FileQuarantine splits a file's undeleted quarantine rows between those
// waiting for approval and those to fix.
type FileQuarantine struct {
	WaitingForApprovalCount *int `yaml:"waiting_for_approval_count" json:"waiting_for_approval_count"`
	RowsToFixCount          *int `yaml:"rows_to_fix_count" json:"rows_to_fix_count"`
}

// NewFileQuarantine is the quarantine split of p; nil for a nil p.
func NewFileQuarantine(p *FileProgress) *FileQuarantine {
	if p == nil {
		return nil
	}
	return &FileQuarantine{WaitingForApprovalCount: p.WaitingForApprovalCount, RowsToFixCount: p.RowsToFixCount}
}
