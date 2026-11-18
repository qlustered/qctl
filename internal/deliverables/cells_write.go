package deliverables

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

const (
	createCellBindingFailed = "create deliverable cell binding failed"
	patchCellBindingFailed  = "patch deliverable cell binding failed"
	deleteCellBindingFailed = "delete deliverable cell binding failed"
)

// CreateCellBinding POSTs body as raw JSON to
// /deliverable-revisions/{revisionID}/cell-bindings. A 422
// that lists issues is a *RevisionInvalidError.
func (c *Client) CreateCellBinding(token string, revisionID uuid.UUID, body map[string]any) (*api.CellBindingResponse, error) {
	data, err := bindingBody(body)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CreateDeliverableCellBindingWithBodyWithResponse(context.Background(), c.orgID, revisionID,
		&api.CreateDeliverableCellBindingParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, bindingInvalidError(wrapAPIError(apiClient, err, createCellBindingFailed, "create_deliverable_cell_binding"), createCellBindingFailed)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, bindingHTTPError(resp.StatusCode(), resp.Body, createCellBindingFailed, "create_deliverable_cell_binding")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty create deliverable cell binding response")
	}
	return resp.JSON200, nil
}

// PatchCellBinding sends body as raw JSON to PATCH
// /deliverable-revisions/{revisionID}/cell-bindings/{cellBindingID}.
// A 422 that lists issues is a *RevisionInvalidError.
func (c *Client) PatchCellBinding(token string, revisionID, cellBindingID uuid.UUID, body map[string]any) (*api.CellBindingResponse, error) {
	data, err := bindingBody(body)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PatchDeliverableCellBindingWithBodyWithResponse(context.Background(), c.orgID, revisionID, cellBindingID,
		&api.PatchDeliverableCellBindingParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, bindingInvalidError(wrapAPIError(apiClient, err, patchCellBindingFailed, "patch_deliverable_cell_binding"), patchCellBindingFailed)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, bindingHTTPError(resp.StatusCode(), resp.Body, patchCellBindingFailed, "patch_deliverable_cell_binding")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty patch deliverable cell binding response")
	}
	return resp.JSON200, nil
}

// DeleteCellBinding deletes a cell binding from revisionID.
func (c *Client) DeleteCellBinding(token string, revisionID, cellBindingID uuid.UUID) error {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteDeliverableCellBindingWithResponse(context.Background(), c.orgID, revisionID, cellBindingID,
		&api.DeleteDeliverableCellBindingParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, deleteCellBindingFailed, "delete_deliverable_cell_binding")
	}
	if resp.StatusCode() != http.StatusNoContent {
		return handleError(resp.StatusCode(), resp.Body, deleteCellBindingFailed, "delete_deliverable_cell_binding")
	}
	return nil
}
