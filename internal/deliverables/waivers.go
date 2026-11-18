package deliverables

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// WaiverResult is a created waiver and its original response body.
type WaiverResult struct {
	*api.DeliverableRunWaiverResponse
	Body json.RawMessage `json:"-"`
}

// CheckWaivableRow checks live readiness for a row eligible for a waiver.
func (c *Client) CheckWaivableRow(token string, runID, tableID, rowID int) error {
	seen := 0
	carriedError := false
	for page := 1; ; page++ {
		result, err := c.Readiness(token, runID, ReadinessParams{Page: page, Limit: 500})
		if err != nil {
			return err
		}
		if result.RunID != runID {
			return fmt.Errorf("readiness returned run %d, expected run %d", result.RunID, runID)
		}
		if !result.canChangeWaivers() {
			return qerrors.NewBadUsagef("waivers can only be changed before sign-off; run %d is %s", runID, result.State)
		}
		eligible, carried := result.waiverEligibility(tableID, rowID)
		if eligible {
			return nil
		}
		carriedError = carriedError || carried
		seen += len(result.Rows)
		if seen >= result.RowsTotal || len(result.Rows) == 0 {
			break
		}
	}
	if carriedError {
		return qerrors.NewBadUsagef("readiness has a carried_rows error but no verified waiver target for row %d of table %d on run %d; check with qctl deliverable-run readiness %d; if the server supplies no waivable_row target, fix the row's data/filter first", rowID, tableID, runID, runID)
	}
	return qerrors.NewBadUsagef("row %d of table %d is not blocked or carried_unbound on run %d; check with qctl deliverable-run readiness %d", rowID, tableID, runID, runID)
}

func (r *Readiness) waiverEligibility(tableID, rowID int) (eligible, carriedError bool) {
	for _, row := range r.Rows {
		if row.MemberDatasetID == tableID && row.RowID == rowID && waivableBucket(row.Bucket) {
			return true, false
		}
	}
	for _, issue := range r.Errors {
		carriedError = carriedError || issue.BindingKind == "carried_rows"
		if row := issue.waivableCarriedRow(); row != nil && row.MemberDatasetID == tableID && row.RowID == rowID {
			return true, carriedError
		}
	}
	return false, carriedError
}

func waivableBucket(bucket string) bool {
	switch bucket {
	case "blocked_awaiting_review", "blocked_failing_rules", "blocked_indeterminate", "carried_unbound":
		return true
	}
	return false
}

// CreateWaiver creates a run's row waiver with a reason.
func (c *Client) CreateWaiver(token string, runID int, body api.WaiverCreateRequest) (*WaiverResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	const failed = "create deliverable run waiver failed"
	resp, err := apiClient.API.CreateDeliverableRunWaiverWithResponse(context.Background(), c.orgID, runID, &api.CreateDeliverableRunWaiverParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, failed, "create_deliverable_run_waiver")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, failed, "create_deliverable_run_waiver")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable run waiver response")
	}
	return &WaiverResult{DeliverableRunWaiverResponse: resp.JSON200, Body: resp.Body}, nil
}

// DeleteWaiver removes a waiver from one run.
func (c *Client) DeleteWaiver(token string, runID, waiverID int) error {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return err
	}
	const failed = "delete deliverable run waiver failed"
	resp, err := apiClient.API.DeleteDeliverableRunWaiverWithResponse(context.Background(), c.orgID, runID, waiverID, &api.DeleteDeliverableRunWaiverParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, failed, "delete_deliverable_run_waiver")
	}
	if resp.StatusCode() != http.StatusNoContent {
		return handleError(resp.StatusCode(), resp.Body, failed, "delete_deliverable_run_waiver")
	}
	return nil
}
