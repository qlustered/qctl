package deliverables

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// driftTimeout is the deadline Drift gives its request before
// client.ResolveTimeout applies --request-timeout.
const driftTimeout = 45 * time.Second

// DriftParams selects a delivered run's drift rows. A zero value sends
// nothing.
type DriftParams struct {
	Limit int
}

func (p DriftParams) apiParams() *api.ReadDeliveredRunDriftParams {
	out := &api.ReadDeliveredRunDriftParams{}
	if p.Limit > 0 {
		out.Limit = &p.Limit
	}
	return out
}

// DriftResult is a delivered run's drift and the original response body.
type DriftResult struct {
	*api.DeliveredRunDriftResponse
	Body json.RawMessage `json:"-"`
}

// Drift reads how a delivered run's rows differ from their live rows.
func (c *Client) Drift(token string, runID int, params DriftParams) (*DriftResult, error) {
	apiClient, err := c.newAPIClientWithin(token, driftTimeout)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ReadDeliveredRunDriftWithResponse(context.Background(), c.orgID, runID, params.apiParams())
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "read delivered run drift failed", "read_delivered_run_drift")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "read delivered run drift failed", "read_delivered_run_drift")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty delivered run drift response")
	}
	var drift api.DeliveredRunDriftResponse
	if err := jsonnum.Unmarshal(resp.Body, &drift); err != nil {
		return nil, fmt.Errorf("decode delivered run drift: %w", err)
	}
	return &DriftResult{DeliveredRunDriftResponse: &drift, Body: resp.Body}, nil
}
