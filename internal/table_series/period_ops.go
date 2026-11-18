package table_series

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// Wire types of the series-period operations.
type (
	PeriodDeleteResult   = api.SeriesPeriodDeleteResponseSchema
	PeriodRecreateResult = api.SeriesPeriodRecreateResponseSchema
	PeriodRecreateBody   = api.SeriesPeriodRecreateRequestSchema
	ReloadableFile       = api.SeriesPeriodReloadableFileSchema
)

// periodOpTimeout is the per-attempt deadline of a period delete or recreate,
// each of which copies or moves the period's table in one request.
const periodOpTimeout = 5 * time.Minute

func (c *Client) newPeriodOpClient(token string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: token,
		Timeout: client.ResolveTimeout(periodOpTimeout), Verbosity: c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	return apiClient, nil
}

// DeletePeriod deletes period periodKey of series seriesID and returns the
// decoded response and its body.
func (c *Client) DeletePeriod(token string, seriesID uuid.UUID, periodKey string) (*PeriodDeleteResult, []byte, error) {
	apiClient, err := c.newPeriodOpClient(token)
	if err != nil {
		return nil, nil, err
	}
	resp, err := apiClient.API.DeleteTableSeriesPeriodWithResponse(context.Background(), c.orgID, seriesID, periodKey,
		&api.DeleteTableSeriesPeriodParams{})
	if err != nil {
		return nil, nil, wrapAPIError(apiClient, err, "delete series period failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, handleError(resp.StatusCode(), resp.Body, "delete series period failed")
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty series period delete response")
	}
	return resp.JSON200, resp.Body, nil
}

// RecreatePeriod recreates period periodKey of series seriesID as body asks
// and returns the decoded response and its body.
func (c *Client) RecreatePeriod(token string, seriesID uuid.UUID, periodKey string, body PeriodRecreateBody) (*PeriodRecreateResult, []byte, error) {
	apiClient, err := c.newPeriodOpClient(token)
	if err != nil {
		return nil, nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("encode series period recreate: %w", err)
	}
	resp, err := apiClient.API.RecreateTableSeriesPeriodWithBodyWithResponse(context.Background(), c.orgID, seriesID,
		periodKey, &api.RecreateTableSeriesPeriodParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, nil, wrapAPIError(apiClient, err, "recreate series period failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, handleError(resp.StatusCode(), resp.Body, "recreate series period failed")
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty series period recreate response")
	}
	return resp.JSON200, resp.Body, nil
}

// ListReloadableFiles reads the files of period periodKey's table that a
// recreate can reload, and the response body.
func (c *Client) ListReloadableFiles(token string, seriesID uuid.UUID, periodKey string) ([]ReloadableFile, []byte, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, nil, err
	}
	resp, err := apiClient.API.ListTableSeriesPeriodReloadableFilesWithResponse(context.Background(), c.orgID,
		seriesID, periodKey, &api.ListTableSeriesPeriodReloadableFilesParams{})
	if err != nil {
		return nil, nil, wrapAPIError(apiClient, err, "list reloadable files failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, handleError(resp.StatusCode(), resp.Body, "list reloadable files failed")
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty reloadable files response")
	}
	return *resp.JSON200, resp.Body, nil
}
