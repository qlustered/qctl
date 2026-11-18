package datasets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// SearchMatchMode is the four-value vocabulary `rows search-replace` sends.
// Aliased rather than restated so a spec change reaches the command layer as a
// compile error instead of a silently stale list.
type SearchMatchMode = api.SearchMatchMode

// ColumnUniqueness is the verdict `rows check-unique` prints. Aliased the way
// QuarantineCounts is: the generated schema is already the qctl-side shape.
//
// Reason is a POINTER and nil is the success case — the route sends
// `"reason": null` when the columns are unique and one of contains_nulls /
// has_duplicates when they are not (atlas data_view_core.py). Do not
// read IsUnique off Reason or the other way round; both are always present.
type (
	ColumnUniqueness = api.CheckColumnUniquenessResponseSchema
	UniquenessReason = api.CheckColumnUniquenessResponseSchemaReason
	DuplicateExample = api.DuplicateExample
)

// SearchReplaceRequest is one search-and-replace call.
//
// FieldName and DataSourceModelID are pointers because nil is a MEANING on
// this route, not an omission: a nil field searches every non-deleted schema
// field and a nil source searches every source (atlas
// capi/capi/schemas/data_schema.py). Both reach the wire as JSON null,
// which is what the backend's defaults are.
type SearchReplaceRequest struct {
	FieldName         *string
	DataSourceModelID *int
	SearchTerm        string
	ReplaceTerm       string
	// MatchMode empty means "take the server default"
	// (contains_case_insensitive). An empty string is not a legal enum value,
	// so it is omitted rather than sent.
	MatchMode SearchMatchMode
	DryRun    bool
}

// SearchReplaceResult is the route's five-field answer.
//
// The counts are plain ints and FieldsAffected a plain slice even though the
// generated schema makes all four optional pointers. The route is declared
// `response_model_exclude_unset=False` over a pydantic model that defaults
// every field (atlas capi/capi/views/data_view.py and
// capi/capi/schemas/data_schema.py), so all five are always on the
// wire and a nil pointer here would be a decode artefact, never a server
// distinction worth preserving.
//
// MatchedRows is summed PER FIELD, not per row: with no field name a row that
// matches in two fields counts twice (row_overwrite.py adds
// len(rows) once per field in the loop). Under DryRun only MatchedRows is
// computed — the loop `continue`s before any row is examined — so
// ReplacedRows, SkippedRows and FieldsAffected are zero-valued on a preview
// and say nothing about what a real run would skip.
type SearchReplaceResult struct {
	FieldsAffected []string `json:"fields_affected"`
	MatchedRows    int      `json:"matched_rows"`
	ReplacedRows   int      `json:"replaced_rows"`
	SkippedRows    int      `json:"skipped_rows"`
}

// SearchAndReplace rewrites matching text in the dataset's QUARANTINE rows.
//
// Quarantine only, and only rows no job has claimed: the SELECT is
// `FROM {bad_rows_table} WHERE {match} AND ingest_job_id_ IS NULL AND
// awaiting_processing_ IS NOT TRUE AND is_deleted_ IS NOT TRUE`
// (atlas cettings/.../data_controller/row_overwrite.py). There is no
// clean-table form of this route.
func (c *Client) SearchAndReplace(accessToken string, datasetID int, req SearchReplaceRequest) (*SearchReplaceResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.SearchAndReplaceRequestSchema{
		SearchTerm:        req.SearchTerm,
		ReplaceTerm:       req.ReplaceTerm,
		FieldName:         req.FieldName,
		DataSourceModelID: req.DataSourceModelID,
		DryRun:            &req.DryRun,
	}
	if req.MatchMode != "" {
		body.MatchMode = &req.MatchMode
	}
	var resp *api.SearchAndReplaceResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.SearchAndReplaceThirdParty(
			context.Background(), c.organizationID, datasetID,
			&api.SearchAndReplaceThirdPartyParams{}, body)
		err = requestErr
		if err == nil {
			resp, err = api.ParseSearchAndReplaceResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.SearchAndReplaceWithResponse(
			context.Background(), c.organizationID, datasetID,
			&api.SearchAndReplaceParams{}, body)
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "search and replace failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "search and replace failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("search and replace failed: unexpected empty response")
	}
	return searchReplaceResult(resp.JSON200), nil
}

func searchReplaceResult(schema *api.SearchAndReplaceResponseSchema) *SearchReplaceResult {
	result := &SearchReplaceResult{FieldsAffected: []string{}}
	if schema.MatchedRows != nil {
		result.MatchedRows = *schema.MatchedRows
	}
	if schema.ReplacedRows != nil {
		result.ReplacedRows = *schema.ReplacedRows
	}
	if schema.SkippedRows != nil {
		result.SkippedRows = *schema.SkippedRows
	}
	if schema.FieldsAffected != nil {
		result.FieldsAffected = *schema.FieldsAffected
	}
	return result
}

// setCellBody is the modify-clean-cell body qctl sends, hand-built rather than
// api.ModifyCleanCellRequestSchema.
//
// The generated struct declares CurrentValue as `*string json:"current_value"`
// with no omitempty, so a nil pointer would put `"current_value": null` on the
// wire. The backend ignores the field and reads the current value from the
// stored rows (atlas capi/capi/schemas/data_schema.py
// ModifyCleanCellRequestSchema), so it is omitted.
//
// DataSourceModelID and MapValueOnce are omitted for a plainer reason: neither
// changes anything for overwrite_value: the composite path resolves its own
// source and the non-composite path scopes external callers by their grants.
type setCellBody struct {
	FieldName string              `json:"field_name"`
	NewValue  string              `json:"new_value"`
	Action    api.CleanCellAction `json:"action"`
	IDs       []int               `json:"ids"`
}

