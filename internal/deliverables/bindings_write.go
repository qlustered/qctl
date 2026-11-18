package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

const (
	createSheetBindingFailed  = "create deliverable sheet binding failed"
	patchSheetBindingFailed   = "patch deliverable sheet binding failed"
	deleteSheetBindingFailed  = "delete deliverable sheet binding failed"
	createColumnBindingFailed = "create deliverable column binding failed"
	patchColumnBindingFailed  = "patch deliverable column binding failed"
	deleteColumnBindingFailed = "delete deliverable column binding failed"
)

// CreateSheetBinding POSTs body as raw JSON to
// /deliverable-revisions/{revisionID}/sheet-bindings. A 422 that lists
// issues is a *RevisionInvalidError.
func (c *Client) CreateSheetBinding(token string, revisionID uuid.UUID, body map[string]any) (*api.SheetBindingResponse, error) {
	data, err := bindingBody(body)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CreateDeliverableSheetBindingWithBodyWithResponse(context.Background(), c.orgID, revisionID,
		&api.CreateDeliverableSheetBindingParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, bindingInvalidError(wrapAPIError(apiClient, err, createSheetBindingFailed, "create_deliverable_sheet_binding"), createSheetBindingFailed)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, bindingHTTPError(resp.StatusCode(), resp.Body, createSheetBindingFailed, "create_deliverable_sheet_binding")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty create deliverable sheet binding response")
	}
	return decodeBinding[api.SheetBindingResponse](resp.Body)
}

// PatchSheetBinding sends body as raw JSON to PATCH
// /deliverable-revisions/{revisionID}/sheet-bindings/{sheetBindingID}. A 422
// that lists issues is a *RevisionInvalidError.
func (c *Client) PatchSheetBinding(token string, revisionID, sheetBindingID uuid.UUID, body map[string]any) (*api.SheetBindingPatchResponse, error) {
	data, err := bindingBody(body)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PatchDeliverableSheetBindingWithBodyWithResponse(context.Background(), c.orgID, revisionID, sheetBindingID,
		&api.PatchDeliverableSheetBindingParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, bindingInvalidError(wrapAPIError(apiClient, err, patchSheetBindingFailed, "patch_deliverable_sheet_binding"), patchSheetBindingFailed)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, bindingHTTPError(resp.StatusCode(), resp.Body, patchSheetBindingFailed, "patch_deliverable_sheet_binding")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty patch deliverable sheet binding response")
	}
	return decodeBinding[api.SheetBindingPatchResponse](resp.Body)
}

// DeleteSheetBinding deletes sheet binding sheetBindingID of revision revisionID.
func (c *Client) DeleteSheetBinding(token string, revisionID, sheetBindingID uuid.UUID) error {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteDeliverableSheetBindingWithResponse(context.Background(), c.orgID, revisionID, sheetBindingID,
		&api.DeleteDeliverableSheetBindingParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, deleteSheetBindingFailed, "delete_deliverable_sheet_binding")
	}
	if resp.StatusCode() != http.StatusNoContent {
		return handleError(resp.StatusCode(), resp.Body, deleteSheetBindingFailed, "delete_deliverable_sheet_binding")
	}
	return nil
}

// CreateColumnBinding POSTs body as raw JSON to
// /deliverable-sheet-bindings/{sheetBindingID}/column-bindings. A 422
// that lists issues is a *RevisionInvalidError.
func (c *Client) CreateColumnBinding(token string, sheetBindingID uuid.UUID, body map[string]any) (*api.ColumnBindingResponse, error) {
	data, err := bindingBody(body)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CreateDeliverableColumnBindingWithBodyWithResponse(context.Background(), c.orgID, sheetBindingID,
		&api.CreateDeliverableColumnBindingParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, bindingInvalidError(wrapAPIError(apiClient, err, createColumnBindingFailed, "create_deliverable_column_binding"), createColumnBindingFailed)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, bindingHTTPError(resp.StatusCode(), resp.Body, createColumnBindingFailed, "create_deliverable_column_binding")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty create deliverable column binding response")
	}
	return decodeBinding[api.ColumnBindingResponse](resp.Body)
}

// PatchColumnBinding sends body as raw JSON to PATCH
// /deliverable-sheet-bindings/{sheetBindingID}/column-bindings/{columnBindingID}.
// A 422 that lists issues is a *RevisionInvalidError.
func (c *Client) PatchColumnBinding(token string, sheetBindingID, columnBindingID uuid.UUID, body map[string]any) (*api.ColumnBindingResponse, error) {
	data, err := bindingBody(body)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PatchDeliverableColumnBindingWithBodyWithResponse(context.Background(), c.orgID, sheetBindingID, columnBindingID,
		&api.PatchDeliverableColumnBindingParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, bindingInvalidError(wrapAPIError(apiClient, err, patchColumnBindingFailed, "patch_deliverable_column_binding"), patchColumnBindingFailed)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, bindingHTTPError(resp.StatusCode(), resp.Body, patchColumnBindingFailed, "patch_deliverable_column_binding")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty patch deliverable column binding response")
	}
	return decodeBinding[api.ColumnBindingResponse](resp.Body)
}

// DeleteColumnBinding deletes column binding columnBindingID of sheet binding
// sheetBindingID.
func (c *Client) DeleteColumnBinding(token string, sheetBindingID, columnBindingID uuid.UUID) error {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteDeliverableColumnBindingWithResponse(context.Background(), c.orgID, sheetBindingID, columnBindingID,
		&api.DeleteDeliverableColumnBindingParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, deleteColumnBindingFailed, "delete_deliverable_column_binding")
	}
	if resp.StatusCode() != http.StatusNoContent {
		return handleError(resp.StatusCode(), resp.Body, deleteColumnBindingFailed, "delete_deliverable_column_binding")
	}
	return nil
}

// bindingBody encodes body as JSON.
func bindingBody(body map[string]any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode deliverable binding request: %w", err)
	}
	return data, nil
}

// bindingInvalidError returns err as a *RevisionInvalidError whose first line
// starts with failed when its *APIError is a 422 BINDING_INVALID whose context
// lists issues, and unchanged otherwise.
func bindingInvalidError(err error, failed string) error {
	return issueListError(err, "BINDING_INVALID", failed)
}

// bindingHTTPError is handleError's error for a binding write that answered
// status with body, made a *RevisionInvalidError whose first line starts with
// failed when body is a 422 whose detail lists {loc, msg} issues, and
// bindingInvalidError's otherwise.
func bindingHTTPError(status int, body []byte, failed, operationID string) error {
	err := handleError(status, body, failed, operationID)
	if status == http.StatusUnprocessableEntity {
		if issues := detailIssues(body); len(issues) > 0 {
			return &RevisionInvalidError{Issues: issues, failed: failed, err: err}
		}
	}
	return bindingInvalidError(err, failed)
}

// detailIssues returns body's detail list as issues, each loc element as
// text, and nil when detail is not a list.
func detailIssues(body []byte) []Issue {
	var envelope struct {
		Detail []struct {
			Loc []any  `json:"loc"`
			Msg string `json:"msg"`
		} `json:"detail"`
	}
	if jsonnum.Unmarshal(body, &envelope) != nil {
		return nil
	}
	issues := make([]Issue, 0, len(envelope.Detail))
	for _, detail := range envelope.Detail {
		loc := make([]string, len(detail.Loc))
		for i, part := range detail.Loc {
			loc[i] = fmt.Sprint(part)
		}
		issues = append(issues, Issue{Loc: &loc, Msg: detail.Msg})
	}
	return issues
}
