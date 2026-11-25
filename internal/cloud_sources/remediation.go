package cloud_sources

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// BulkResult is the qctl-side shape of a reprocess response at cloud-source
// scope.
//
// AffectedRows is a pointer because the response schema declares
// affected_rows optional. nil means "the API did not say", and the command
// layer prints "requested" rather than inventing a number.
type BulkResult struct {
	AffectedRows *int
}

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

func bulk[T any](status int, body []byte, json200 *T, affectedRows func(*T) *int, contextMsg string) (*BulkResult, error) {
	if status != http.StatusOK {
		return nil, handleError(status, body, contextMsg)
	}
	if json200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", contextMsg)
	}
	return &BulkResult{AffectedRows: affectedRows(json200)}, nil
}

func bulkActionCount(r *api.BulkActionByStatusResponseSchema) *int { return r.AffectedRows }

// DeleteResult is a delete route's answer. UnmatchedSignatures and
// NotExcludedSignatures are nil for a delete-all-bad-rows answer, whose schema
// has neither.
type DeleteResult = api.DeleteRowsResponseSchema

// deleted returns a delete-rows answer, or the error for a non-200.
func deleted(status int, body []byte, json200 *DeleteResult, contextMsg string) (*DeleteResult, error) {
	if status != http.StatusOK {
		return nil, handleError(status, body, contextMsg)
	}
	if json200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", contextMsg)
	}
	return json200, nil
}

// deletedByStatus returns a delete-all-bad-rows answer as a DeleteResult, or
// the error for a non-200.
func deletedByStatus(status int, body []byte, json200 *api.DeleteRowsByStatusResponseSchema, contextMsg string) (*DeleteResult, error) {
	if status != http.StatusOK {
		return nil, handleError(status, body, contextMsg)
	}
	if json200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", contextMsg)
	}
	return &DeleteResult{
		Result:              json200.Result,
		AffectedRows:        json200.AffectedRows,
		SkippedRepeatedKeys: json200.SkippedRepeatedKeys,
	}, nil
}

// KeepInQuarantineResult is the update-keep-in-quarantine answer.
type KeepInQuarantineResult = api.UpdateKeepInQuarantineResponseSchema

func keepInQuarantineResult(status int, body []byte, json200 *KeepInQuarantineResult, contextMsg string) (*KeepInQuarantineResult, error) {
	if status != http.StatusOK {
		return nil, handleError(status, body, contextMsg)
	}
	if json200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", contextMsg)
	}
	return json200, nil
}

// DeleteRows deletes this source's live quarantine rows in the selected table
// by hex initial_signature_, recording comment as the reason.
// excludeFromFutureUploads is sent as exclude_from_future_uploads, null when
// nil.
//
// delete_rows_in_data_source_core (data_view_core.py) adds each deleted row's
// source-scoped identity entries to the excluded_row_signatures of every file a
// deleted row is linked to, and with excludeFromFutureUploads true to the
// source's excluded_row_signatures_override as well, or to the table's
// excluded_row_signatures for a table-series source, withholding an entry that
// would drop a live clean row (delete_data_rows, quarantine.py).
// UnmatchedSignatures are the requested signatures with no live quarantine
// row of this source; NotExcludedSignatures are those deleted with an entry
// withheld.
func (c *Client) DeleteRows(accessToken string, dataSourceModelID int, signatures []string, isBadData bool, datasetID *int, excludeFromFutureUploads *bool, comment string) (*DeleteResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	// signatures is non-nullable: a nil slice behind the pointer marshals to
	// `null`, which fails the schema.
	if signatures == nil {
		signatures = []string{}
	}
	body := api.DeleteRowsRequestSchema{Signatures: &signatures, ExcludeFromFutureUploads: excludeFromFutureUploads, Comment: comment}
	var resp *api.DeleteRowsInDataSourceResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.DeleteThirdPartyRowsInDataSource(
			context.Background(), c.organizationID, dataSourceModelID,
			&api.DeleteThirdPartyRowsInDataSourceParams{IsBadData: &isBadData, DatasetID: datasetID}, body)
		err = requestErr
		if err == nil {
			resp, err = api.ParseDeleteRowsInDataSourceResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.DeleteRowsInDataSourceWithResponse(
			context.Background(), c.organizationID, dataSourceModelID,
			&api.DeleteRowsInDataSourceParams{IsBadData: &isBadData, DatasetID: datasetID}, body)
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "delete rows failed")
	}
	return deleted(resp.StatusCode(), resp.Body, resp.JSON200, "delete rows failed")
}

