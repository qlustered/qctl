// Package duplicate_groups is the domain client for a mirror or append
// table's duplicate groups: rows held or flagged together until a reviewed
// decision.
package duplicate_groups //nolint:revive // Matches the backend resource name.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Type aliases to the generated schemas.
type (
	Group             = api.DuplicateGroupDetailSchema
	Summary           = api.DuplicateGroupSchema
	Page              = api.DuplicateGroupListSchema
	StateFilter       = api.StateFilter
	ResponseFilter    = api.SenderResponseFilter
	SenderResponse    = api.DuplicateSenderResponseSchema
	DifferingCell     = api.DuplicateDifferingCellSchema
	Decision          = api.DuplicateDecisionSchema
	DecisionRequest   = api.DuplicateDecisionRequestSchema
	DecisionResult    = api.DuplicateDecisionResponseSchema
	ApplyResult       = api.DuplicateApplyResultSchema
	PeriodResult      = api.DuplicatePeriodResultSchema
	AllowAllResult    = api.DuplicateAllowAllResponseSchema
	CommentResult     = api.DuplicateGroupCommentResponseSchema
	ShareResult       = api.DuplicateGroupShareResponseSchema
	DecisionKind      = api.DuplicateDecisionKind
	Origin            = api.DuplicateGroupOrigin
	DecisionState     = api.DuplicateDecisionState
	DecisionScope     = api.DuplicateDecisionScope
	AllowAllSkip      = api.DuplicateAllowAllSkipSchema
	AllowAllPending   = api.DuplicateAllowAllPendingSchema
	GroupMember       = api.DuplicateGroupMemberSchema
	GroupState        = api.GroupState
	PeriodOutcome     = api.PeriodOutcome
	CallerPermissions = api.DuplicateGroupCallerSchema
	DecideRefusal     = api.DecideRefusal
	RetractRefusal    = api.RetractRefusal
	HoldMode          = api.DuplicateGroupHoldMode
)

// APIError is the shared error carrying the backend's error_code and context.
type APIError = comments.APIError

// The wire values, spelled here because the generated constants' names move
// with oapi-codegen's collision prefixes.
const (
	KindAllow         DecisionKind = "allow"
	KindKeepOne       DecisionKind = "keep_one"
	KindNotDuplicates DecisionKind = "not_duplicates"
	KindStopSeries    DecisionKind = "stop_series"

	OriginRepeatedKey   Origin = "repeated_key"
	OriginIdenticalRows Origin = "identical_rows"
	OriginPersonMarked  Origin = "person_marked"

	StatePending   DecisionState = "pending"
	StateEffective DecisionState = "effective"
	StateEnacted   DecisionState = "enacted"
	StateDismissed DecisionState = "dismissed"

	ScopeSeries DecisionScope = "series"

	GroupStateEnded GroupState = "ended"

	// MaxLimit is the list route's largest page.
	MaxLimit = 100
)

// Client handles HTTP requests for duplicate groups.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
}

// NewClient creates a duplicate groups client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{baseURL: baseURL, organizationID: orgUUID, verbosity: verbosity}
}

func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	return apiClient, nil
}

// ParseGroupID parses a full group UUID; anything else exits 2.
func ParseGroupID(id string) (openapi_types.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return openapi_types.UUID{}, qerrors.NewBadUsagef("invalid duplicate group id %q: expected a full UUID", id)
	}
	return parsed, nil
}

// ListOptions filters a page of a table's groups; nil and zero fields are not
// sent.
type ListOptions struct {
	Origin         *Origin
	State          *StateFilter
	StoredItemID   *int
	CloudSourceID  *int
	SenderResponse *ResponseFilter
	IncludeEnded   bool
	Page           int
	Limit          int
}

