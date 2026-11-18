// Package submissions reads the MGA's broker board, where each table-series
// intake's submission for a period stands, and waives a period of an intake.
package submissions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// Board is one series' board for one period.
type Board = api.SubmissionBoardSchema

// Row is one intake's submission for one period.
type Row = api.SubmissionBoardRowSchema

// SeriesCounts is one series' board counts for the period being collected.
type SeriesCounts = api.SeriesSubmissionCountsSchema

// Counts is how many intakes are in each board status.
type Counts = api.SubmissionBoardCountsSchema

// PeriodStatus is one intake's status for one period.
type PeriodStatus = api.SubmissionPeriodStatusSchema

// APIError carries the backend's error metadata.
type APIError = comments.APIError

// Client reads submission boards and waives periods in one organization.
type Client struct {
	baseURL   string
	orgID     uuid.UUID
	verbosity int
}

// NewClient creates a submissions client.
func NewClient(baseURL, orgID string, verbosity int) *Client {
	id, _ := uuid.Parse(orgID)
	return &Client{baseURL: baseURL, orgID: id, verbosity: verbosity}
}

func (c *Client) newAPIClient(token string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: token,
		Timeout: client.ResolveTimeout(0), Verbosity: c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	return apiClient, nil
}

// SeriesBoard reads series seriesID's board for periodKey, or, when periodKey
// is "", for the period the server picks.
func (c *Client) SeriesBoard(token string, seriesID uuid.UUID, periodKey string) (*Board, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	params := &api.GetSeriesSubmissionBoardParams{}
	if periodKey != "" {
		params.PeriodKey = &periodKey
	}
	const msg = "get series submission board failed"
	resp, err := apiClient.API.GetSeriesSubmissionBoardWithResponse(context.Background(), c.orgID, seriesID, params)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty series submission board response")
	}
	return resp.JSON200, nil
}

// IntakeBoard reads series cloud source cloudSourceID's board rows, one per
// period it is expected for, in the server's order.
func (c *Client) IntakeBoard(token string, cloudSourceID int) ([]Row, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	const msg = "get intake submission board failed"
	resp, err := apiClient.API.GetIntakeSubmissionBoardWithResponse(context.Background(), c.orgID, cloudSourceID, &api.GetIntakeSubmissionBoardParams{})
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty intake submission board response")
	}
	return *resp.JSON200, nil
}

// SeriesCounts reads each visible series' board counts, in the server's
// order.
func (c *Client) SeriesCounts(token string) ([]SeriesCounts, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	const msg = "get series submission counts failed"
	resp, err := apiClient.API.GetSeriesSubmissionCountsWithResponse(context.Background(), c.orgID, &api.GetSeriesSubmissionCountsParams{})
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty series submission counts response")
	}
	return *resp.JSON200, nil
}

// WaiverResult is an intake's status for a period after a waiver change, and
// the response body it came in. Status and Body are nil when the server
// answered 204.
type WaiverResult struct {
	Status *PeriodStatus
	Body   json.RawMessage
}

// Waive waives series cloud source cloudSourceID's submission for periodKey,
// sending reason as the waiver's reason.
func (c *Client) Waive(token string, cloudSourceID int, periodKey, reason string) (*WaiverResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	const msg = "waive submission failed"
	resp, err := apiClient.API.WaiveSubmissionWithResponse(context.Background(), c.orgID, cloudSourceID, periodKey, &api.WaiveSubmissionParams{},
		api.WaiveSubmissionJSONRequestBody{Reason: reason})
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty waive submission response")
	}
	return &WaiverResult{Status: resp.JSON200, Body: resp.Body}, nil
}

// WithdrawWaiver withdraws the waiver in effect for series cloud source
// cloudSourceID's periodKey. A 204 answer is a result with a nil Status.
func (c *Client) WithdrawWaiver(token string, cloudSourceID int, periodKey string) (*WaiverResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	const msg = "withdraw waiver failed"
	resp, err := apiClient.API.WithdrawWaiverWithResponse(context.Background(), c.orgID, cloudSourceID, periodKey, &api.WithdrawWaiverParams{})
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	switch resp.StatusCode() {
	case http.StatusNoContent:
		return &WaiverResult{}, nil
	case http.StatusOK:
	default:
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty withdraw waiver response")
	}
	return &WaiverResult{Status: resp.JSON200, Body: resp.Body}, nil
}
