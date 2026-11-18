package deliverables

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// MaxCompletenessLimit is the largest limit the completeness endpoint accepts.
const MaxCompletenessLimit = 10000

const completenessTimeout = 45 * time.Second

// CompletenessParams selects the maximum number of missed rows; zero sends no limit.
type CompletenessParams struct {
	Limit int
}

func (p CompletenessParams) apiParams() *api.ReadDeliverableCompletenessParams {
	out := &api.ReadDeliverableCompletenessParams{}
	if p.Limit > 0 {
		out.Limit = &p.Limit
	}
	return out
}

// CompletenessResult is a deliverable's missed rows and the original response body.
type CompletenessResult struct {
	*api.CompletenessResponse
	Body json.RawMessage `json:"-"`
}

// Completeness reads a deliverable's missed rows and their destinations.
func (c *Client) Completeness(token string, id uuid.UUID, params CompletenessParams) (*CompletenessResult, error) {
	apiClient, err := c.newAPIClientWithin(token, completenessTimeout)
	if err != nil {
		return nil, err
	}
	const failed = "read deliverable completeness failed"
	resp, err := apiClient.API.ReadDeliverableCompletenessWithResponse(context.Background(), c.orgID, id, params.apiParams())
	if err != nil {
		return nil, wrapAPIError(apiClient, err, failed, "read_deliverable_completeness")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, failed, "read_deliverable_completeness")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable completeness response")
	}
	return &CompletenessResult{CompletenessResponse: resp.JSON200, Body: resp.Body}, nil
}
