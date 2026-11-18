package datasets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// BulkResult is the qctl-side shape of an affected-row remediation response.
// A nil count means the API did not say how many rows were affected.
type BulkResult struct {
	AffectedRows *int
}

// QuarantineMoveResult preserves the move route's distinct outcomes. Pointers
// distinguish an older response without counts from an actual zero. Decode
// from the body because the generated schema's int fields lose that distinction.
//
// CommentThreadID is the thread a --row-id move's comment opened, and
// NotifiedUserCount is how many followers a move-all comment notified; each is
// nil when the server sent null or no value.
type QuarantineMoveResult struct {
	Moved               *int    `json:"moved"`
	AlreadyInQuarantine *int    `json:"already_in_quarantine"`
	NotFound            *int    `json:"not_found"`
	CommentThreadID     *string `json:"comment_thread_id,omitempty"`
	NotifiedUserCount   *int    `json:"notified_user_count,omitempty"`
}

// MoveComment is the optional comment on a --row-id move. A nil Text sends
// neither field; IsInternal is sent only alongside Text.
type MoveComment struct {
	Text       *string
	IsInternal *bool
}

func quarantineMoveResult(body []byte) (*QuarantineMoveResult, error) {
	var result QuarantineMoveResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode quarantine move counts: %w", err)
	}
	return &result, nil
}

// QuarantineCounts is the hold-state and validation-status counts returned by
// the count-by-status endpoint.
type QuarantineCounts = api.QuarantineCountByStatusResponseSchema

// KeepInQuarantineResult is the update-keep-in-quarantine answer.
type KeepInQuarantineResult = api.UpdateKeepInQuarantineResponseSchema

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

func keepInQuarantineResult(status int, body []byte, json200 *KeepInQuarantineResult, contextMsg string) (*KeepInQuarantineResult, error) {
	if status != http.StatusOK {
		return nil, handleError(status, body, contextMsg)
	}
	if json200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", contextMsg)
	}
	return json200, nil
}

// DeleteRows deletes live quarantine rows by hex initial_signature_, or by
// status when signatures is empty and status is not nil, recording comment as
// the reason.
//
// signatures is always sent as a LIST: the backend takes its status branch
// only under `if not body.signatures and quarantine_status is not None`
// (delete_rows_in_dataset_core, data_view_core.py).
//
// The signature form (delete_data_rows, quarantine.py) adds each deleted
// row's source-scoped identity entries to the table's excluded_row_signatures,
// withholding an entry a live clean row with a requested signature also has.
// UnmatchedSignatures are the requested signatures with no live quarantine
// row; NotExcludedSignatures are those deleted with an entry withheld.
//
// The status form (delete_bad_rows_by_status, quarantine.py) adds no entries
// and skips every row of a repeated-key group the status splits, listing those
// groups in SkippedRepeatedKeys; its signature lists come back empty.
func (c *Client) DeleteRows(accessToken string, datasetID int, signatures []string, isBadData bool, status *api.QuarantineStatus, comment string) (*DeleteResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	if signatures == nil {
		signatures = []string{}
	}
	body := api.DeleteRowsRequestSchema{Signatures: &signatures, Comment: comment}
	params := &api.DeleteRowsInDatasetParams{IsBadData: &isBadData, QuarantineStatus: status}
	var resp *api.DeleteRowsInDatasetResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.DeleteThirdPartyRowsInDataset(
			context.Background(), c.organizationID, datasetID, (*api.DeleteThirdPartyRowsInDatasetParams)(params), body)
		err = requestErr
		if err == nil {
			resp, err = api.ParseDeleteRowsInDatasetResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.DeleteRowsInDatasetWithResponse(
			context.Background(), c.organizationID, datasetID, params, body)
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "delete rows failed")
	}
	return deleted(resp.StatusCode(), resp.Body, resp.JSON200, "delete rows failed")
}

