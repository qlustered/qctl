package table_series

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// Apply sends a prepared plan and returns the series, including partial results.
func (c *Client) Apply(token string, p *Plan) (*Series, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	id := p.targetID()
	var last *Series
	for i, request := range p.Requests {
		var written *Series
		if request.Schedule {
			var schedule *DeadlineSchedule
			if schedule, err = c.sendSchedule(apiClient, request, id); err == nil {
				p.Schedule = schedule
			}
		} else {
			written, err = c.sendRequest(apiClient, request, id)
		}
		if err != nil {
			stopped := fmt.Errorf("table series apply stopped at request %d of %d (%d completed); diff and reapply to finish: %w", i+1, len(p.Requests), i, err)
			if i == 0 {
				return nil, stopped
			}
			return c.applyResult(token, id, last, stopped)
		}
		if written != nil {
			id, last = written.ID, written
		}
	}
	return c.applyResult(token, id, last, nil)
}

func (c *Client) applyResult(token string, id uuid.UUID, last *Series, applyErr error) (*Series, error) {
	item, err := c.Get(token, id)
	if err != nil {
		if applyErr != nil {
			return last, fmt.Errorf("%w; reading the partial result failed: %s", applyErr, err)
		}
		return last, fmt.Errorf("table series changes applied, but reading the result failed: %w", err)
	}
	return item, applyErr
}

func (c *Client) sendRequest(apiClient *client.Client, request Request, target uuid.UUID) (*Series, error) {
	body := make(map[string]any, len(request.Body))
	for key, value := range request.Body {
		if key == "series_id" && value == createdID {
			value = target
		}
		body[key] = value
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode table series request: %w", err)
	}
	seriesID := request.SeriesID
	if seriesID == uuid.Nil {
		seriesID = target
	}
	ctx := context.Background()
	if request.DatasetID == 0 {
		if request.Method == http.MethodPost {
			resp, err := apiClient.API.CreateTableSeriesWithBodyWithResponse(ctx, c.orgID, &api.CreateTableSeriesParams{}, "application/json", bytes.NewReader(data))
			if err != nil {
				return nil, wrapAPIError(apiClient, err, "create table series failed")
			}
			if resp.StatusCode() != http.StatusCreated {
				return nil, handleError(resp.StatusCode(), resp.Body, "create table series failed")
			}
			if resp.JSON201 == nil {
				return nil, fmt.Errorf("unexpected empty create table series response")
			}
			return (*Series)(resp.JSON201), nil
		}
		resp, err := apiClient.API.PatchTableSeriesWithBodyWithResponse(ctx, c.orgID, seriesID, &api.PatchTableSeriesParams{}, "application/json", bytes.NewReader(data))
		if err != nil {
			return nil, wrapAPIError(apiClient, err, "patch table series failed")
		}
		if resp.StatusCode() != http.StatusOK {
			return nil, handleError(resp.StatusCode(), resp.Body, "patch table series failed")
		}
		return (*Series)(resp.JSON200), nil
	}
	return nil, memberRequestError(c.sendMemberRequest(apiClient, request, seriesID, data), request.DatasetID)
}

// sendSchedule PATCHes the deadline schedule of target, or of request.SeriesID when it is set, without a period_key.
func (c *Client) sendSchedule(apiClient *client.Client, request Request, target uuid.UUID) (*DeadlineSchedule, error) {
	data, err := json.Marshal(request.Body)
	if err != nil {
		return nil, fmt.Errorf("encode table series deadline schedule request: %w", err)
	}
	seriesID := request.SeriesID
	if seriesID == uuid.Nil {
		seriesID = target
	}
	resp, err := apiClient.API.PatchTableSeriesDeadlineScheduleWithBodyWithResponse(context.Background(), c.orgID, seriesID,
		&api.PatchTableSeriesDeadlineScheduleParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "patch table series deadline schedule failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "patch table series deadline schedule failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty table series deadline schedule response")
	}
	return resp.JSON200, nil
}

func (c *Client) sendMemberRequest(apiClient *client.Client, r Request, seriesID uuid.UUID, data []byte) error {
	ctx := context.Background()
	switch r.Method {
	case http.MethodPost:
		resp, err := apiClient.API.AddTableSeriesMemberWithBodyWithResponse(ctx, c.orgID, seriesID, &api.AddTableSeriesMemberParams{}, "application/json", bytes.NewReader(data))
		if err != nil {
			return wrapAPIError(apiClient, err, "add table series member failed")
		}
		if resp.StatusCode() != http.StatusCreated {
			return handleError(resp.StatusCode(), resp.Body, "add table series member failed")
		}
	case http.MethodPatch:
		resp, err := apiClient.API.PatchTableSeriesMemberWithBodyWithResponse(ctx, c.orgID, seriesID, r.DatasetID, &api.PatchTableSeriesMemberParams{}, "application/json", bytes.NewReader(data))
		if err != nil {
			return wrapAPIError(apiClient, err, "patch table series member failed")
		}
		if resp.StatusCode() != http.StatusOK {
			return handleError(resp.StatusCode(), resp.Body, "patch table series member failed")
		}
	case http.MethodDelete:
		resp, err := apiClient.API.DeleteTableSeriesMemberWithResponse(ctx, c.orgID, seriesID, r.DatasetID, &api.DeleteTableSeriesMemberParams{})
		if err != nil {
			return wrapAPIError(apiClient, err, "remove table series member failed")
		}
		if resp.StatusCode() != http.StatusNoContent {
			return handleError(resp.StatusCode(), resp.Body, "remove table series member failed")
		}
	}
	return nil
}

// Delete removes one empty, unreferenced series.
func (c *Client) Delete(token string, id uuid.UUID) error {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteTableSeriesWithResponse(context.Background(), c.orgID, id, &api.DeleteTableSeriesParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, "delete table series failed")
	}
	if resp.StatusCode() != http.StatusNoContent {
		return handleError(resp.StatusCode(), resp.Body, "delete table series failed")
	}
	return nil
}