// DeleteAllBadRows deletes all live quarantined rows of this source in the
// selected table, recording comment as the reason.
//
// delete_bad_rows_by_status (quarantine.py) adds no exclusion entries and
// skips every row of a repeated-key group the selection splits, listing those
// groups in SkippedRepeatedKeys.
func (c *Client) DeleteAllBadRows(accessToken string, dataSourceModelID int, datasetID *int, comment string) (*DeleteResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.DeleteAllBadRowsRequestSchema{Comment: comment}
	var resp *api.DeleteAllBadRowsForDataSourceViewResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.DeleteAllThirdPartyBadRowsForDataSourceView(
			context.Background(), c.organizationID, dataSourceModelID,
			&api.DeleteAllThirdPartyBadRowsForDataSourceViewParams{DatasetID: datasetID}, body)
		err = requestErr
		if err == nil {
			resp, err = api.ParseDeleteAllBadRowsForDataSourceViewResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.DeleteAllBadRowsForDataSourceViewWithResponse(
			context.Background(), c.organizationID, dataSourceModelID,
			&api.DeleteAllBadRowsForDataSourceViewParams{DatasetID: datasetID}, body)
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "delete all quarantined rows failed")
	}
	return deletedByStatus(resp.StatusCode(), resp.Body, resp.JSON200, "delete all quarantined rows failed")
}

// ReprocessAllBadRows reprocesses quarantined source rows in the selected table.
func (c *Client) ReprocessAllBadRows(accessToken string, dataSourceModelID int, datasetID *int) (*BulkResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	var resp *api.ReprocessAllBadRowsForDataSourceViewResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.ReprocessAllThirdPartyBadRowsForDataSourceView(
			context.Background(), c.organizationID, dataSourceModelID,
			&api.ReprocessAllThirdPartyBadRowsForDataSourceViewParams{DatasetID: datasetID})
		err = requestErr
		if err == nil {
			resp, err = api.ParseReprocessAllBadRowsForDataSourceViewResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.ReprocessAllBadRowsForDataSourceViewWithResponse(
			context.Background(), c.organizationID, dataSourceModelID,
			&api.ReprocessAllBadRowsForDataSourceViewParams{DatasetID: datasetID})
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "reprocess quarantined rows failed")
	}
	return bulk(resp.StatusCode(), resp.Body, resp.JSON200, bulkActionCount, "reprocess quarantined rows failed")
}

// UpdateKeepInQuarantine drives `rows hold`, `rows release` and
// `rows reset-hold` at this scope. quarantine_hold is required and
// non-nullable, so every request names one of the three values; the
// dataset-scope twin carries the derivation of what each one does.
//
// Sending NEITHER ids nor a status is legal here and means "every quarantine row
// of this source": update_keep_in_quarantine_core raises 422 for an empty
// selector only when limit_to_data_source_model_id is None
// (data_view_core.py), and this route always sets it. qctl exposes
// that as an explicit --all rather than letting it happen by omission.
func (c *Client) UpdateKeepInQuarantine(accessToken string, dataSourceModelID int, hold api.QuarantineHold, ids []int, status *api.QuarantineStatus, datasetID *int) (*KeepInQuarantineResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.UpdateKeepInQuarantineRequestSchema{QuarantineHold: hold, QuarantineStatus: status}
	if len(ids) > 0 {
		body.Ids = &ids
	}
	var resp *api.UpdateKeepInQuarantineForDataSourceViewResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.UpdateThirdPartyKeepInQuarantineForDataSourceView(
			context.Background(), c.organizationID, dataSourceModelID,
			&api.UpdateThirdPartyKeepInQuarantineForDataSourceViewParams{DatasetID: datasetID}, body)
		err = requestErr
		if err == nil {
			resp, err = api.ParseUpdateKeepInQuarantineForDataSourceViewResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.UpdateKeepInQuarantineForDataSourceViewWithResponse(
			context.Background(), c.organizationID, dataSourceModelID,
			&api.UpdateKeepInQuarantineForDataSourceViewParams{DatasetID: datasetID}, body)
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "update quarantine hold failed")
	}
	return keepInQuarantineResult(resp.StatusCode(), resp.Body, resp.JSON200, "update quarantine hold failed")
}