// SetCell overwrites one field on the listed rows with overwrite_value.
//
// "clean cell" in the route's name means a cell carrying no alert or warning,
// NOT the clean table. On an ordinary table the ids are QUARANTINE row ids:
// overwrite_value reaches overwrite_bad_rows_any_value, whose statement is
// `FROM {bad_rows_table} WHERE id in %s AND ingest_job_id_ IS null AND
// awaiting_processing_ IS NOT TRUE` (atlas row_overwrite.py). A clean
// table's id matches nothing there and the route still answers
// {"result":"ok"} — so on that branch a caller cannot learn from the response
// that it addressed the wrong population.
//
// A COMPOSITE table takes the other branch and behaves differently enough to
// matter: each id becomes a durable CellOverride rather than a data-plane
// write, and the ids are checked first. validate_live_row_ids probes the clean
// AND the bad table (data_retrieval.py), so an id live in either one
// is accepted, and an id live in neither is a 404 naming the ids. That branch
// also 404s a table with no active composite source. Its 401 guard is NOT
// reachable from here and is not promised anywhere qctl speaks: the view
// always passes user_id from a Depends(require_dataset_permission(...))
// parameter, so the controller's `if self.user_id is None` cannot fire through
// this route. So "silently does nothing" is the
// ordinary-table behaviour only; do not promise it for every table.
//
// There is no count to return and none is invented: the view's answer is the
// literal dict {'result': 'ok'} (data_view_core.py), so a number here
// could only be a guess at what the write touched.
func (c *Client) SetCell(accessToken string, datasetID int, rowIDs []int, fieldName, newValue string) error {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	// A nil slice marshals to "ids":null, which pydantic rejects with a
	// type error before modify_clean_cell_core's own "No ids are passed"
	// check can produce the better message. Same guard, same reason, as
	// ReprocessBadRows' ids.
	if rowIDs == nil {
		rowIDs = []int{}
	}
	raw, err := json.Marshal(setCellBody{
		IDs:       rowIDs,
		FieldName: fieldName,
		NewValue:  newValue,
		Action:    api.CleanCellActionOverwriteValue,
	})
	if err != nil {
		return fmt.Errorf("failed to encode set cell request: %w", err)
	}
	var resp *api.ModifyCleanCellResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.ModifyThirdPartyCleanCellWithBody(
			context.Background(), c.organizationID, datasetID,
			&api.ModifyThirdPartyCleanCellParams{}, "application/json", bytes.NewReader(raw))
		err = requestErr
		if err == nil {
			resp, err = api.ParseModifyCleanCellResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.ModifyCleanCellWithBodyWithResponse(
			context.Background(), c.organizationID, datasetID,
			&api.ModifyCleanCellParams{}, "application/json", bytes.NewReader(raw))
	}
	if err != nil {
		return wrapAPIError(apiClient, err, "set cell failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return handleError(resp.StatusCode(), resp.Body, "set cell failed")
	}
	return nil
}

// CheckColumnUniqueness asks whether the columns are a unique key over the
// dataset's CLEAN rows — the one route of these three that reads the clean
// table (atlas data_view_core.py) and the one that needs only
// DATASET_DATA_READ.
//
// A NULL in any requested column short-circuits the answer: null_count > 0
// returns reason=contains_nulls with is_unique false and an EMPTY
// duplicate_examples, because the duplicate-group query never runs. An empty
// DuplicateExamples therefore does not mean "no duplicates".
//
// The query runs under `SET LOCAL statement_timeout = 30000`, and the timeout
// surfaces as a 504 — exit 1, a retry signal, not bad usage.
//
// A number in a duplicate example's Values decodes as jsonnum.Unmarshal
// decodes it.
func (c *Client) CheckColumnUniqueness(accessToken string, datasetID int, columns []string) (*ColumnUniqueness, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	// Same nil-to-empty guard as SetCell's ids: the field validator's
	// "columns must be a non-empty list" is a better 422 than the type error
	// a JSON null earns (atlas capi/capi/schemas/data_schema.py).
	if columns == nil {
		columns = []string{}
	}
	var resp *api.CheckColumnUniquenessResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.CheckThirdPartyColumnUniqueness(
			context.Background(), c.organizationID, datasetID,
			&api.CheckThirdPartyColumnUniquenessParams{},
			api.CheckColumnUniquenessRequestSchema{Columns: columns})
		err = requestErr
		if err == nil {
			resp, err = api.ParseCheckColumnUniquenessResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.CheckColumnUniquenessWithResponse(
			context.Background(), c.organizationID, datasetID,
			&api.CheckColumnUniquenessParams{},
			api.CheckColumnUniquenessRequestSchema{Columns: columns})
	}
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "check column uniqueness failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "check column uniqueness failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("check column uniqueness failed: unexpected empty response")
	}
	var res ColumnUniqueness
	if err := jsonnum.Unmarshal(resp.Body, &res); err != nil {
		return nil, fmt.Errorf("failed to decode check column uniqueness response: %w", err)
	}
	return &res, nil
}
