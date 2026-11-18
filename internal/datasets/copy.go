package datasets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// The wire shapes of GET and POST /datasets/{id}/copy.
type (
	CopyOptions = api.DatasetCopyOptionsSchema
	CopyRequest = api.DatasetCopyRequestSchema
	CopyResult  = api.DatasetCopyPostResponseSchema
)

// CopyOptions returns what a copy of the dataset would carry: the suggested
// name and table name, each copy option with its count, and the data sources a
// copy can take; and the response body as received. A non-nil series sends its
// series_id and period_key, and the names returned are those the series gives
// the copy.
func (c *Client) CopyOptions(accessToken string, datasetID int, series *api.DatasetCopySeriesRequest) (*CopyOptions, []byte, error) {
	const contextMsg = "get copy options failed"
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}
	params := &api.GetDatasetCopyOptionsParams{}
	if series != nil {
		params.SeriesID = &series.SeriesID
		params.PeriodKey = &series.PeriodKey
	}
	resp, err := apiClient.API.GetDatasetCopyOptionsWithResponse(
		context.Background(), c.organizationID, datasetID, params)
	if err != nil {
		return nil, nil, wrapAPIError(apiClient, err, contextMsg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, handleError(resp.StatusCode(), resp.Body, contextMsg)
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("%s: unexpected empty response", contextMsg)
	}
	var options CopyOptions
	if err := jsonnum.Unmarshal(resp.Body, &options); err != nil {
		return nil, nil, fmt.Errorf("%s: decode response: %w", contextMsg, err)
	}
	return &options, resp.Body, nil
}

// Copy creates a new dataset from the dataset and returns it with the record of
// the copy, and the response body as received. req is sent as given; a nil
// Include goes out as null.
func (c *Client) Copy(accessToken string, datasetID int, req CopyRequest) (*CopyResult, []byte, error) {
	const contextMsg = "copy table failed"
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}
	resp, err := apiClient.API.PostDatasetCopyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.PostDatasetCopyParams{}, req)
	if err != nil {
		return nil, nil, wrapAPIError(apiClient, err, contextMsg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, handleError(resp.StatusCode(), resp.Body, contextMsg)
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("%s: unexpected empty response", contextMsg)
	}
	var result CopyResult
	if err := jsonnum.Unmarshal(resp.Body, &result); err != nil {
		return nil, nil, fmt.Errorf("%s: decode response: %w", contextMsg, err)
	}
	return &result, resp.Body, nil
}
