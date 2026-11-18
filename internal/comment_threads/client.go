// Package comment_threads is the domain client for dataset-scoped comment
// threads: a discussion anchored to cells or whole rows of a table.
//
// These routes serve cell/row threads and data-review threads. A
// rule-revision-review thread is deliberately NOT reachable here — the backend
// answers 404 for any thread carrying rule_revision_review_id, and the rule arc
// keeps its own /rule-reviews/{id}/comments routes (qctl review comment).
package comment_threads //nolint:revive // Matches the backend resource name.

import (
	"context"
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

// byRowsMaxRows is the backend's cap on ThreadsByRowsRequestSchema.row_ids.
const byRowsMaxRows = 100

// Type aliases to the generated schemas (frozen cross-phase contract).
type (
	Thread        = api.CommentThreadResponseSchema
	Comment       = api.CommentResponseSchema
	Overlay       = api.RowThreadOverlaySchema
	Target        = api.CommentThreadTargetSchema
	DroppedTarget = api.DroppedTargetSchema
	CreateResult  = api.CommentThreadCreateResponseSchema
)

// APIError and Directory are re-exported so command files under
// internal/commands/ never have to import internal/comments directly.
type (
	APIError  = comments.APIError
	Directory = comments.Directory
)

// Client handles HTTP requests for dataset-scoped comment thread operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new comment threads client (standard pattern).
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
// error from the captured bytes to preserve error_code/context.
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	return comments.WrapAPIError(apiClient, err, contextMsg)
}

func handleError(statusCode int, body []byte, contextMsg string) error {
	return comments.HandleError(statusCode, body, contextMsg)
}

// parseThreadID keeps the "expected a full UUID" message identical to the rule
// arc's: thread ids are never short-prefix resolved.
func parseThreadID(id string) (openapi_types.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return openapi_types.UUID{}, qerrors.NewBadUsagef("invalid comment thread id %q: expected a full UUID", id)
	}
	return parsed, nil
}

func parseCommentID(id string) (openapi_types.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return openapi_types.UUID{}, qerrors.NewBadUsagef("invalid comment id %q: expected a full UUID", id)
	}
	return parsed, nil
}

// CreateOptions are the caller-supplied fields for a new thread. A thread is
// created WITH its first comment: Body is required by the backend.
type CreateOptions struct {
	Targets []Target
	Body    string
	Title   string
}

// Create opens a thread on one dataset's rows/cells. Targets are validated per
// target: the 201 body's DroppedTargets reports the ones that failed while the
// thread is created from the rest. A request whose targets ALL fail is a 422
// that creates nothing.
func (c *Client) Create(accessToken string, datasetID int, opts CreateOptions) (*CreateResult, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.CommentThreadCreateSchema{Targets: opts.Targets, Body: opts.Body}
	if opts.Title != "" {
		body.Title = &opts.Title
	}
	resp, err := apiClient.API.CreateCommentThreadWithResponse(
		context.Background(), c.organizationID, datasetID, &api.CreateCommentThreadParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "create comment thread failed")
	}
	if resp.StatusCode() != http.StatusCreated {
		return nil, handleError(resp.StatusCode(), resp.Body, "create comment thread failed")
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON201, nil
}

