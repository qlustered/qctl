package duplicate_groups //nolint:revive // Matches the backend resource name.

import (
	"context"
	"fmt"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/comments"
)

// Type aliases for marking rows as a person_marked group and retracting it.
type (
	MarkRequest   = api.DuplicateGroupMarkRequestSchema
	MarkResult    = api.DuplicateGroupMarkResponseSchema
	MarkedMember  = api.DuplicateGroupMarkedMemberSchema
	RetractResult = api.DuplicateGroupRetractResponseSchema
)

// The hold modes of a person_marked group, and the bounds of a mark's rows.
const (
	HoldModeHold HoldMode = "hold"
	HoldModeFlag HoldMode = "flag"

	MinMarkRows = 2
	MaxMarkRows = 500
)

// Mark marks the table's rows as one person_marked group.
func (c *Client) Mark(accessToken string, tableID int, body MarkRequest) (*MarkResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "mark duplicate group failed"
	resp, err := apiClient.API.MarkDuplicateGroupWithResponse(
		context.Background(), c.organizationID, tableID, &api.MarkDuplicateGroupParams{}, body)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusCreated {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("%s: empty response", msg)
	}
	return resp.JSON201, nil
}

// Retract ends a person_marked group as retracted; comment may be nil.
func (c *Client) Retract(accessToken string, tableID int, groupID openapi_types.UUID, comment *string) (*RetractResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "retract duplicate group failed"
	resp, err := apiClient.API.RetractDuplicateGroupWithResponse(
		context.Background(), c.organizationID, tableID, groupID, &api.RetractDuplicateGroupParams{},
		api.DuplicateGroupRetractRequestSchema{Comment: comment})
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: empty response", msg)
	}
	return resp.JSON200, nil
}