// List reads one page of the table's groups.
func (c *Client) List(accessToken string, tableID int, opts ListOptions) (*Page, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.GetDuplicateGroupsParams{
		Origin: opts.Origin, State: opts.State, StoredItemID: opts.StoredItemID,
		DataSourceModelID: opts.CloudSourceID, SenderResponse: opts.SenderResponse,
	}
	if opts.IncludeEnded {
		params.IncludeEnded = &opts.IncludeEnded
	}
	if opts.Page > 0 {
		params.Page = &opts.Page
	}
	if opts.Limit > 0 {
		params.Limit = &opts.Limit
	}
	const msg = "list duplicate groups failed"
	resp, err := apiClient.API.GetDuplicateGroupsWithResponse(context.Background(), c.organizationID, tableID, params)
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

// FindByRow reads every page of the table's groups matching opts at
// MaxLimit and returns those with a listed member on rowID, and the number
// of pages read. opts.Page and opts.Limit are ignored.
func (c *Client) FindByRow(accessToken string, tableID int, opts ListOptions, rowID int) ([]Summary, int, error) {
	var found []Summary
	opts.Limit = MaxLimit
	for page := 1; ; page++ {
		opts.Page = page
		list, err := c.List(accessToken, tableID, opts)
		if err != nil {
			return nil, page, err
		}
		for i := range list.Results {
			if HasRow(&list.Results[i], rowID) {
				found = append(found, list.Results[i])
			}
		}
		if list.Next == nil || len(list.Results) == 0 {
			return found, page, nil
		}
	}
}

// HasRow reports whether one of g's listed members is on rowID.
func HasRow(g *Summary, rowID int) bool {
	for _, m := range g.Members {
		if m.RowID == rowID {
			return true
		}
	}
	return false
}

// Get reads one group with its earlier decisions and ended responses.
func (c *Client) Get(accessToken string, tableID int, groupID openapi_types.UUID) (*Group, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "get duplicate group failed"
	resp, err := apiClient.API.GetDuplicateGroupWithResponse(
		context.Background(), c.organizationID, tableID, groupID, &api.GetDuplicateGroupParams{})
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

// Decide proposes or approves a decision on a group and applies it.
func (c *Client) Decide(accessToken string, tableID int, groupID openapi_types.UUID, body DecisionRequest) (*DecisionResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "decide duplicate group failed"
	resp, err := apiClient.API.DecideDuplicateGroupWithResponse(
		context.Background(), c.organizationID, tableID, groupID, &api.DecideDuplicateGroupParams{}, body)
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

// Withdraw withdraws the group's pending decision decisionID and applies the
// withdrawal.
func (c *Client) Withdraw(accessToken string, tableID int, groupID, decisionID openapi_types.UUID, comment *string) (*DecisionResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "withdraw duplicate group decision failed"
	body := api.DuplicateDecisionWithdrawRequestSchema{DecisionID: decisionID, Comment: comment}
	resp, err := apiClient.API.WithdrawDuplicateGroupDecisionWithResponse(
		context.Background(), c.organizationID, tableID, groupID, &api.WithdrawDuplicateGroupDecisionParams{}, body)
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

// Approve approves the group's pending decision decisionID, replacing the
// caller's earlier vote, and applies it.
func (c *Client) Approve(accessToken string, tableID int, groupID, decisionID openapi_types.UUID, comment *string) (*DecisionResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "approve duplicate group decision failed"
	body := api.DuplicateDecisionApproveRequestSchema{Comment: comment}
	resp, err := apiClient.API.ApproveDuplicateGroupDecisionWithResponse(
		context.Background(), c.organizationID, tableID, groupID, decisionID, &api.ApproveDuplicateGroupDecisionParams{}, body)
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

// Disagree records the caller's disagreement with the group's pending
// decision decisionID, replacing their earlier vote.
func (c *Client) Disagree(accessToken string, tableID int, groupID, decisionID openapi_types.UUID, reason string) (*DecisionResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "disagree with duplicate group decision failed"
	body := api.DuplicateDecisionDisagreeRequestSchema{Reason: reason}
	resp, err := apiClient.API.DisagreeDuplicateGroupDecisionWithResponse(
		context.Background(), c.organizationID, tableID, groupID, decisionID, &api.DisagreeDuplicateGroupDecisionParams{}, body)
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

// Dismiss dismisses the group's pending decision decisionID and applies the
// dismissal.
func (c *Client) Dismiss(accessToken string, tableID int, groupID, decisionID openapi_types.UUID, reason string) (*DecisionResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "dismiss duplicate group decision failed"
	body := api.DuplicateDecisionDismissRequestSchema{Reason: reason}
	resp, err := apiClient.API.DismissDuplicateGroupDecisionWithResponse(
		context.Background(), c.organizationID, tableID, groupID, decisionID, &api.DismissDuplicateGroupDecisionParams{}, body)
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

// StopSeries proposes, on a group, to stop the series allow decisionID in
// every period of its series, and applies the proposal.
func (c *Client) StopSeries(accessToken string, tableID int, groupID, decisionID openapi_types.UUID, comment *string) (*DecisionResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "stop duplicate group series allow failed"
	body := api.DuplicateStopSeriesRequestSchema{DecisionID: decisionID, Comment: comment}
	resp, err := apiClient.API.StopDuplicateGroupSeriesWithResponse(
		context.Background(), c.organizationID, tableID, groupID, &api.StopDuplicateGroupSeriesParams{}, body)
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

// AllowAll proposes or approves an allow on each repeated_key group of an
// upload that holds a copy, and applies them.
func (c *Client) AllowAll(accessToken string, tableID, storedItemID int, comment *string) (*AllowAllResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "allow-all duplicate groups failed"
	body := api.DuplicateAllowAllRequestSchema{StoredItemID: storedItemID, Comment: comment}
	resp, err := apiClient.API.AllowAllDuplicateGroupsWithResponse(
		context.Background(), c.organizationID, tableID, &api.AllowAllDuplicateGroupsParams{}, body)
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

// Reapply retries unapplied decisions of the table's groups.
func (c *Client) Reapply(accessToken string, tableID int) (*ApplyResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "re-apply duplicate groups failed"
	resp, err := apiClient.API.ApplyDuplicateGroupsWithResponse(
		context.Background(), c.organizationID, tableID, &api.ApplyDuplicateGroupsParams{})
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

// AddComment posts to a group's internal discussion; parentID may be nil.
func (c *Client) AddComment(accessToken string, tableID int, groupID openapi_types.UUID, body string, parentID *openapi_types.UUID) (*CommentResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "comment on duplicate group failed"
	resp, err := apiClient.API.AddDuplicateGroupCommentWithResponse(
		context.Background(), c.organizationID, tableID, groupID, &api.AddDuplicateGroupCommentParams{},
		api.CreateCommentSchema{Body: body, ParentID: parentID})
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

// Share shares a group's discussion with the data source its rows are on.
func (c *Client) Share(accessToken string, tableID int, groupID openapi_types.UUID, comment *string) (*ShareResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "share duplicate group discussion failed"
	resp, err := apiClient.API.ShareDuplicateGroupThreadWithResponse(
		context.Background(), c.organizationID, tableID, groupID, &api.ShareDuplicateGroupThreadParams{},
		api.DuplicateGroupShareRequestSchema{Comment: comment})
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
