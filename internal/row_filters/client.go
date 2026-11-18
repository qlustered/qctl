package row_filters //nolint:revive // Matches the resource domain.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// Client counts the rows a row filter keeps.
type Client struct {
	baseURL        string
	organizationID uuid.UUID
	verbosity      int
}

// NewClient creates a row filter client for the organization.
func NewClient(baseURL, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{baseURL: baseURL, organizationID: orgID, verbosity: verbosity}, nil
}

// CountKept returns the server's count of the table's rows that f keeps. f's
// description is not sent.
func (c *Client) CountKept(token string, tableID int, f *Filter) (*api.RowFilterCountSchema, error) {
	cl, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Verbosity: c.verbosity})
	if err != nil {
		return nil, err
	}
	body := api.RowFilterCountRequestSchema{RowFilter: Draft(f)}
	resp, err := cl.API.CountRowsKeptByDraftRowFilterWithResponse(context.Background(), c.organizationID, tableID, &api.CountRowsKeptByDraftRowFilterParams{}, body)
	if err != nil {
		return nil, cl.HandleError(err, "row filter count failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "row filter count failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty row filter count")
	}
	return resp.JSON200, nil
}
