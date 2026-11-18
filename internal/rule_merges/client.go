// Package rule_merges is the domain client for rule merges: previewing a merge
// of one table's rules into another, reading a merge back, and applying it.
package rule_merges //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// Client handles HTTP requests for rule merge operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new rule merges client.
func NewClient(baseURL string, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgID,
		verbosity:      verbosity,
	}, nil
}

// Type aliases for generated types.
type (
	RuleMerge = api.RuleMergeSchema     //nolint:revive // Public alias for the generated merge.
	Item      = api.RuleMergeItemSchema //nolint:revive // Public alias for the generated merge item.
)

// Resolution is one resolution Apply sends. RowFilterSet sends row_filter
// even when RowFilter is nil, as null, which removes the source rule's filter.
type Resolution struct {
	api.RuleMergeResolutionSchema
	RowFilterSet bool
}

// MarshalJSON encodes the generated resolution, with row_filter null when
// RowFilterSet is true and RowFilter is nil.
func (r Resolution) MarshalJSON() ([]byte, error) {
	if !r.RowFilterSet {
		return json.Marshal(r.RuleMergeResolutionSchema)
	}
	// The outer row_filter hides the embedded one, whose omitempty drops nil.
	return json.Marshal(struct {
		api.RuleMergeResolutionSchema
		RowFilter *api.DescribedRowFilter `json:"row_filter"`
	}{r.RuleMergeResolutionSchema, r.RowFilter})
}

// applyRequest is the body of an apply: api.RuleMergeApplyRequestSchema with
// each resolution encoded by Resolution.MarshalJSON.
type applyRequest struct {
	Resolutions []Resolution `json:"resolutions,omitempty"`
}

// Preview stores and returns a new previewed merge of sourceDatasetID's rules
// into targetDatasetID, with the response body as received.
func (c *Client) Preview(accessToken string, targetDatasetID, sourceDatasetID int) (*RuleMerge, []byte, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}

	const contextMsg = "failed to preview rule merge"
	resp, err := apiClient.API.PostRuleMergeWithResponse(
		context.Background(), c.organizationID, targetDatasetID, &api.PostRuleMergeParams{},
		api.RuleMergeRequestSchema{SourceDatasetID: sourceDatasetID})
	if err != nil {
		return nil, nil, wrapAPIError(apiClient, err, contextMsg)
	}
	return ruleMergeFromResponse(resp.StatusCode(), http.StatusOK, resp.Body, contextMsg)
}

// Get returns merge mergeID into targetDatasetID, with the response body as
// received. The server settles an applying merge before it answers.
func (c *Client) Get(accessToken string, targetDatasetID, mergeID int) (*RuleMerge, []byte, error) {
	return c.GetContext(context.Background(), accessToken, targetDatasetID, mergeID)
}

// GetContext is Get with ctx controlling the request.
func (c *Client) GetContext(ctx context.Context, accessToken string, targetDatasetID, mergeID int) (*RuleMerge, []byte, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}

	const contextMsg = "failed to get rule merge"
	resp, err := apiClient.API.GetRuleMergeWithResponse(
		ctx, c.organizationID, targetDatasetID, mergeID, &api.GetRuleMergeParams{})
	if err != nil {
		return nil, nil, wrapAPIError(apiClient, err, contextMsg)
	}
	return ruleMergeFromResponse(resp.StatusCode(), http.StatusOK, resp.Body, contextMsg)
}

// Apply applies previewed merge mergeID into targetDatasetID with resolutions,
// sent in order, and returns the merge the server accepted with the response
// body as received. With no resolutions the request carries none.
func (c *Client) Apply(accessToken string, targetDatasetID, mergeID int, resolutions []Resolution) (*RuleMerge, []byte, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}

	const contextMsg = "failed to apply rule merge"
	body, err := json.Marshal(applyRequest{Resolutions: resolutions})
	if err != nil {
		return nil, nil, fmt.Errorf("%s: failed to encode request: %w", contextMsg, err)
	}
	resp, err := apiClient.API.ApplyRuleMergeWithBodyWithResponse(
		context.Background(), c.organizationID, targetDatasetID, mergeID, &api.ApplyRuleMergeParams{},
		"application/json", bytes.NewReader(body))
	if err != nil {
		return nil, nil, wrapAPIError(apiClient, err, contextMsg)
	}
	return ruleMergeFromResponse(resp.StatusCode(), http.StatusAccepted, resp.Body, contextMsg)
}

// ruleMergeFromResponse decodes a RuleMerge from body when status is want and
// returns body with it. A non-2xx status is an *APIError; any other 2xx status
// is an error too.
func ruleMergeFromResponse(status, want int, body []byte, contextMsg string) (*RuleMerge, []byte, error) {
	if status < 200 || status >= 300 {
		return nil, nil, handleError(status, body, contextMsg)
	}
	if status != want {
		return nil, nil, fmt.Errorf("%s: unexpected HTTP status %d, want %d", contextMsg, status, want)
	}
	var merge RuleMerge
	if err := jsonnum.Unmarshal(body, &merge); err != nil {
		return nil, nil, fmt.Errorf("%s: failed to decode response: %w", contextMsg, err)
	}
	return &merge, body, nil
}

func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	return apiClient, nil
}
