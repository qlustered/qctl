package rule_reviews //nolint:revive // Established package path; renaming would break imports.

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
	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Type aliases to the generated schemas (frozen cross-phase contract).
type (
	Review          = api.ReviewResponseSchema       // create / close responses
	ReviewDetail    = api.ReviewDetailResponseSchema // get / reopen / assign / unassign
	InboxItem       = api.ReviewListItemSchema
	InboxPage       = api.ReviewsInboxResponseSchema       // {Reviews, Page, Limit, Total}
	RerequestResult = api.RerequestReviewersResponseSchema // {ReviewID, RerequestedReviewerIds, Status}
	AuditPage       = api.ReviewAuditListSchema            // {AuditEntries, Page, Limit, Total}
	AuditEntry      = api.ReviewAuditEntrySchema
	EffectivePolicy = api.EffectivePolicyResponseSchema
	Comment         = api.CommentResponseSchema        // Phase 2: detail-embedded thread + add/edit responses
	Decision        = api.ReviewDecisionResponseSchema // Phase 2: detail-embedded decisions
	DecisionResult  = api.SubmitDecisionResponseSchema // Phase 2: {Decision, ReviewCompleted, ReviewStatus}
)

// Client handles HTTP requests for rule review operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new rule reviews client (standard pattern).
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

// wrapAPIError converts an error returned by a generated *WithResponse call
// into an *APIError when the transport captured a structured error body. This
// covers the 422 case: the generated parser rejects the backend's
// object-shaped detail (its HTTPValidationError expects a list), returning a
// parse error before the caller can inspect resp.Body — so we rebuild the
// error from the captured bytes to preserve error_code/context. Falls back to
// the client's generic handling for transport-level failures with no HTTP body.
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

func parseReviewID(reviewID string) (openapi_types.UUID, error) {
	id, err := uuid.Parse(reviewID)
	if err != nil {
		return openapi_types.UUID{}, qerrors.NewBadUsagef("invalid review id %q: expected a full UUID", reviewID)
	}
	return id, nil
}

// InboxOptions filters the reviews inbox.
type InboxOptions struct {
	Page, Limit  int    // command defaults: 1 / 20 (API max 100)
	Status       string // "" = all; else open|changes_requested|approved|rejected|closed
	All          bool   // true → my_reviews_only=false (admin/ops only; backend enforces)
	ReviewerType string // "" = all; else author|reviewer|mentioned
}

