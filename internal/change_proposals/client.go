// Package change_proposals is the domain client for reading change proposals
// (/api/orgs/{org}/change-proposals).
package change_proposals //nolint:revive // Established package-path style in this repo (rule_reviews, privilege_requests).

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// Type aliases to the generated schemas.
type (
	Proposal  = api.ChangeProposalSchema
	ListItem  = api.ChangeProposalListItemSchema
	ListPage  = api.ChangeProposalListSchema // {Items}; no total
	State     = api.ChangeProposalState
	Operation = api.ChangeReviewOperation
	Action    = api.ChangeProposalAction
	Review    = api.ChangeProposalReviewSchema
	Impact    = api.ChangeProposalImpactSchema
	Overlap   = api.ChangeProposalOverlapSchema
	Author    = api.ChangeProposalAuthorSchema
)

// Detail is the GET response: the proposal, its review now, the impact
// stored when it last went to review, the live proposals it overlaps, and
// the delegations behind each author.
type Detail struct {
	Proposal
	Review         *Review                 `json:"review"`
	ImpactAtSubmit *Impact                 `json:"impact_at_submit"`
	ImpactDiff     *map[string]interface{} `json:"impact_diff"`
	Overlaps       *[]Overlap              `json:"overlaps"`
	Authors        []Author                `json:"authors"`

	ApproveReapprovesSeriesRules *bool `json:"approve_reapproves_series_rules"`
}

// MaskedFields is p's masked_fields: the fields whose values its payload
// carries masked for the caller, or nil.
func MaskedFields(p *Proposal) []string {
	if p.MaskedFields == nil {
		return nil
	}
	return *p.MaskedFields
}

// The server's limit bounds for List.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Client handles HTTP requests for change proposals.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
}

// NewClient creates a change proposals client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
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

// ListParams filters the proposal listing. Empty States, nil pointers, false
// booleans and non-positive Limit/Offset are not sent.
type ListParams struct {
	States         []State
	DatasetID      *int
	Operation      *Operation
	ReviewableByMe bool
	AuthoredByMe   bool
	Limit          int
	Offset         int
}

func (p ListParams) apiParams() *api.ListChangeProposalsParams {
	out := &api.ListChangeProposalsParams{
		DatasetID: p.DatasetID,
		Operation: p.Operation,
	}
	if len(p.States) > 0 {
		states := append([]State(nil), p.States...)
		out.State = &states
	}
	if p.ReviewableByMe {
		t := true
		out.ReviewableByMe = &t
	}
	if p.AuthoredByMe {
		t := true
		out.AuthoredByMe = &t
	}
	if p.Limit > 0 {
		limit := p.Limit
		out.Limit = &limit
	}
	if p.Offset > 0 {
		offset := p.Offset
		out.Offset = &offset
	}
	return out
}

// List returns one page of the proposals the caller can see, newest first.
// Numbers in each payload and execution_result decode as jsonnum.Unmarshal
// decodes them.
func (c *Client) List(ctx context.Context, accessToken string, params ListParams) (*ListPage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ListChangeProposalsWithResponse(ctx, c.organizationID, params.apiParams())
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list change proposals failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list change proposals failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("list change proposals failed: unexpected empty response")
	}
	var page ListPage
	if err := jsonnum.Unmarshal(resp.Body, &page); err != nil {
		return nil, fmt.Errorf("decode change proposal list: %w", err)
	}
	return &page, nil
}

// Get fetches one proposal with its review. Numbers in its maps decode as
// jsonnum.Unmarshal decodes them.
func (c *Client) Get(ctx context.Context, accessToken string, id uuid.UUID) (*Detail, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetChangeProposalWithResponse(ctx, c.organizationID, id, &api.GetChangeProposalParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get change proposal failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get change proposal failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("get change proposal failed: unexpected empty response")
	}
	var detail Detail
	if err := jsonnum.Unmarshal(resp.Body, &detail); err != nil {
		return nil, fmt.Errorf("decode change proposal: %w", err)
	}
	return &detail, nil
}
