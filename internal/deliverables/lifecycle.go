package deliverables

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// Archive moves a deliverable to the trash.
func (c *Client) Archive(token string, id uuid.UUID) (*Deliverable, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ArchiveDeliverableWithResponse(context.Background(), c.orgID, id, &api.ArchiveDeliverableParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "archive deliverable failed", "archive_deliverable")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "archive deliverable failed", "archive_deliverable")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty archive deliverable response")
	}
	return resp.JSON200, nil
}

// Recover restores a deliverable from the trash.
func (c *Client) Recover(token string, id uuid.UUID) (*Deliverable, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.RecoverDeliverableWithResponse(context.Background(), c.orgID, id, &api.RecoverDeliverableParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "recover deliverable failed", "recover_deliverable")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "recover deliverable failed", "recover_deliverable")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty recover deliverable response")
	}
	return resp.JSON200, nil
}

// DeleteRevision permanently deletes a draft revision.
func (c *Client) DeleteRevision(token string, id uuid.UUID) error {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteDeliverableRevisionWithResponse(context.Background(), c.orgID, id, &api.DeleteDeliverableRevisionParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, "delete deliverable revision failed", "delete_deliverable_revision")
	}
	if resp.StatusCode() != http.StatusNoContent {
		return handleError(resp.StatusCode(), resp.Body, "delete deliverable revision failed", "delete_deliverable_revision")
	}
	return nil
}
