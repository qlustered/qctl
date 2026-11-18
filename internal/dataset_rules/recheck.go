package dataset_rules //nolint:revive // Public package name matches the resource domain.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// CleanRowRecheckCount reads the bounded count of eligible clean rows.
func (c *Client) CleanRowRecheckCount(token string, tableID int, ruleID string) (*api.CleanRowRecheckCountSchema, error) {
	id, err := uuid.Parse(ruleID)
	if err != nil {
		return nil, fmt.Errorf("invalid table rule ID: %w", err)
	}
	cl, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: c.timeout, Verbosity: c.verbosity})
	if err != nil {
		return nil, err
	}
	resp, err := cl.API.GetCleanRowRecheckCountWithResponse(context.Background(), c.organizationID, tableID, id, &api.GetCleanRowRecheckCountParams{})
	if err != nil {
		return nil, cl.HandleError(err, "re-check preview failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "re-check preview failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty re-check preview")
	}
	return resp.JSON200, nil
}

// RecheckCleanRows moves eligible clean rows to quarantine for reprocessing.
func (c *Client) RecheckCleanRows(token string, tableID int, ruleID string) (*api.MoveRowsToQuarantineResponseSchema, error) {
	id, err := uuid.Parse(ruleID)
	if err != nil {
		return nil, fmt.Errorf("invalid table rule ID: %w", err)
	}
	cl, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: c.timeout, Verbosity: c.verbosity})
	if err != nil {
		return nil, err
	}
	resp, err := cl.API.RecheckCleanRowsForDatasetRuleWithResponse(context.Background(), c.organizationID, tableID, id, &api.RecheckCleanRowsForDatasetRuleParams{})
	if err != nil {
		return nil, cl.HandleError(err, "re-check failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "re-check failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty re-check response")
	}
	return resp.JSON200, nil
}