// DeleteAllBadRows deletes every live quarantined row in the dataset,
// recording comment as the reason. The route is the whole selector: its body
// is the comment alone and it takes no status filter.
//
// delete_bad_rows_by_status (quarantine.py) adds no exclusion entries and
// skips every row of a repeated-key group the selection splits, listing those
// groups in SkippedRepeatedKeys.
func (c *Client) DeleteAllBadRows(accessToken string, datasetID int, comment string) (*DeleteResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.DeleteAllBadRowsRequestSchema{Comment: comment}
	var resp *api.DeleteAllBadRowsInDatasetViewResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.DeleteAllThirdPartyBadRowsInDatasetView(
			context.Background(), c.organizationID, datasetID, &api.DeleteAllThirdPartyBadRowsInDatasetViewParams{}, body)
		err = requestErr
		if err == nil {
			resp, err = api.ParseDeleteAllBadRowsInDatasetViewResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.DeleteAllBadRowsInDatasetViewWithResponse(
			context.Background(), c.organizationID, datasetID, &api.DeleteAllBadRowsInDatasetViewParams{}, body)
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "delete all quarantined rows failed")
	}
	return deletedByStatus(resp.StatusCode(), resp.Body, resp.JSON200, "delete all quarantined rows failed")
}

// ReprocessBadRows sends quarantined rows back through validation.
//
// ids is always a LIST: reprocess_bad_rows_core reads `if body and body.ids` (data_view_core.py),
// so an empty list plus a status is the status form.
func (c *Client) ReprocessBadRows(accessToken string, datasetID int, ids []int, status *api.QuarantineStatus) (*BulkResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []int{}
	}
	params := &api.ReprocessBadRowsForDatasetViewParams{QuarantineStatus: status}
	var resp *api.ReprocessBadRowsForDatasetViewResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.ReprocessThirdPartyBadRowsForDatasetView(
			context.Background(), c.organizationID, datasetID, (*api.ReprocessThirdPartyBadRowsForDatasetViewParams)(params),
			api.ReprocessBadRowsRequestSchema{Ids: ids})
		err = requestErr
		if err == nil {
			resp, err = api.ParseReprocessBadRowsForDatasetViewResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.ReprocessBadRowsForDatasetViewWithResponse(
			context.Background(), c.organizationID, datasetID, params,
			api.ReprocessBadRowsRequestSchema{Ids: ids})
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "reprocess quarantined rows failed")
	}
	return bulk(resp.StatusCode(), resp.Body, resp.JSON200, bulkActionCount, "reprocess quarantined rows failed")
}

// ReprocessAllBadRows reprocesses every quarantined row in the dataset. The
// route is the whole selector: no body, no status filter.
func (c *Client) ReprocessAllBadRows(accessToken string, datasetID int) (*BulkResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	var resp *api.ReprocessAllBadRowsForDatasetViewResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.ReprocessAllThirdPartyBadRowsForDatasetView(
			context.Background(), c.organizationID, datasetID, &api.ReprocessAllThirdPartyBadRowsForDatasetViewParams{})
		err = requestErr
		if err == nil {
			resp, err = api.ParseReprocessAllBadRowsForDatasetViewResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.ReprocessAllBadRowsForDatasetViewWithResponse(
			context.Background(), c.organizationID, datasetID, &api.ReprocessAllBadRowsForDatasetViewParams{})
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "reprocess all quarantined rows failed")
	}
	return bulk(resp.StatusCode(), resp.Body, resp.JSON200, bulkActionCount, "reprocess all quarantined rows failed")
}