// ByRows returns every UNRESOLVED thread touching these rows. The endpoint caps
// row_ids at 100, so a longer set is split into successive calls and the
// results concatenated — the same chunking cosmosview's overlay hook does for
// a grid page. An empty set makes no request: the schema requires min_length 1.
func (c *Client) ByRows(accessToken string, datasetID int, rowIDs []int) ([]Overlay, error) {
	if len(rowIDs) == 0 {
		return nil, nil
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	var out []Overlay
	for start := 0; start < len(rowIDs); start += byRowsMaxRows {
		end := start + byRowsMaxRows
		if end > len(rowIDs) {
			end = len(rowIDs)
		}
		resp, err := apiClient.API.CommentThreadsByRowsWithResponse(
			context.Background(), c.organizationID, datasetID,
			&api.CommentThreadsByRowsParams{},
			api.ThreadsByRowsRequestSchema{RowIds: rowIDs[start:end]})
		if err != nil {
			return nil, wrapAPIError(apiClient, err, "list comment threads failed")
		}
		if resp.StatusCode() != http.StatusOK {
			return nil, handleError(resp.StatusCode(), resp.Body, "list comment threads failed")
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("unexpected empty response")
		}
		out = append(out, resp.JSON200.Threads...)
	}
	return out, nil
}

// List fetches one page of readable unresolved threads without transcripts.
func (c *Client) List(accessToken string, datasetID, page, limit int) (*api.CommentThreadListSchema, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ListOpenCommentThreadsWithResponse(
		context.Background(), c.organizationID, datasetID,
		&api.ListOpenCommentThreadsParams{Page: &page, Limit: &limit},
	)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list comment threads failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list comment threads failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Get fetches one thread with its full comment transcript and targets. A
// thread carrying rule_revision_review_id answers 404 here by design.
func (c *Client) Get(accessToken string, datasetID int, threadID string) (*Thread, error) {
	threadUUID, err := parseThreadID(threadID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetCommentThreadWithResponse(
		context.Background(), c.organizationID, datasetID, threadUUID, &api.GetCommentThreadParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get comment thread failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get comment thread failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// AddComment posts a comment on a thread. parentID "" is a top-level comment;
// otherwise it must be a comment of the SAME thread that is not deleted (else
// 404), and the reply depth is capped at 3 (422 COMMENT_DEPTH_EXCEEDED).
// linked_dry_run_job_id is deliberately left unset, so it always goes out as an
// explicit null (the generated field has no omitempty, so the key is on the
// wire either way): the backend discards any value on this arc — dry-run jobs
// belong to the rule-review arc only.
func (c *Client) AddComment(accessToken string, datasetID int, threadID, body, parentID string) (*Comment, error) {
	threadUUID, err := parseThreadID(threadID)
	if err != nil {
		return nil, err
	}
	payload := api.CreateCommentSchema{Body: body}
	if parentID != "" {
		parentUUID, err := parseCommentID(parentID)
		if err != nil {
			return nil, err
		}
		payload.ParentID = &parentUUID
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.AddThreadCommentWithResponse(
		context.Background(), c.organizationID, datasetID, threadUUID,
		&api.AddThreadCommentParams{}, payload)
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
func (c *Client) EditComment(accessToken string, datasetID int, threadID, commentID, body string) (*Comment, error) {
	threadUUID, err := parseThreadID(threadID)
	if err != nil {
		return nil, err
	}
	commentUUID, err := parseCommentID(commentID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.EditThreadCommentWithResponse(
		context.Background(), c.organizationID, datasetID, threadUUID, commentUUID,
		&api.EditThreadCommentParams{}, api.EditCommentSchema{Body: body})
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

// DeleteComment removes a comment. Author-only (403 otherwise). Server-side: a
// leaf comment is removed outright; one with replies is tombstoned (body
// rewritten to "[comment deleted]", thread structure preserved).
func (c *Client) DeleteComment(accessToken string, datasetID int, threadID, commentID string) error {
	threadUUID, err := parseThreadID(threadID)
	if err != nil {
		return err
	}
	commentUUID, err := parseCommentID(commentID)
	if err != nil {
		return err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteThreadCommentWithResponse(
		context.Background(), c.organizationID, datasetID, threadUUID, commentUUID,
		&api.DeleteThreadCommentParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, "delete comment failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return handleError(resp.StatusCode(), resp.Body, "delete comment failed")
	}
	return nil
}

// Resolve marks a thread resolved (resolved_reason "manual"). The response is
// thread STATE, not the transcript: comments is [] with a real comment_count.
func (c *Client) Resolve(accessToken string, datasetID int, threadID string) (*Thread, error) {
	threadUUID, err := parseThreadID(threadID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ResolveCommentThreadWithResponse(
		context.Background(), c.organizationID, datasetID, threadUUID, &api.ResolveCommentThreadParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "resolve comment thread failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "resolve comment thread failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Unresolve reopens a resolved thread, clearing resolved_at/by/reason. Like
// Resolve, the response carries state rather than the transcript.
func (c *Client) Unresolve(accessToken string, datasetID int, threadID string) (*Thread, error) {
	threadUUID, err := parseThreadID(threadID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.UnresolveCommentThreadWithResponse(
		context.Background(), c.organizationID, datasetID, threadUUID, &api.UnresolveCommentThreadParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "unresolve comment thread failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "unresolve comment thread failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// FetchUsers sweeps the org's membership into a Directory for name→UUID
// resolution and UUID→name display. Fetch once per command invocation.
func (c *Client) FetchUsers(accessToken string) (*comments.Directory, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	return comments.FetchUsers(apiClient, c.organizationID)
}

// FetchMentionableUsers reads the people a comment on datasetID may mention
// into a Directory.
func (c *Client) FetchMentionableUsers(accessToken string, datasetID int) (*comments.Directory, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	return comments.FetchMentionableUsers(apiClient, c.organizationID, datasetID)
}