// Inbox lists reviews the caller is involved in (or all, with All).
func (c *Client) Inbox(accessToken string, opts InboxOptions) (*InboxPage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListRuleReviewsInboxParams{}
	if opts.Page > 0 {
		params.Page = &opts.Page
	}
	if opts.Limit > 0 {
		params.Limit = &opts.Limit
	}
	if opts.Status != "" {
		s := api.ReviewRequestStatus(opts.Status)
		params.Status = &s
	}
	if opts.All {
		mine := false
		params.MyReviewsOnly = &mine
	}
	if opts.ReviewerType != "" {
		rt := api.ReviewerType(opts.ReviewerType)
		params.ReviewerType = &rt
	}
	resp, err := apiClient.API.ListRuleReviewsInboxWithResponse(context.Background(), c.organizationID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list rule reviews failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list rule reviews failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// ListForRevision lists reviews bound to one rule revision (unpaginated,
// created_at desc). datasetID nil = no scope filter; status "" = all.
func (c *Client) ListForRevision(accessToken, ruleRevisionID string, datasetID *int, status string) ([]Review, error) {
	revisionUUID, err := uuid.Parse(ruleRevisionID)
	if err != nil {
		return nil, qerrors.NewBadUsagef("invalid rule revision id %q: expected a full UUID", ruleRevisionID)
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListRuleReviewsForRevisionParams{DatasetID: datasetID}
	if status != "" {
		s := api.ReviewRequestStatus(status)
		params.StatusFilter = &s
	}
	resp, err := apiClient.API.ListRuleReviewsForRevisionWithResponse(
		context.Background(), c.organizationID, revisionUUID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list rule reviews failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list rule reviews failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200.Reviews, nil
}

// Get fetches the full review detail.
func (c *Client) Get(accessToken, reviewID string) (*ReviewDetail, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetRuleReviewWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.GetRuleReviewParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get rule review failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get rule review failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// AuditLog fetches one page of the review's audit trail.
func (c *Client) AuditLog(accessToken, reviewID string, page, limit int) (*AuditPage, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListRuleReviewAuditLogParams{}
	if page > 0 {
		params.Page = &page
	}
	if limit > 0 {
		params.Limit = &limit
	}
	resp, err := apiClient.API.ListRuleReviewAuditLogWithResponse(
		context.Background(), c.organizationID, reviewUUID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get audit log failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get audit log failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// CreateReviewOptions are the caller-supplied fields for a new review request.
type CreateReviewOptions struct {
	Title       string               // required by the API; command defaults to "Review <name>@<release>"
	Description string               // "" = omit
	DatasetID   *int                 // nil = org-wide
	ReviewerIDs []openapi_types.UUID // initial assignees; nil = none
}

// createReviewWireBody is a hand-written mirror of api.CreateReviewRequestSchema.
// The generated schema serializes a nil Description as `"description": null`,
// but the backend contract requires the key to be ABSENT when there is no
// description (while `dataset_id` must stay present-and-null for org-wide
// reviews). Per CLAUDE.md, client.gen.go must never be hand-edited, so this
// wrapper struct — not a tweak to the generated tag — encodes that difference.
type createReviewWireBody struct {
	Title               string                `json:"title"`
	Description         *string               `json:"description,omitempty"`
	DatasetID           *int                  `json:"dataset_id"` // no omitempty: null = org-wide
	AssignedReviewerIDs *[]openapi_types.UUID `json:"assigned_reviewer_ids,omitempty"`
}

// CreateReview creates a review request for a rule revision
// (side effect server-side: revision draft → in_review).
func (c *Client) CreateReview(accessToken, ruleRevisionID string, opts CreateReviewOptions) (*Review, error) {
	revisionUUID, err := uuid.Parse(ruleRevisionID)
	if err != nil {
		return nil, qerrors.NewBadUsagef("invalid rule revision id %q: expected a full UUID", ruleRevisionID)
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := createReviewWireBody{
		Title:     opts.Title,
		DatasetID: opts.DatasetID,
	}
	if opts.Description != "" {
		body.Description = &opts.Description
	}
	if len(opts.ReviewerIDs) > 0 {
		body.AssignedReviewerIDs = &opts.ReviewerIDs
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to encode create review request: %w", err)
	}
	resp, err := apiClient.API.CreateRuleReviewRequestWithBodyWithResponse(
		context.Background(), c.organizationID, revisionUUID, &api.CreateRuleReviewRequestParams{}, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "create review request failed")
	}
	if resp.StatusCode() != http.StatusCreated {
		return nil, handleError(resp.StatusCode(), resp.Body, "create review request failed")
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON201, nil
}

// Close closes an open/changes_requested review (terminal).
func (c *Client) Close(accessToken, reviewID string) (*Review, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CloseRuleReviewWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.CloseRuleReviewParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "close review failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "close review failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Reopen reopens a rejected review. The returned status must be reported
// verbatim — it may be "closed" (superseded by a sibling active review).
func (c *Client) Reopen(accessToken, reviewID string) (*ReviewDetail, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ReopenRuleReviewWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.ReopenRuleReviewParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "reopen review failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "reopen review failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Assign adds reviewers to an open/changes_requested review.
func (c *Client) Assign(accessToken, reviewID string, reviewerIDs []openapi_types.UUID) (*ReviewDetail, error) {
	return c.assignOrUnassign(accessToken, reviewID, reviewerIDs, true)
}

// Unassign removes reviewers; on an approved review this can retract an
// approval and reopen it — report the returned status.
func (c *Client) Unassign(accessToken, reviewID string, reviewerIDs []openapi_types.UUID) (*ReviewDetail, error) {
	return c.assignOrUnassign(accessToken, reviewID, reviewerIDs, false)
}

func (c *Client) assignOrUnassign(accessToken, reviewID string, reviewerIDs []openapi_types.UUID, assign bool) (*ReviewDetail, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	var (
		statusCode int
		rawBody    []byte
		payload    *api.ReviewDetailResponseSchema
		contextMsg string
	)
	if assign {
		contextMsg = "assign reviewers failed"
		resp, err := apiClient.API.AssignRuleReviewersWithResponse(
			context.Background(), c.organizationID, reviewUUID, &api.AssignRuleReviewersParams{},
			api.AssignRuleReviewersJSONRequestBody{ReviewerIds: reviewerIDs})
		if err != nil {
			return nil, wrapAPIError(apiClient, err, contextMsg)
		}
		statusCode, rawBody, payload = resp.StatusCode(), resp.Body, resp.JSON200
	} else {
		contextMsg = "unassign reviewers failed"
		resp, err := apiClient.API.UnassignRuleReviewersWithResponse(
			context.Background(), c.organizationID, reviewUUID, &api.UnassignRuleReviewersParams{},
			api.UnassignRuleReviewersJSONRequestBody{ReviewerIds: reviewerIDs})
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

// LinkDryRun links a dry-run job to the review headline. This is what the
// policy gate checks; it overwrites any previous link and has no review-status
// restriction. The backend does NOT verify the job exists or belongs to the
// org (a bogus id dies on the DB FK), so callers pre-validate with
// dry_runs.GetDryRunJob before calling this.
func (c *Client) LinkDryRun(accessToken, reviewID string, jobID int) (*Review, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.LinkDryRunToRuleReviewWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.LinkDryRunToRuleReviewParams{},
		api.LinkDryRunToRuleReviewJSONRequestBody{DryRunJobID: jobID})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "link dry run failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "link dry run failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// LinkDryRunToComment links a dry-run job to a single comment. This is
// display-only — no policy significance — so the response message is discarded.
// Callers pre-validate the job the same way as LinkDryRun.
func (c *Client) LinkDryRunToComment(accessToken, reviewID, commentID string, jobID int) error {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return err
	}
	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return qerrors.NewBadUsagef("invalid comment id %q: expected a full UUID", commentID)
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.LinkDryRunToCommentOfRuleReviewWithResponse(
		context.Background(), c.organizationID, reviewUUID, commentUUID, &api.LinkDryRunToCommentOfRuleReviewParams{},
		api.LinkDryRunToCommentOfRuleReviewJSONRequestBody{DryRunJobID: jobID})
	if err != nil {
		return wrapAPIError(apiClient, err, "link dry run to comment failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return handleError(resp.StatusCode(), resp.Body, "link dry run to comment failed")
	}
	return nil
}

// Rerequest re-notifies assigned reviewers. Empty/nil reviewerIDs = the
// server nudges everyone still being waited on.
func (c *Client) Rerequest(accessToken, reviewID string, reviewerIDs []openapi_types.UUID) (*RerequestResult, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.RerequestRuleReviewersJSONRequestBody{}
	if len(reviewerIDs) > 0 {
		body.ReviewerIds = &reviewerIDs
	}
	resp, err := apiClient.API.RerequestRuleReviewersWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.RerequestRuleReviewersParams{}, body)
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

// CommentOptions are the caller-supplied fields for a new comment.
type CommentOptions struct {
	Body        string
	ParentID    string // "" = top-level; else the parent comment UUID
	DryRunJobID *int   // nil = none; callers pre-validate the job (see LinkDryRun)
}

// AddComment posts a comment (optionally a threaded reply, max depth 3) to a
// review. Any role that can see the review can comment, regardless of status.
func (c *Client) AddComment(accessToken, reviewID string, opts CommentOptions) (*Comment, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	body := api.CreateCommentSchema{
		Body:              opts.Body,
		LinkedDryRunJobID: opts.DryRunJobID,
	}
	if opts.ParentID != "" {
		parentUUID, err := uuid.Parse(opts.ParentID)
		if err != nil {
			return nil, qerrors.NewBadUsagef("invalid comment id %q: expected a full UUID", opts.ParentID)
		}
		body.ParentID = &parentUUID
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.AddCommentToRuleReviewWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.AddCommentToRuleReviewParams{}, body)
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

// EditComment replaces a comment's body. Author-only (403 otherwise — admins
// cannot edit others' comments); sets is_edited; newly added mentions notify.
func (c *Client) EditComment(accessToken, reviewID, commentID, body string) (*Comment, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return nil, qerrors.NewBadUsagef("invalid comment id %q: expected a full UUID", commentID)
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.EditCommentForRuleReviewWithResponse(
		context.Background(), c.organizationID, reviewUUID, commentUUID,
		&api.EditCommentForRuleReviewParams{}, api.EditCommentSchema{Body: body})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "edit comment failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "edit comment failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// DeleteComment removes a comment (author or org admin). Server-side: leaf
// comments are removed outright; comments with replies are tombstoned
// ("[comment deleted]", thread structure preserved).
func (c *Client) DeleteComment(accessToken, reviewID, commentID string) error {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return err
	}
	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return qerrors.NewBadUsagef("invalid comment id %q: expected a full UUID", commentID)
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteCommentOfRuleReviewWithResponse(
		context.Background(), c.organizationID, reviewUUID, commentUUID, &api.DeleteCommentOfRuleReviewParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, "delete comment failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return handleError(resp.StatusCode(), resp.Body, "delete comment failed")
	}
	return nil
}

// SubmitDecision submits the caller's verdict. The backend upserts one row
// per reviewer — submitting again replaces the previous verdict. comment ""
// is omitted (null == absent for the backend's Optional[str]).
func (c *Client) SubmitDecision(accessToken, reviewID string, decision api.ReviewDecisionEnum, comment string) (*DecisionResult, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.SubmitDecisionSchema{Decision: decision}
	if comment != "" {
		body.Comment = &comment
	}
	resp, err := apiClient.API.SubmitRuleReviewDecisionWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.SubmitRuleReviewDecisionParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "submit decision failed")
	}
	if resp.StatusCode() != http.StatusCreated {
		return nil, handleError(resp.StatusCode(), resp.Body, "submit decision failed")
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON201, nil
}

// WithdrawDecision withdraws the caller's own standing decision and returns
// the recomputed detail. Report the returned status verbatim — withdrawal can
// reopen an approved review, keep it approved, or land it closed (superseded).
func (c *Client) WithdrawDecision(accessToken, reviewID string) (*ReviewDetail, error) {
	reviewUUID, err := parseReviewID(reviewID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.WithdrawRuleReviewDecisionWithResponse(
		context.Background(), c.organizationID, reviewUUID, &api.WithdrawRuleReviewDecisionParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "withdraw decision failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "withdraw decision failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
