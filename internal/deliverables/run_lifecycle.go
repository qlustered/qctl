package deliverables

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// RunTransitionResult is the updated run and the original response body.
type RunTransitionResult struct {
	Run  *Run
	Body json.RawMessage
}

// MarkDelivered records a delivery note for run runID.
func (c *Client) MarkDelivered(token string, runID int, note string) (*RunTransitionResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	body := api.MarkDeliveredRequest{Note: note}
	resp, err := apiClient.API.MarkDeliverableRunDeliveredWithResponse(context.Background(), c.orgID, runID, &api.MarkDeliverableRunDeliveredParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "mark deliverable run delivered failed", "mark_deliverable_run_delivered")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "mark deliverable run delivered failed", "mark_deliverable_run_delivered")
	}
	return decodeRunTransition(resp.Body)
}

// CancelRun cancels run runID.
func (c *Client) CancelRun(token string, runID int) (*RunTransitionResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CancelDeliverableRunWithResponse(context.Background(), c.orgID, runID, &api.CancelDeliverableRunParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "cancel deliverable run failed", "cancel_deliverable_run")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "cancel deliverable run failed", "cancel_deliverable_run")
	}
	return decodeRunTransition(resp.Body)
}

func decodeRunTransition(body json.RawMessage) (*RunTransitionResult, error) {
	var run *Run
	if err := jsonnum.Unmarshal(body, &run); err != nil {
		return nil, fmt.Errorf("decode updated deliverable run: %w", err)
	}
	if run == nil {
		return nil, fmt.Errorf("unexpected empty deliverable run response")
	}
	return &RunTransitionResult{Run: run, Body: body}, nil
}