// MoveRowsToQuarantine drives the --row-id form of BOTH
// `rows send-to-quarantine` (hold) and `rows reprocess-clean` (undecided).
//
// The value is ALWAYS put on the wire even though the schema defaults it to
// hold. MoveRowsToQuarantineRequestSchema.QuarantineHold is a *QuarantineHold
// with omitempty, so a nil pointer is OMITTED rather than serialized, and each
// of the two verbs has an intent of its own that the server default happens to
// agree with for only one of them. An explicit JSON null is not a legal value
// on this route at all — quarantine_hold is declared non-nullable with a
// default (atlas capi/capi/schemas/data_schema.py) — so the
// pointer-and-nil convention this method used for reprocess-clean is retired
// rather than preserved, and "undecided" is what the old null meant.
//
// All three values move the row into the quarantine table and queue it: the
// INSERT stamps awaiting_processing_ = TRUE whatever the hold is
// (quarantine.py declares the default and passes it). What the value
// decides is what the row carries out of the move —
// move_rows_to_quarantine_core passes QuarantineHold.column_value as
// keep_in_quarantine (data_view_core.py), which move_rows_to_quarantine
// writes into keep_in_quarantine_ (quarantine.py) — and therefore whether a
// row that comes back valid may leave. A released row leaves as soon as it
// has no issues; an undecided one defers to the table, because
// _should_hold_valid_row_in_quarantine keeps a NULL row in quarantine when it
// carries suspected_duplicate_, the dataset quarantines rows until approved,
// or its effective data-review policy requires review
// (atlas cmapper/cmapper/cleaner.py).
//
// comment rides along as the request's comment and comment_is_internal.
func (c *Client) MoveRowsToQuarantine(accessToken string, datasetID int, ids []int, hold api.QuarantineHold, comment MoveComment) (*QuarantineMoveResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	// Ids is a bare []int with no omitempty and atlas declares it required and
	// non-nullable, so a nil slice would reach the wire as "ids":null and 422.
	// Same guard, same reason, as ReprocessBadRows' ids.
	if ids == nil {
		ids = []int{}
	}
	body := api.MoveRowsToQuarantineRequestSchema{Ids: ids, QuarantineHold: &hold, Comment: comment.Text}
	if comment.Text != nil {
		body.CommentIsInternal = comment.IsInternal
	}
	var resp *api.MoveRowsToQuarantineViewResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.MoveThirdPartyRowsToQuarantineView(
			context.Background(), c.organizationID, datasetID,
			&api.MoveThirdPartyRowsToQuarantineViewParams{}, body)
		err = requestErr
		if err == nil {
			resp, err = api.ParseMoveRowsToQuarantineViewResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.MoveRowsToQuarantineViewWithResponse(
			context.Background(), c.organizationID, datasetID,
			&api.MoveRowsToQuarantineViewParams{}, body)
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "move rows to quarantine failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "move rows to quarantine failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("move rows to quarantine failed: unexpected empty response")
	}
	return quarantineMoveResult(resp.Body)
}

// MoveAllRowsToQuarantine moves EVERY live clean row of a dataset into
// quarantine, with hold as MoveRowsToQuarantine takes it: hold for
// `rows send-to-quarantine --all`, undecided for `rows reprocess-clean --all`.
// Like DeleteAllBadRows and ReprocessAllBadRows the route is the whole
// selector: no ids, no status filter.
//
// The value is always put on the wire, for MoveRowsToQuarantine's reason: the
// field is a *QuarantineHold with omitempty, and an omitted value takes the
// server's default, hold (move_all_rows_to_quarantine_core, atlas
// data_view_core.py).
//
// comment, when not nil, is sent as the request's comment.
func (c *Client) MoveAllRowsToQuarantine(accessToken string, datasetID int, hold api.QuarantineHold, comment *string) (*QuarantineMoveResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	var resp *api.MoveAllRowsToQuarantineViewResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.MoveAllThirdPartyRowsToQuarantineView(
			context.Background(), c.organizationID, datasetID, &api.MoveAllThirdPartyRowsToQuarantineViewParams{},
			api.MoveAllRowsToQuarantineViewJSONRequestBody{QuarantineHold: &hold, Comment: comment})
		err = requestErr
		if err == nil {
			resp, err = api.ParseMoveAllRowsToQuarantineViewResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.MoveAllRowsToQuarantineViewWithResponse(
			context.Background(), c.organizationID, datasetID, &api.MoveAllRowsToQuarantineViewParams{},
			api.MoveAllRowsToQuarantineViewJSONRequestBody{QuarantineHold: &hold, Comment: comment})
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "move all rows to quarantine failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "move all rows to quarantine failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("move all rows to quarantine failed: unexpected empty response")
	}
	return quarantineMoveResult(resp.Body)
}

