// Package privilege_requests is the domain client for the break-glass ops
// privilege workflow: a request to flip another user's is_ops flag, the
// two-person approval (or rejection) that applies it, and the immutable audit
// ledger of every change that was applied.
//
// It wraps the six /api/ops/{org}/privilege-* routes and nothing else (atlas
// capi/capi/views/privilege_ticketing_view.py). Things a caller cannot see
// from the method signatures:
//
//   - The routes are NOT org-filtered. The listing and the audit are
//     platform-wide whichever organization id is in the path; the org only
//     decides where the bearer token is bound and the permission checked.
//   - Create can come back already "Approved": when the requestor is the sole
//     eligible ops user the backend applies the change inline. Read the
//     returned status; never assume "Pending".
//   - Approve and Reject answer an unknown id with 400, not 404. Only Get
//     gives a missing request exit 3, which is why the commands fetch first.
//   - Reject sends rejection_reason as null and takes no reason parameter:
//     the controller accepts the field and discards it.
//   - Expiry is lazy and server-side. A stale request lists as "Pending"
//     until someone tries to approve it; nothing here computes a TTL.
package privilege_requests //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, value_mapping).

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// Type aliases to the generated schemas (the frozen cross-ticket contract).
type (
	Request         = api.PrivilegeRequestResponseSchema // create; embedded in approve / reject results
	ListItem        = api.PrivilegeRequestListItemSchema // carries the three people as emails
	ListPage        = api.PrivilegeRequestListSchema     // {Requests, Total, Page, Limit}
	Detail          = api.PrivilegeRequestDetailSchema   // embeds the people and the applied audit rows
	AuditEntry      = api.PrivilegeAuditEntrySchema      // people as UUIDs only
	AuditPage       = api.PrivilegeAuditListSchema       // {AuditEntries, Total, Page, Limit}
	ApprovalResult  = api.ApprovalResponseSchema         // {Request, TargetUser, Message}
	RejectionResult = api.RejectionResponseSchema        // {Request, Message}
	UserInfo        = api.UserInfoForPrivilegeSchema
)

// Client handles HTTP requests for the privilege workflow.
//
// It carries no deadline of its own: client.New resolves the zero Timeout to
// --request-timeout, or to client.DefaultTimeout when that flag is unset.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
}

// NewClient creates a privilege requests client (standard pattern).
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

// pageParams returns the pointers a generated params struct wants, leaving a
// non-positive value unset so the server default applies. Callers validate
// positivity before calling; this only decides what reaches the wire.
func pageParams(page, limit int) (pagePtr, limitPtr *int) {
	if page > 0 {
		pagePtr = &page
	}
	if limit > 0 {
		limitPtr = &limit
	}
	return pagePtr, limitPtr
}

// Create files a request to escalate or demote target. The backend answers
// 201 — with status "Approved" rather than "Pending" when it applied the
// change inline (see the package comment).
func (c *Client) Create(accessToken string, target openapi_types.UUID, action api.PrivilegeAction, justification string) (*Request, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.CreatePrivilegeRequestSchema{
		Action:        action,
		TargetUserID:  target,
		Justification: justification,
	}
	resp, err := apiClient.API.CreatePrivilegeRequestWithResponse(
		context.Background(), c.organizationID, &api.CreatePrivilegeRequestParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "create privilege request failed")
	}
	if resp.StatusCode() != http.StatusCreated {
		return nil, handleError(resp.StatusCode(), resp.Body, "create privilege request failed")
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("create privilege request failed: unexpected empty response")
	}
	return resp.JSON201, nil
}

// ListOptions filters the request listing. A nil filter is not sent; Page and
// Limit are sent only when positive.
type ListOptions struct {
	Status       *api.PrivilegeRequestStatus
	TargetUserID *openapi_types.UUID
	Page, Limit  int
}

// List returns one page of requests, newest first, across the whole platform.
func (c *Client) List(accessToken string, opts ListOptions) (*ListPage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListPrivilegeRequestsParams{
		Status:       opts.Status,
		TargetUserID: opts.TargetUserID,
	}
	params.Page, params.Limit = pageParams(opts.Page, opts.Limit)
	resp, err := apiClient.API.ListPrivilegeRequestsWithResponse(context.Background(), c.organizationID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list privilege requests failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list privilege requests failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("list privilege requests failed: unexpected empty response")
	}
	return resp.JSON200, nil
}

// Get fetches one request with its people and applied audit rows. It is the
// only route here that answers a missing request with 404.
func (c *Client) Get(accessToken string, id openapi_types.UUID) (*Detail, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetPrivilegeRequestWithResponse(
		context.Background(), c.organizationID, id, &api.GetPrivilegeRequestParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get privilege request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get privilege request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("get privilege request failed: unexpected empty response")
	}
	return resp.JSON200, nil
}

// Approve applies a pending request under the two-person rule. The route
// takes no body.
func (c *Client) Approve(accessToken string, id openapi_types.UUID) (*ApprovalResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ApprovePrivilegeRequestWithResponse(
		context.Background(), c.organizationID, id, &api.ApprovePrivilegeRequestParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "approve privilege request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "approve privilege request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("approve privilege request failed: unexpected empty response")
	}
	return resp.JSON200, nil
}

// Reject declines a pending request. There is deliberately no reason
// parameter: the body carries rejection_reason null, because the controller
// discards whatever it is given.
func (c *Client) Reject(accessToken string, id openapi_types.UUID) (*RejectionResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.RejectPrivilegeRequestWithResponse(
		context.Background(), c.organizationID, id, &api.RejectPrivilegeRequestParams{},
		api.RejectPrivilegeRequestSchema{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "reject privilege request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "reject privilege request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("reject privilege request failed: unexpected empty response")
	}
	return resp.JSON200, nil
}

// AuditOptions filters the audit ledger. A nil filter is not sent; Page and
// Limit are sent only when positive.
type AuditOptions struct {
	TargetUserID *openapi_types.UUID
	RequestID    *openapi_types.UUID
	Page, Limit  int
}

// Audit returns one page of the ledger, newest first: one row per APPLIED
// change. Rejections and expiries leave no row.
func (c *Client) Audit(accessToken string, opts AuditOptions) (*AuditPage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListPrivilegeAuditParams{
		TargetUserID: opts.TargetUserID,
		RequestID:    opts.RequestID,
	}
	params.Page, params.Limit = pageParams(opts.Page, opts.Limit)
	resp, err := apiClient.API.ListPrivilegeAuditWithResponse(context.Background(), c.organizationID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list privilege audit failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list privilege audit failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("list privilege audit failed: unexpected empty response")
	}
	return resp.JSON200, nil
}
