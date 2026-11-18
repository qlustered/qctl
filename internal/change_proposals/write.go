package change_proposals //nolint:revive // Established package-path style in this repo (rule_reviews, privilege_requests).

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

// Wire bodies of the write routes.
type (
	CreateRequest = api.ChangeProposalCreateSchema
	EditRequest   = api.ChangeProposalEditSchema
)

// Create stores a new proposal: a draft, or a pending_review one when
// body.Submit is true.
func (c *Client) Create(ctx context.Context, accessToken string, body CreateRequest) (*Proposal, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "create change proposal failed"
	resp, err := apiClient.API.CreateChangeProposalWithResponse(ctx, c.organizationID, &api.CreateChangeProposalParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, msg)
	}
	return decodeProposal(resp.StatusCode(), http.StatusCreated, resp.Body, msg)
}

// EditClears names the edit keys a nil value clears instead of keeping.
type EditClears struct {
	DependsOn bool
	Note      bool
}

// Edit writes a new version of a proposal. A nil Payload keeps the stored
// one; a nil VersionID skips the server's
// version check; a nil addressed list keeps the stored one. A nil
// DependsOnID or Note keeps the stored value, or clears it when clears names
// it.
func (c *Client) Edit(ctx context.Context, accessToken string, id uuid.UUID, body EditRequest, clears EditClears) (*Proposal, error) {
	const msg = "edit change proposal failed"
	data, err := editBody(body, clears)
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", msg, err)
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.EditChangeProposalWithBodyWithResponse(ctx, c.organizationID, id,
		&api.EditChangeProposalParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, msg)
	}
	return decodeProposal(resp.StatusCode(), http.StatusOK, resp.Body, msg)
}

// editBody is body as JSON, without payload when it is nil, and without
// depends_on_id or note when that value is nil and clears does not name it:
// the server keeps a key that is absent and clears one that is null.
func editBody(body EditRequest, clears EditClears) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if body.Payload == nil {
		delete(fields, "payload")
	}
	if body.DependsOnID == nil && !clears.DependsOn {
		delete(fields, "depends_on_id")
	}
	if body.Note == nil && !clears.Note {
		delete(fields, "note")
	}
	return json.Marshal(fields)
}

// Submit sends a draft or changes_requested proposal to review.
func (c *Client) Submit(ctx context.Context, accessToken string, id uuid.UUID) (*Proposal, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "submit change proposal failed"
	resp, err := apiClient.API.SubmitChangeProposalWithResponse(ctx, c.organizationID, id, &api.SubmitChangeProposalParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, msg)
	}
	return decodeProposal(resp.StatusCode(), http.StatusOK, resp.Body, msg)
}

// Withdraw withdraws a live proposal.
func (c *Client) Withdraw(ctx context.Context, accessToken string, id uuid.UUID) (*Proposal, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "withdraw change proposal failed"
	resp, err := apiClient.API.WithdrawChangeProposalWithResponse(ctx, c.organizationID, id, &api.WithdrawChangeProposalParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, msg)
	}
	return decodeProposal(resp.StatusCode(), http.StatusOK, resp.Body, msg)
}

// RerunDryRun stages a new validation dry run of a pending_review or
// changes_requested rule-param proposal.
func (c *Client) RerunDryRun(ctx context.Context, accessToken string, id uuid.UUID) (*Proposal, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "re-run change proposal dry run failed"
	resp, err := apiClient.API.RerunChangeProposalDryRunWithResponse(ctx, c.organizationID, id, &api.RerunChangeProposalDryRunParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, msg)
	}
	return decodeProposal(resp.StatusCode(), http.StatusOK, resp.Body, msg)
}

// decodeProposal decodes a proposal response body when status is want, and
// builds the *APIError otherwise.
func decodeProposal(status, want int, body []byte, msg string) (*Proposal, error) {
	if status != want {
		return nil, handleError(status, body, msg)
	}
	var proposal Proposal
	if err := jsonnum.Unmarshal(body, &proposal); err != nil {
		return nil, fmt.Errorf("%s: decode change proposal: %w", msg, err)
	}
	return &proposal, nil
}

// Wire bodies of the decision routes.
type (
	ApproveRequest  = api.ChangeProposalApproveSchema
	DecisionRequest = api.ChangeProposalDecisionSchema
)

// Approve approves a pending_review proposal at body's version and impact
// digest, which runs its operation as the caller.
func (c *Client) Approve(ctx context.Context, accessToken string, id uuid.UUID, body ApproveRequest) (*Proposal, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "approve change proposal failed"
	resp, err := apiClient.API.ApproveChangeProposalWithResponse(ctx, c.organizationID, id, &api.ApproveChangeProposalParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, msg)
	}
	return decodeProposal(resp.StatusCode(), http.StatusOK, resp.Body, msg)
}

// Reject rejects a proposal with reason.
func (c *Client) Reject(ctx context.Context, accessToken string, id uuid.UUID, reason string) (*Proposal, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "reject change proposal failed"
	resp, err := apiClient.API.RejectChangeProposalWithResponse(ctx, c.organizationID, id,
		&api.RejectChangeProposalParams{}, DecisionRequest{Reason: reason})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, msg)
	}
	return decodeProposal(resp.StatusCode(), http.StatusOK, resp.Body, msg)
}

// RequestChanges sends a proposal back to its authors with reason.
func (c *Client) RequestChanges(ctx context.Context, accessToken string, id uuid.UUID, reason string) (*Proposal, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	const msg = "request changes on change proposal failed"
	resp, err := apiClient.API.RequestChangeProposalChangesWithResponse(ctx, c.organizationID, id,
		&api.RequestChangeProposalChangesParams{}, DecisionRequest{Reason: reason})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, msg)
	}
	return decodeProposal(resp.StatusCode(), http.StatusOK, resp.Body, msg)
}
