package data_reviews //nolint:revive // Mirrors the backend's data-review arc, like rule_reviews.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// Type aliases to the generated schemas (frozen cross-ticket contract — the
// command packages import these names, never the api.* spellings, so a
// regenerated client that renames a schema is one edit here).
type (
	Review          = api.DataReviewResponseSchema             // detail / assign / unassign / close / submit element
	Change          = api.DataReviewChangeSchema               // one latest-per-field gate entry, with its ledger decision
	Decision        = api.DataReviewDecisionResponseSchema     // a reviewer's whole-submission verdict
	Group           = api.DataReviewGroupSchema                // one submission's batch-card aggregates
	GroupPage       = api.DataReviewGroupListResponseSchema    // {Groups, Total}
	TopChange       = api.TopChangeSchema                      // a change ranked by leverage
	RowOverlay      = api.RowReviewOverlaySchema               // the grid's slim per-row overlay
	RowOverlayPage  = api.BulkRowReviewsResponseSchema         // {Reviews}: the by-rows wire response
	OverlayChange   = api.RowReviewOverlayChangeSchema         // one change on an overlay, with its ledger decision
	SignoffProgress = api.SignoffProgressSchema                // the burndown, team and my_* halves
	StateCounts     = api.ReviewStateCountResponseSchema       // the Review tab pills
	BatchResult     = api.BatchDecisionResponseSchema          // {Processed, Skipped}
	BatchSkipped    = api.BatchSkippedItemSchema               // one review a bulk decision left untouched, with why
	ActionResult    = api.RecordActionResponseSchema           // one ledger write's full recompute set
	RerequestResult = api.DataRerequestReviewersResponseSchema // who was actually re-notified
	Comment         = api.CommentResponseSchema                // the unified comment model

	GroupAssignment = api.DataReviewGroupAssignmentResponseSchema // a request's assignees after a group assign
	GroupRerequest  = api.DataReviewGroupRerequestResponseSchema  // who a group re-request notified
	GroupCancel     = api.DataReviewGroupCancelResponseSchema     // the reviews a cancel removed and closed
)

// byRowsChunkSize is the backend's cap on one by-rows request
// (BulkRowReviewsRequestSchema.row_ids, max_length=100). ByRows chunks at it
// rather than letting a caller's page size decide whether the call 422s.
const byRowsChunkSize = 100

// jsonContentType is the content type for the two batch routes, which go
// out through the generated *WithBody* entry points so the hand-written wire
// bodies below reach the wire verbatim.
const jsonContentType = "application/json"

// Client handles HTTP requests for data-review operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new data reviews client (standard pattern). timeout is
// left at zero deliberately: this package has no deadline of its own, so
// client.ResolveTimeout gives --request-timeout, then client.DefaultTimeout.
func NewClient(baseURL string, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
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

// parseUUID validates one of the arc's ids. Every id here — review, group,
// comment — is a full UUID with no short-prefix resolution, so a malformed one
// is something the user typed wrong: exit 2, before any request goes out.
func parseUUID(kind, value string) (openapi_types.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return openapi_types.UUID{}, qerrors.NewBadUsagef("invalid %s %q: expected a full UUID", kind, value)
	}
	return id, nil
}

func parseReviewID(reviewID string) (openapi_types.UUID, error) {
	return parseUUID("data review id", reviewID)
}

// GetOptions selects the detail GET's optional expansions. Both are opt-in
// because the plain metadata GET opens no data-plane controller server-side;
// include=changes does (and implies release_block). A caller without an active
// UserToDataSet still gets 200 with changes: [] — never a 403.
type GetOptions struct {
	IncludeChanges bool // ?include=changes  — per-field changes + release_block_reason
	ChangesSinceMe bool // ?changes_since=me — edits newer than my latest approval
}

