package duplicate_groups //nolint:revive // Matches the backend resource name.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Type aliases to the generated source-view (sender) schemas.
type (
	SourceGroup    = api.SourceDuplicateGroupDetailSchema
	SourceSummary  = api.SourceDuplicateGroupSchema
	SourcePage     = api.SourceDuplicateGroupListSchema
	SourceMember   = api.SourceDuplicateMemberSchema
	SourceDecision = api.SourceDuplicateDecisionSchema
	SourceResponse = api.SourceDuplicateResponseSchema
	SourceCaller   = api.SourceDuplicateCallerSchema
	SenderState    = api.SenderState
	HeldReason     = api.HeldReason
	SenderOutcome  = api.SenderOutcome
	RespondRefusal = api.RespondRefusal
	ResponseKind   = api.DuplicateGroupResponseKind
)

// The source view's wire values.
const (
	ResponseSeparateRows         ResponseKind = "separate_rows"
	ResponseCorrectedFileFollows ResponseKind = "corrected_file_follows"

	SenderHeld    SenderState = "held"
	SenderDecided SenderState = "decided"
	SenderLoaded  SenderState = "loaded"
	SenderEnded   SenderState = "ended"
)

// ParseResponseID parses a full response UUID; anything else exits 2.
func ParseResponseID(id string) (openapi_types.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return openapi_types.UUID{}, qerrors.NewBadUsagef("invalid duplicate group response id %q: expected a full UUID", id)
	}
	return parsed, nil
}

// SourceListOptions filters a page of the groups on the caller's cloud
// sources; nil and zero fields are not sent.
type SourceListOptions struct {
	CloudSourceID *int
	StoredItemID  *int
	IncludeEnded  bool
	Page          int
	Limit         int
}

// ListSource reads one page of the table's groups with a copy on the
// caller's cloud sources.
func (c *Client) ListSource(accessToken string, tableID int, opts SourceListOptions) (*SourcePage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.GetSourceDuplicateGroupsParams{DataSourceModelID: opts.CloudSourceID, StoredItemID: opts.StoredItemID}
	if opts.IncludeEnded {
		params.IncludeEnded = &opts.IncludeEnded
	}
	if opts.Page > 0 {
		params.Page = &opts.Page
	}
	if opts.Limit > 0 {
		params.Limit = &opts.Limit
	}
	const msg = "list source duplicate groups failed"
	resp, err := apiClient.API.GetSourceDuplicateGroupsWithResponse(context.Background(), c.organizationID, tableID, params)
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

// GetSource reads one group as the caller's cloud sources see it, with their
// ended responses.
func (c *Client) GetSource(accessToken string, tableID int, groupID openapi_types.UUID) (*SourceGroup, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "get source duplicate group failed"
	resp, err := apiClient.API.GetSourceDuplicateGroupWithResponse(
		context.Background(), c.organizationID, tableID, groupID, &api.GetSourceDuplicateGroupParams{})
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

// Respond records a response on a group for one of the caller's cloud
// sources; cloudSourceID may be nil. The comment is sent as given.
func (c *Client) Respond(accessToken string, tableID int, groupID openapi_types.UUID, kind ResponseKind,
	comment string, cloudSourceID *int) (*SourceResponse, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "respond on duplicate group failed"
	body := api.SourceDuplicateResponseRequestSchema{Kind: kind, Comment: comment, DataSourceModelID: cloudSourceID}
	resp, err := apiClient.API.PostSourceDuplicateGroupResponseWithResponse(
		context.Background(), c.organizationID, tableID, groupID, &api.PostSourceDuplicateGroupResponseParams{}, body)
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

// WithdrawResponse withdraws a live response of one of the caller's cloud
// sources on a group.
func (c *Client) WithdrawResponse(accessToken string, tableID int, groupID, responseID openapi_types.UUID) (*SourceResponse, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "withdraw duplicate group response failed"
	resp, err := apiClient.API.WithdrawSourceDuplicateGroupResponseWithResponse(
		context.Background(), c.organizationID, tableID, groupID, responseID, &api.WithdrawSourceDuplicateGroupResponseParams{})
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
