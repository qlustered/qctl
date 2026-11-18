package datasets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// DataPage is the decoded backend get_data response (parallel-array schema).
type DataPage = api.DataWithCellHistorySchema

// GetData fetches one page of dataset data. params is sent unmodified; nil is treated as
// an empty GetDataParams. The backend remains authoritative for all validation.
//
// opts carry the query parameters api.GetDataParams cannot hold — today only
// WithAlertItemID, whose generated field is unusable; see alert_item_filter.go.
func (c *Client) GetData(accessToken string, datasetID int, params *api.GetDataParams, opts ...DataQueryOption) (*DataPage, error) {
	page, _, err := c.GetDataWithBody(accessToken, datasetID, params, opts...)
	return page, err
}

// GetDataWithBody is GetData plus the response body exactly as the server sent
// it, for output that must not pass through DataPage: its FieldDisplayFormats
// and Labels have no null element, so decoding turns the null format of a
// non-numeric column into {kind: "", decimals: null} and a null label into "".
func (c *Client) GetDataWithBody(accessToken string, datasetID int, params *api.GetDataParams, opts ...DataQueryOption) (*DataPage, []byte, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create API client: %w", err)
	}
	if params == nil {
		params = &api.GetDataParams{}
	}

	var resp *api.GetDataResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.GetThirdPartyData(
			context.Background(), c.organizationID, datasetID, (*api.GetThirdPartyDataParams)(params), applyDataQueryOptions(opts)...)
		err = requestErr
		if err == nil {
			resp, err = api.ParseGetDataResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.GetDataWithResponse(
			context.Background(), c.organizationID, datasetID, params, applyDataQueryOptions(opts)...)
	}
	if err != nil {
		return nil, nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty response")
	}
	page, err := decodeDataPage(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return page, resp.Body, nil
}

// decodeDataPage decodes a get_data body with UseNumber, so every number in
// results reaches the caller as the json.Number the server wrote. The
// generated JSON200 holds them as float64, which cannot carry a BigInteger
// cell past 2^53: 1234567890123456789 became 1234567890123456800 in -o json
// and 1234567890123460000 in the table view.
func decodeDataPage(body []byte) (*DataPage, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var page DataPage
	if err := dec.Decode(&page); err != nil {
		return nil, fmt.Errorf("failed to decode data response: %w", err)
	}
	return &page, nil
}