// UpdateKeepInQuarantine drives `rows hold` (hold), `rows release` (release)
// and `rows reset-hold` (undecided) — all three values quarantine_hold takes.
// It is required and non-nullable here, so there is nothing to omit and no
// pointer to take (atlas capi/capi/schemas/data_schema.py).
//
// release and undecided both queue the rows they write for reprocessing —
// `triggers_reprocessing = value is not True`
// (quarantine.py _update_keep_in_quarantine_returning) — and hold queues
// nothing.
//
// When the table's effective data-review policy requires review or its
// quarantine_rows_until_approved is on, a release
// records the caller's approvals instead of releasing rows outright
// (update_keep_in_quarantine_core, data_view_core.py), and only that answer
// carries SkippedRows, SkippedRowIds, ReleasedRowIds and Approvals.
func (c *Client) UpdateKeepInQuarantine(accessToken string, datasetID int, hold api.QuarantineHold, ids []int, status *api.QuarantineStatus) (*KeepInQuarantineResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.UpdateKeepInQuarantineRequestSchema{QuarantineHold: hold, QuarantineStatus: status}
	if len(ids) > 0 {
		body.Ids = &ids
	}
	var resp *api.UpdateKeepInQuarantineViewResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.UpdateThirdPartyKeepInQuarantineView(
			context.Background(), c.organizationID, datasetID,
			&api.UpdateThirdPartyKeepInQuarantineViewParams{}, body)
		err = requestErr
		if err == nil {
			resp, err = api.ParseUpdateKeepInQuarantineViewResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.UpdateKeepInQuarantineViewWithResponse(
			context.Background(), c.organizationID, datasetID,
			&api.UpdateKeepInQuarantineViewParams{}, body)
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "update quarantine hold failed")
	}
	return keepInQuarantineResult(resp.StatusCode(), resp.Body, resp.JSON200, "update quarantine hold failed")
}

// QuarantineCountScope narrows GetQuarantineCountByStatus to one cloud source
// (DataSourceModelID) or one file (StoredItemID). The zero value counts the
// whole table.
type QuarantineCountScope struct {
	DataSourceModelID *int
	StoredItemID      *int
}

// GetQuarantineCountByStatus returns the count of live quarantine rows per
// status bucket in scope (atlas quarantine.py
// get_quarantine_count_by_status, which appends `is_deleted_ = FALSE`).
// on_hold, approved, waiting_for_approval, held_for_approval and
// passed_validation use the same predicates as the bulk filters;
// failed_validation counts
// `passes_validation_ IS FALSE`, while the bulk filter for it is
// `IS NOT TRUE`. See sizedStatusPhrase in
// internal/commands/rows/confirm.go for what that means for a prompt.
func (c *Client) GetQuarantineCountByStatus(accessToken string, datasetID int, scope QuarantineCountScope) (*QuarantineCounts, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	var resp *api.GetQuarantineCountByStatusResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.GetThirdPartyQuarantineCountByStatus(
			context.Background(), c.organizationID, datasetID, &api.GetThirdPartyQuarantineCountByStatusParams{
				DataSourceModelID: scope.DataSourceModelID,
				StoredItemID:      scope.StoredItemID,
			})
		err = requestErr
		if err == nil {
			resp, err = api.ParseGetQuarantineCountByStatusResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.GetQuarantineCountByStatusWithResponse(
			context.Background(), c.organizationID, datasetID, &api.GetQuarantineCountByStatusParams{
				DataSourceModelID: scope.DataSourceModelID,
				StoredItemID:      scope.StoredItemID,
			})
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get quarantine counts failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get quarantine counts failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("get quarantine counts failed: unexpected empty response")
	}
	return resp.JSON200, nil
}
