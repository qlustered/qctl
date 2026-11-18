package stored_items

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// DeleteResult is the delete route's answer at file scope.
type DeleteResult = api.DeleteRowsResponseSchema

func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	return apiClient, nil
}

// DeleteRows deletes live quarantine rows by hex initial_signature_ through
// one uploaded file's route, recording comment as the reason.
//
// delete_data_rows (quarantine.py) matches the signatures across the table,
// not only among rows linked to this file, and adds each deleted row's
// source-scoped identity entries to the file's excluded_row_signatures,
// withholding an entry a live clean row linked to this file also has.
// UnmatchedSignatures are the requested signatures with no live quarantine
// row; NotExcludedSignatures are those deleted with an entry withheld.
//
// A non-nil datasetID is sent as dataset_id, the table the file must belong
// to. There is no status parameter: DeleteRowsInStoredItemParams has none,
// which is what makes --status unsupported at file scope.
func (c *Client) DeleteRows(accessToken string, storedItemID int, signatures []string, isBadData bool, datasetID *int, comment string) (*DeleteResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	// signatures is non-nullable: a nil slice behind the pointer marshals to
	// `null`, which fails the schema.
	if signatures == nil {
		signatures = []string{}
	}
	body := api.DeleteRowsRequestSchema{Signatures: &signatures, Comment: comment}
	var resp *api.DeleteRowsInStoredItemResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.DeleteThirdPartyRowsInStoredItem(
			context.Background(), c.organizationID, storedItemID,
			&api.DeleteThirdPartyRowsInStoredItemParams{IsBadData: &isBadData, DatasetID: datasetID}, body)
		err = requestErr
		if err == nil {
			resp, err = api.ParseDeleteRowsInStoredItemResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.DeleteRowsInStoredItemWithResponse(
			context.Background(), c.organizationID, storedItemID,
			&api.DeleteRowsInStoredItemParams{IsBadData: &isBadData, DatasetID: datasetID}, body)
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "delete rows failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "delete rows failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("delete rows failed: unexpected empty response")
	}
	return resp.JSON200, nil
}