// Get fetches one row review. GET /data-reviews/{id}.
func (c *Client) Get(accessToken, reviewID string, opts GetOptions) (*Review, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.GetDataReviewParams{}
	if opts.IncludeChanges {
		include := "changes"
		params.Include = &include
	}
	if opts.ChangesSinceMe {
		since := "me"
		params.ChangesSince = &since
	}
	resp, err := apiClient.API.GetDataReviewWithResponse(context.Background(), c.organizationID, reviewUUID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get data review failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get data review failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	review, err := decodeReview(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to decode data review response: %w", err)
	}
	return review, nil
}

// decodeReview decodes body with jsonnum.Unmarshal, including the values each
// changes_since entry holds in AdditionalProperties.
func decodeReview(body []byte) (*Review, error) {
	var review Review
	if err := jsonnum.Unmarshal(body, &review); err != nil {
		return nil, err
	}
	if review.ChangesSince == nil {
		return &review, nil
	}
	var raw struct {
		ChangesSince map[string][]map[string]interface{} `json:"changes_since"`
	}
	if err := jsonnum.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	for field, entries := range *review.ChangesSince {
		for i, entry := range entries {
			for key := range entry.AdditionalProperties {
				entry.AdditionalProperties[key] = raw.ChangesSince[field][i][key]
			}
		}
	}
	return &review, nil
}

// ListDecisions lists the review's whole-submission verdicts (one per
// reviewer). The detail GET does NOT carry these — they are a separate route.
func (c *Client) ListDecisions(accessToken, reviewID string) ([]Decision, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ListDataReviewDecisionsWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.ListDataReviewDecisionsParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list data review decisions failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list data review decisions failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return *resp.JSON200, nil
}

// GroupsOptions filters the submission-group list. DatasetID is required by
// the backend; the list is ordered created_at DESC, id DESC and takes no
// ordering parameters.
type GroupsOptions struct {
	DatasetID     int
	IncludeClosed bool // false (the server default) lists only groups with an active member
	Limit         int  // 0 = omit; server default 50, range 1..200
	Offset        int  // 0 = omit; server default 0
}

// ListGroups lists one dataset's submission groups with their aggregates.
func (c *Client) ListGroups(accessToken string, opts GroupsOptions) (*GroupPage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListDataReviewGroupsParams{
		DatasetID:     opts.DatasetID,
		IncludeClosed: &opts.IncludeClosed,
	}
	// Zero is the caller saying nothing, not a page of zero rows: the server's
	// own defaults (50 / 0) are the right answer and its range guards would
	// reject limit=0 outright.
	if opts.Limit > 0 {
		params.Limit = &opts.Limit
	}
	if opts.Offset > 0 {
		params.Offset = &opts.Offset
	}
	resp, err := apiClient.API.ListDataReviewGroupsWithResponse(context.Background(), c.organizationID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list data review groups failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list data review groups failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// TopChangesOptions selects a dataset's ranked changes.
type TopChangesOptions struct {
	DatasetID     int
	ReviewGroupID *openapi_types.UUID
	State         api.DecisionState // undecided|unresolved|decided|accepted|rejected|changes_requested|all
	Limit         int               // the server CLAMPS this to 1..20 rather than rejecting it
	Offset        int
}

// TopChanges lists a dataset's submitted changes ranked by leverage.
func (c *Client) TopChanges(accessToken string, opts TopChangesOptions) ([]TopChange, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	state := opts.State
	limit := opts.Limit
	params := &api.DataReviewTopChangesParams{
		DatasetID:     opts.DatasetID,
		DecisionState: &state,
		Limit:         &limit,
		ReviewGroupID: opts.ReviewGroupID,
	}
	if opts.Offset > 0 {
		params.Offset = &opts.Offset
	}
	resp, err := apiClient.API.DataReviewTopChangesWithResponse(context.Background(), c.organizationID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list top changes failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list top changes failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return *resp.JSON200, nil
}

// TopChangesCount returns the UNCAPPED size of the pool TopChanges ranks. It
// is an upper bound: stage-1 only, so candidates the list drops as superseded
// are still counted. count == 0 does imply an empty list; the converse does not
// hold.
func (c *Client) TopChangesCount(accessToken string, opts TopChangesOptions) (int, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return 0, err
	}
	params := &api.DataReviewTopChangesCountParams{
		DatasetID: opts.DatasetID, DecisionState: opts.State, ReviewGroupID: opts.ReviewGroupID,
	}
	resp, err := apiClient.API.DataReviewTopChangesCountWithResponse(context.Background(), c.organizationID, params)
	if err != nil {
		return 0, wrapAPIError(apiClient, err, "count top changes failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return 0, handleError(resp.StatusCode(), resp.Body, "count top changes failed")
	}
	if resp.JSON200 == nil {
		return 0, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200.Count, nil
}

// ByRows fetches the active-review overlay for a page of grid rows, chunked at
// the backend's 100-id cap and concatenated in input order. Only ACTIVE
// reviews (open|changes_requested) come back, at most one per row, so a row
// with no active review is simply absent. An empty input is not a request: the
// backend requires min_length=1 and a caller with nothing to overlay has
// nothing to ask about.
func (c *Client) ByRows(accessToken string, datasetID int, rowIDs []int) ([]RowOverlay, error) {
	overlays := []RowOverlay{}
	if len(rowIDs) == 0 {
		return overlays, nil
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(rowIDs); start += byRowsChunkSize {
		end := start + byRowsChunkSize
		if end > len(rowIDs) {
			end = len(rowIDs)
		}
		body := api.DataReviewsByRowsJSONRequestBody{RowIds: rowIDs[start:end]}
		resp, err := apiClient.API.DataReviewsByRowsWithResponse(
			context.Background(), c.organizationID, datasetID, &api.DataReviewsByRowsParams{}, body)
		if err != nil {
			return nil, wrapAPIError(apiClient, err, "get row reviews failed")
		}
		if resp.StatusCode() != http.StatusOK {
			return nil, handleError(resp.StatusCode(), resp.Body, "get row reviews failed")
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("unexpected empty response")
		}
		overlays = append(overlays, resp.JSON200.Reviews...)
	}
	return overlays, nil
}

// SignoffProgress fetches the dataset's reviewer burndown. my_total == 0 means
// the caller has no stake in this dataset — not that there is nothing to do.
func (c *Client) SignoffProgress(accessToken string, datasetID int) (*SignoffProgress, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.DataReviewSignoffProgressWithResponse(
		context.Background(), c.organizationID, datasetID, &api.DataReviewSignoffProgressParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get signoff progress failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get signoff progress failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// CountByState fetches the per-row review-state counts over the dataset's
// non-closed-review rows. groupID "" scopes to the whole dataset (the param is
// omitted); an unknown group is all zeros, not a 404. This route is gated on
// DATASET_DATA_READ rather than DATA_REVIEW_LIST — it lives on the data view.
func (c *Client) CountByState(accessToken string, datasetID int, groupID string) (*StateCounts, error) {
	params := &api.GetReviewCountByStateParams{}
	if groupID != "" {
		groupUUID, err := parseGroupID(groupID)
		if err != nil {
			return nil, err
		}
		params.ReviewGroupID = &groupUUID
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetReviewCountByStateWithResponse(
		context.Background(), c.organizationID, datasetID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "count reviews by state failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "count reviews by state failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// SubmitOptions are the caller-supplied fields for a new submission. RowIds is
// 1..500; an existing active review for a row is REUSED rather than duplicated,
// and re-snapshots required_approvals from the effective policy.
type SubmitOptions struct {
	DatasetID   int
	RowIDs      []int
	Title       string
	Description string               // "" = null
	ReviewerIDs []openapi_types.UUID // nil/empty = null
}

// Submit requests review for a batch of rows. The response is a BARE ARRAY of
// reviews; the new group's id appears only in each element's review_groups[]
// (newest by created_at), never at the top level.
func (c *Client) Submit(accessToken string, opts SubmitOptions) ([]Review, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.SubmitDataReviewBatchJSONRequestBody{
		DatasetID: opts.DatasetID,
		RowIds:    opts.RowIDs,
		Title:     opts.Title,
	}
	if opts.Description != "" {
		body.Description = &opts.Description
	}
	if len(opts.ReviewerIDs) > 0 {
		body.AssignedReviewerIds = &opts.ReviewerIDs
	}
	resp, err := apiClient.API.SubmitDataReviewBatchWithResponse(
		context.Background(), c.organizationID, &api.SubmitDataReviewBatchParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "submit rows for review failed")
	}
	if resp.StatusCode() != http.StatusCreated {
		return nil, handleError(resp.StatusCode(), resp.Body, "submit rows for review failed")
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return *resp.JSON201, nil
}

// BatchSelector names the reviews a bulk whole-row decision applies to.
// EXACTLY one of the two is populated. The backend's own XOR guard
// (_execute_bulk_decision, data_review_view.py) is a 422 without an error_code,
// but qctl never lets a request get that far: both or neither is exit 2 here,
// so that 422 is unreachable from this client.
type BatchSelector struct {
	ReviewIDs []string // explicit data review UUIDs, 1..500, deduped server-side
	GroupID   string   // one ReviewGroup UUID; expands server-side to its OPEN members, capped at 500
}

// batchSelectorWireBody is withdraw's whole body and the selector half of
// approve's. It is hand-written rather than api.BatchSelectorSchema
// because the generated tags carry no omitempty: a nil pointer there
// serialises as `"review_group_id": null`, so every request would name BOTH
// selector keys. Per CLAUDE.md client.gen.go is never hand-edited, so the
// difference lives here.
type batchSelectorWireBody struct {
	DataReviewIDs *[]openapi_types.UUID `json:"data_review_ids,omitempty"`
	ReviewGroupID *openapi_types.UUID   `json:"review_group_id,omitempty"`
}

// batchDecisionWireBody adds approve's one comment applied to every row
// in the batch. `comment` keeps no omitempty on purpose: absent and null read
// the same to the backend's `str | None = None`, and a present key is what
// separates this body from withdraw's, which never carries one at all.
type batchDecisionWireBody struct {
	batchSelectorWireBody
	Comment *string `json:"comment"`
}

// wire validates the selector and renders it. A list is never sent empty: an
// empty data_review_ids is a 422 server-side, and it is exit 2 here first.
func (s BatchSelector) wire() (batchSelectorWireBody, error) {
	hasIDs, hasGroup := len(s.ReviewIDs) > 0, s.GroupID != ""
	switch {
	case hasIDs && hasGroup:
		return batchSelectorWireBody{}, qerrors.NewBadUsage(
			"a bulk decision takes either data review ids or a review group, not both")
	case !hasIDs && !hasGroup:
		return batchSelectorWireBody{}, qerrors.NewBadUsage(
			"a bulk decision needs at least one data review id, or a review group")
	case hasGroup:
		groupUUID, err := parseGroupID(s.GroupID)
		if err != nil {
			return batchSelectorWireBody{}, err
		}
		return batchSelectorWireBody{ReviewGroupID: &groupUUID}, nil
	default:
		ids := make([]openapi_types.UUID, 0, len(s.ReviewIDs))
		for _, raw := range s.ReviewIDs {
			id, err := parseReviewID(raw)
			if err != nil {
				return batchSelectorWireBody{}, err
			}
			ids = append(ids, id)
		}
		return batchSelectorWireBody{DataReviewIDs: &ids}, nil
	}
}

// bulkVerb names one of the two whole-row batch routes. It is a type rather
// than a bare string so batchDecision's dispatch has no fall-through case: a
// third verb added later fails loudly instead of quietly becoming withdraw.
type bulkVerb string

const (
	verbApprove  bulkVerb = "approve"
	verbWithdraw bulkVerb = "withdraw"
)

// Approve records the caller's whole-row approval across the selection.
// Approving your own submission is processed but never counts toward the
// meter; a closed id lands in Skipped with REVIEW_NOT_OPEN rather than
// failing the request.
func (c *Client) Approve(accessToken string, selector BatchSelector, comment string) (*BatchResult, error) {
	return c.batchDecision(accessToken, selector, comment, verbApprove)
}

// Withdraw deletes only the CALLER's own whole-row decision across the
// selection and never re-quarantines a released row. It carries no comment.
func (c *Client) Withdraw(accessToken string, selector BatchSelector) (*BatchResult, error) {
	return c.batchDecision(accessToken, selector, "", verbWithdraw)
}

func (c *Client) batchDecision(accessToken string, selector BatchSelector, comment string, verb bulkVerb) (*BatchResult, error) {
	wire, err := selector.wire()
	if err != nil {
		return nil, err
	}
	var payload []byte
	if verb == verbWithdraw {
		// Withdraw's schema is the selector alone. Sending a comment key it
		// does not model would be noise on a route that cannot use it.
		payload, err = json.Marshal(wire)
	} else {
		body := batchDecisionWireBody{batchSelectorWireBody: wire}
		if comment != "" {
			body.Comment = &comment
		}
		payload, err = json.Marshal(body)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to encode %s request: %w", verb, err)
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	contextMsg := string(verb) + " data reviews failed"
	var (
		statusCode int
		rawBody    []byte
		result     *BatchResult
	)
	ctx := context.Background()
	switch verb {
	case verbApprove:
		resp, err := apiClient.API.ApproveDataReviewsWithBodyWithResponse(
			ctx, c.organizationID, &api.ApproveDataReviewsParams{}, jsonContentType, bytes.NewReader(payload))
		if err != nil {
			return nil, wrapAPIError(apiClient, err, contextMsg)
		}
		statusCode, rawBody, result = resp.StatusCode(), resp.Body, resp.JSON200
	case verbWithdraw:
		resp, err := apiClient.API.WithdrawDataReviewsWithBodyWithResponse(
			ctx, c.organizationID, &api.WithdrawDataReviewsParams{}, jsonContentType, bytes.NewReader(payload))
		if err != nil {
			return nil, wrapAPIError(apiClient, err, contextMsg)
		}
		statusCode, rawBody, result = resp.StatusCode(), resp.Body, resp.JSON200
	default:
		return nil, fmt.Errorf("unknown bulk decision verb %q", verb)
	}
	if statusCode != http.StatusOK {
		return nil, handleError(statusCode, rawBody, contextMsg)
	}
	if result == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return result, nil
}

// ActionOptions are the caller-supplied fields for one per-cell ledger write.
type ActionOptions struct {
	Decision       api.RecordActionSchemaDecision
	Comment        string
	SuggestedValue string
}

// RecordAction records or withdraws a cell decision. Comment and SuggestedValue
// are sent as strings, including empty values.
func (c *Client) RecordAction(accessToken, reviewID string, historyID int, opts ActionOptions) (*ActionResult, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	comment, suggested := opts.Comment, opts.SuggestedValue
	body := api.RecordDataReviewActionJSONRequestBody{
		Decision:       opts.Decision,
		Comment:        &comment,
		SuggestedValue: &suggested,
	}
	resp, err := apiClient.API.RecordDataReviewActionWithResponse(
		context.Background(), c.organizationID, reviewUUID, historyID,
		&api.RecordDataReviewActionParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "record data review action failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "record data review action failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Assign adds reviewers to an active review. It is additive and idempotent,
// has no designated-reviewer gate, and re-notifies reviewers already on the
// review. A non-active review is 422 REVIEW_NOT_OPEN.
func (c *Client) Assign(accessToken, reviewID string, reviewerIDs []openapi_types.UUID) (*Review, error) {
	return c.assignOrUnassign(accessToken, reviewID, reviewerIDs, true)
}

// Unassign removes reviewers. There is no last-reviewer guard, and it RETAINS
// their per-cell verdicts — those then surface in the detail's
// unassigned_vets[], still gating the row.
func (c *Client) Unassign(accessToken, reviewID string, reviewerIDs []openapi_types.UUID) (*Review, error) {
	return c.assignOrUnassign(accessToken, reviewID, reviewerIDs, false)
}

func (c *Client) assignOrUnassign(accessToken, reviewID string, reviewerIDs []openapi_types.UUID, assign bool) (*Review, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	// reviewer_ids is a REQUIRED list on this schema, so a nil slice is sent
	// as [] rather than null: null fails pydantic before the route runs, which
	// would report a client bug as a validation error about the user's input.
	if reviewerIDs == nil {
		reviewerIDs = []openapi_types.UUID{}
	}
	var (
		statusCode int
		rawBody    []byte
		payload    *Review
		contextMsg string
	)
	ctx := context.Background()
	if assign {
		contextMsg = "assign reviewers failed"
		resp, err := apiClient.API.AssignDataReviewReviewersWithResponse(
			ctx, c.organizationID, reviewUUID, &api.AssignDataReviewReviewersParams{},
			api.AssignDataReviewReviewersJSONRequestBody{ReviewerIds: reviewerIDs})
		if err != nil {
			return nil, wrapAPIError(apiClient, err, contextMsg)
		}
		statusCode, rawBody, payload = resp.StatusCode(), resp.Body, resp.JSON200
	} else {
		contextMsg = "unassign reviewers failed"
		resp, err := apiClient.API.UnassignDataReviewReviewersWithResponse(
			ctx, c.organizationID, reviewUUID, &api.UnassignDataReviewReviewersParams{},
			api.UnassignDataReviewReviewersJSONRequestBody{ReviewerIds: reviewerIDs})
		if err != nil {
			return nil, wrapAPIError(apiClient, err, contextMsg)
		}
		statusCode, rawBody, payload = resp.StatusCode(), resp.Body, resp.JSON200
	}
	if statusCode != http.StatusOK {
		return nil, handleError(statusCode, rawBody, contextMsg)
	}
	if payload == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return payload, nil
}

// Rerequest re-notifies assigned reviewers. Empty/nil reviewerIDs sends {} and
// lets the server pick the assignees with no decision yet; the requester is
// dropped either way. The response reports who was ACTUALLY notified, and an
// empty set is a legitimate no-op. A terminal review is 409 with error_code
// review_not_active.
func (c *Client) Rerequest(accessToken, reviewID string, reviewerIDs []openapi_types.UUID) (*RerequestResult, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.RerequestDataReviewReviewersJSONRequestBody{}
	if len(reviewerIDs) > 0 {
		body.ReviewerIds = &reviewerIDs
	}
	resp, err := apiClient.API.RerequestDataReviewReviewersWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.RerequestDataReviewReviewersParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "re-request reviewers failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "re-request reviewers failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Close closes an active review.
func (c *Client) Close(accessToken, reviewID string) (*Review, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CloseDataReviewWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.CloseDataReviewParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "close data review failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "close data review failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

func parseGroupID(groupID string) (openapi_types.UUID, error) {
	return parseUUID("review group id", groupID)
}

// AssignGroup adds reviewers to a request (review group) with at least one
// open or changes_requested review, notifying only those not yet assigned.
// The response lists every assignee after the call. A request with no such
// review is 422 REVIEW_NOT_OPEN.
func (c *Client) AssignGroup(accessToken, groupID string, reviewerIDs []openapi_types.UUID) (*GroupAssignment, error) {
	groupUUID, err := parseGroupID(groupID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	// reviewer_ids is a required list: send [] rather than null.
	if reviewerIDs == nil {
		reviewerIDs = []openapi_types.UUID{}
	}
	resp, err := apiClient.API.AssignDataReviewGroupReviewersWithResponse(
		context.Background(), c.organizationID, groupUUID, &api.AssignDataReviewGroupReviewersParams{},
		api.AssignDataReviewGroupReviewersJSONRequestBody{ReviewerIds: reviewerIDs})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "assign request reviewers failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "assign request reviewers failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// RerequestGroup re-notifies a request's assignees who have not decided every
// open review in it. An empty result is a legitimate no-op; a request with no
// open review is 422 REVIEW_NOT_OPEN.
func (c *Client) RerequestGroup(accessToken, groupID string) (*GroupRerequest, error) {
	groupUUID, err := parseGroupID(groupID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.RerequestDataReviewGroupReviewersWithResponse(
		context.Background(), c.organizationID, groupUUID, &api.RerequestDataReviewGroupReviewersParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "re-request request reviewers failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "re-request request reviewers failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// CancelGroup cancels a request: it removes every review from the request and
// closes those that are open and in no other request. Repeating it answers
// with two empty lists.
func (c *Client) CancelGroup(accessToken, groupID string) (*GroupCancel, error) {
	groupUUID, err := parseGroupID(groupID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CancelDataReviewGroupWithResponse(
		context.Background(), c.organizationID, groupUUID, &api.CancelDataReviewGroupParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "cancel request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "cancel request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// CommentOptions are the caller-supplied fields for a new review comment.
type CommentOptions struct {
	Body     string // 1..10000 characters
	ParentID string // "" = top-level; else the parent comment UUID (max depth 3)
}

// AddComment posts a comment to a review's thread, creating the thread lazily.
// This is the ONLY comment route on the data arc: listing, editing, deleting
// and resolving go through the dataset-scoped comment-thread routes that
// `qctl comment-thread` already wraps. linked_dry_run_job_id is never set —
// the backend ignores it here (it is rule-arc only).
func (c *Client) AddComment(accessToken, reviewID string, opts CommentOptions) (*Comment, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	body := api.AddDataReviewCommentJSONRequestBody{Body: opts.Body}
	if opts.ParentID != "" {
		parentUUID, err := parseUUID("comment id", opts.ParentID)
		if err != nil {
			return nil, err
		}
		body.ParentID = &parentUUID
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.AddDataReviewCommentWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.AddDataReviewCommentParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "add comment failed")
	}
	if resp.StatusCode() != http.StatusCreated {
		return nil, handleError(resp.StatusCode(), resp.Body, "add comment failed")
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON201, nil
}
