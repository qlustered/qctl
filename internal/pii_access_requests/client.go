// Package pii_access_requests handles a viewer's requests for unmasked personal data.
package pii_access_requests //nolint:revive // Package name follows the domain.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

type (
	Request       = api.PiiAccessRequestSchema
	ListPage      = api.PiiAccessRequestListSchema
	AdminRequest  = api.PiiAccessRequestAdminSchema
	AdminListPage = api.PiiAccessRequestAdminListSchema
)

// Client handles requester operations on a table's PII access requests.
type Client struct {
	baseURL        string
	organizationID uuid.UUID
	verbosity      int
	shared         bool
}

// NewClient creates a requester client for the selected provider.
func NewClient(baseURL, organizationID string, verbosity int, shared bool) *Client {
	orgID, _ := uuid.Parse(organizationID)
	return &Client{baseURL: baseURL, organizationID: orgID, verbosity: verbosity, shared: shared}
}

func (c *Client) apiClient(token string) (*client.Client, error) {
	return client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Verbosity: c.verbosity})
}

// Create asks for unmasked access for the given duration and reason.
func (c *Client) Create(token string, tableID int, duration api.PiiGrantDuration, reason string) (*Request, error) {
	cl, err := c.apiClient(token)
	if err != nil {
		return nil, err
	}
	body := api.CreatePiiAccessRequestSchema{Duration: duration, Reason: reason}
	const msg = "request PII access failed"
	if c.shared {
		resp, err := cl.API.CreateThirdPartyPiiAccessRequestViewWithResponse(context.Background(), c.organizationID, tableID,
			&api.CreateThirdPartyPiiAccessRequestViewParams{}, body)
		if err != nil {
			return nil, comments.WrapAPIError(cl, err, msg)
		}
		return decode[Request](resp.HTTPResponse, resp.Body, http.StatusCreated, msg)
	}
	resp, err := cl.API.CreatePiiAccessRequestViewWithResponse(context.Background(), c.organizationID, tableID,
		&api.CreatePiiAccessRequestViewParams{}, body)
	if err != nil {
		return nil, comments.WrapAPIError(cl, err, msg)
	}
	return decode[Request](resp.HTTPResponse, resp.Body, http.StatusCreated, msg)
}

// List returns one page of the caller's requests, newest first.
func (c *Client) List(token string, tableID, page, limit int) (*ListPage, error) {
	cl, err := c.apiClient(token)
	if err != nil {
		return nil, err
	}
	const msg = "list PII access requests failed"
	if c.shared {
		resp, err := cl.API.ListThirdPartyPiiAccessRequestsViewWithResponse(context.Background(), c.organizationID, tableID,
			&api.ListThirdPartyPiiAccessRequestsViewParams{Page: &page, Limit: &limit})
		if err != nil {
			return nil, comments.WrapAPIError(cl, err, msg)
		}
		return decode[ListPage](resp.HTTPResponse, resp.Body, http.StatusOK, msg)
	}
	resp, err := cl.API.ListPiiAccessRequestsViewWithResponse(context.Background(), c.organizationID, tableID,
		&api.ListPiiAccessRequestsViewParams{Page: &page, Limit: &limit})
	if err != nil {
		return nil, comments.WrapAPIError(cl, err, msg)
	}
	return decode[ListPage](resp.HTTPResponse, resp.Body, http.StatusOK, msg)
}

// Withdraw withdraws the caller's open request on the table.
func (c *Client) Withdraw(token string, tableID, requestID int) (*Request, error) {
	cl, err := c.apiClient(token)
	if err != nil {
		return nil, err
	}
	const msg = "withdraw PII access request failed"
	if c.shared {
		resp, err := cl.API.WithdrawThirdPartyPiiAccessRequestViewWithResponse(context.Background(), c.organizationID, tableID,
			requestID, &api.WithdrawThirdPartyPiiAccessRequestViewParams{})
		if err != nil {
			return nil, comments.WrapAPIError(cl, err, msg)
		}
		return decode[Request](resp.HTTPResponse, resp.Body, http.StatusOK, msg)
	}
	resp, err := cl.API.WithdrawPiiAccessRequestViewWithResponse(context.Background(), c.organizationID, tableID,
		requestID, &api.WithdrawPiiAccessRequestViewParams{})
	if err != nil {
		return nil, comments.WrapAPIError(cl, err, msg)
	}
	return decode[Request](resp.HTTPResponse, resp.Body, http.StatusOK, msg)
}

// ListAll returns one page of every requester's requests on the table, newest
// first, through the owning organization's admin route.
func (c *Client) ListAll(token string, tableID, page, limit int) (*AdminListPage, error) {
	cl, err := c.apiClient(token)
	if err != nil {
		return nil, err
	}
	const msg = "list PII access requests failed"
	resp, err := cl.API.ListDatasetPiiAccessRequestsViewWithResponse(context.Background(), c.organizationID, tableID,
		&api.ListDatasetPiiAccessRequestsViewParams{Page: &page, Limit: &limit})
	if err != nil {
		return nil, comments.WrapAPIError(cl, err, msg)
	}
	return decode[AdminListPage](resp.HTTPResponse, resp.Body, http.StatusOK, msg)
}

// Approve approves an open request on the table through the owning
// organization's admin route. A nil duration or reason sends null, which keeps
// the request's own.
func (c *Client) Approve(token string, tableID, requestID int, duration *api.PiiGrantDuration, reason *string) (*AdminRequest, error) {
	cl, err := c.apiClient(token)
	if err != nil {
		return nil, err
	}
	const msg = "approve PII access request failed"
	resp, err := cl.API.ApprovePiiAccessRequestViewWithResponse(context.Background(), c.organizationID, tableID, requestID,
		&api.ApprovePiiAccessRequestViewParams{}, api.ApprovePiiAccessRequestSchema{Duration: duration, Reason: reason})
	if err != nil {
		return nil, comments.WrapAPIError(cl, err, msg)
	}
	return decode[AdminRequest](resp.HTTPResponse, resp.Body, http.StatusOK, msg)
}

// Deny denies an open request on the table with reason through the owning
// organization's admin route.
func (c *Client) Deny(token string, tableID, requestID int, reason string) (*AdminRequest, error) {
	cl, err := c.apiClient(token)
	if err != nil {
		return nil, err
	}
	const msg = "deny PII access request failed"
	resp, err := cl.API.DenyPiiAccessRequestViewWithResponse(context.Background(), c.organizationID, tableID, requestID,
		&api.DenyPiiAccessRequestViewParams{}, api.DenyPiiAccessRequestSchema{Reason: reason})
	if err != nil {
		return nil, comments.WrapAPIError(cl, err, msg)
	}
	return decode[AdminRequest](resp.HTTPResponse, resp.Body, http.StatusOK, msg)
}

func decode[T any](resp *http.Response, body []byte, success int, msg string) (*T, error) {
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, apierror.HandleHTTPErrorWithHeader(resp.StatusCode, body, resp.Header, msg)
	}
	if resp.StatusCode != success {
		return nil, comments.HandleError(resp.StatusCode, body, msg)
	}
	var value *T
	if err := jsonnum.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("%s: %w", msg, err)
	}
	if value == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", msg)
	}
	return value, nil
}
