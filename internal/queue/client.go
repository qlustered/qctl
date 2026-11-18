// Package queue is the domain client for the platform's internal job queue —
// the seven ops routes in atlas's capi/capi/views/queue_view.py.
package queue

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// Client handles HTTP requests for queue message operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new queue client. It validates the organization ID up
// front, like ingestion.NewClient and rule_versions.NewClient.
func NewClient(baseURL, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgID,
		verbosity:      verbosity,
	}, nil
}

// Type aliases for generated types — use these directly.
type (
	Message             = api.QueueSchema
	MessagesPage        = api.QueuesListSchema
	DeadIDs             = api.QueueDeadListResponseSchema
	RescheduleReply     = api.QueuesRescheduleResponseSchema
	ResurrectReply      = api.QueueResurrectOneResponseSchema
	ResurrectBatchReply = api.QueueResurrectBatchResponseSchema
	RetireReply         = api.QueueRetireOldResponseSchema
	OrderBy             = api.QueueOrderBy
)

// GetMessagesParams holds the filters for listing queue messages.
//
// Reverse carries qctl's house inversion: this endpoint defaults `reverse=true`
// server-side, so `--reverse` means "reverse the DEFAULT order" and is sent as
// reverse=false. See internal/rule_versions/client.go for the same note; it is
// not a copy-paste bug.
type GetMessagesParams struct {
	Acknowledged      *bool
	IsDead            *bool
	QName             *string
	DatasetID         *int
	DataSourceModelID *int
	SearchQuery       string
	OrderBy           string
	Page              int
	Limit             int
	Reverse           bool
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

// GetMessages lists queue messages.
func (c *Client) GetMessages(accessToken string, params GetMessagesParams) (*MessagesPage, error) {
	return c.GetMessagesContext(context.Background(), accessToken, params)
}

// GetMessagesContext is GetMessages with ctx controlling the request.
func (c *Client) GetMessagesContext(ctx context.Context, accessToken string, params GetMessagesParams) (*MessagesPage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	apiParams := &api.GetQueueMsgsParams{
		Acknowledged:      params.Acknowledged,
		IsDead:            params.IsDead,
		QName:             params.QName,
		DatasetID:         params.DatasetID,
		DataSourceModelID: params.DataSourceModelID,
	}
	if params.SearchQuery != "" {
		apiParams.SearchQuery = &params.SearchQuery
	}
	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.QueueOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetQueueMsgsWithResponse(ctx, c.organizationID, apiParams)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetMessage retrieves a single queue message by its primary key.
func (c *Client) GetMessage(accessToken string, msgID int) (*Message, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	resp, err := apiClient.API.GetOneQueueMsgWithResponse(
		context.Background(), c.organizationID, msgID, &api.GetOneQueueMsgParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// ListDeadParams holds the dead-list filters.
//
// IdleFor is sent as idle_for, the same PostgreSQL interval
// ResurrectBatchParams.OlderThan carries; an empty value lets the server fall
// back to CUEUE_INVISIBLE_TIME.
type ListDeadParams struct {
	IdleFor string
	Limit   int
}

// ListDeadIDs returns the ids ResurrectBatch would revive, as of this call.
//
// This is NOT the same population as GetMessages with IsDead set. The listing
// filter is `acknowledged = false AND receive_count >= max_receive_count`;
// this route adds `(dequeued_at IS NULL OR dequeued_at + idle_for < now())`,
// so a message a consumer picked up moments ago is is_dead in the listing and
// absent here. Given the same interval and limit, the route applies
// ResurrectBatch's predicate, order and default window.
//
// It does not expose the route's q_name: ResurrectBatch takes no queue.
func (c *Client) ListDeadIDs(accessToken string, params ListDeadParams) (*DeadIDs, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	apiParams := &api.ListDeadQueueMessagesParams{}
	if params.IdleFor != "" {
		apiParams.IdleFor = &params.IdleFor
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}

	resp, err := apiClient.API.ListDeadQueueMessagesWithResponse(
		context.Background(), c.organizationID, apiParams)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Reschedule resets messages so they are delivered again immediately.
//
// The route updates every UNACKNOWLEDGED message among the ids — not only the
// dead ones — and then DISCARDS the UPDATE's rowcount, answering a flat
// {"result": "ok"}. An id that is acknowledged, or belongs to another
// organization, is a silent no-op. Callers must not report a number of
// messages retried; there is none to report.
func (c *Client) Reschedule(accessToken string, ids []int) (*RescheduleReply, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	resp, err := apiClient.API.RescheduleQueueMsgsWithResponse(
		context.Background(), c.organizationID, &api.RescheduleQueueMsgsParams{},
		api.QueuesRescheduleRequestSchema{Ids: ids})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Resurrect revives one soft-dead message, optionally after a backoff delay.
//
// The route answers 500 — not 400 or 404 — when the message is not soft-dead,
// so callers check the message's state themselves before calling this.
func (c *Client) Resurrect(accessToken string, msgID, backoffSeconds int) (*ResurrectReply, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	body := api.QueueResurrectOneRequestSchema{BackoffSeconds: &backoffSeconds}
	resp, err := apiClient.API.ResurrectQueueMessageWithResponse(
		context.Background(), c.organizationID, msgID, &api.ResurrectQueueMessageParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// ResurrectBatchParams holds the batch resurrect body.
//
// OlderThan is a PostgreSQL interval measured from dequeued_at — the last
// delivery attempt — and a message that was never dequeued qualifies whatever
// it says. An empty value lets the server fall back to CUEUE_INVISIBLE_TIME,
// the platform-wide default, whatever the message's queue. This is NOT the
// column RetireParams.OlderThan uses.
type ResurrectBatchParams struct {
	OlderThan      string
	Limit          int
	BackoffSeconds int
}

// ResurrectBatch revives soft-dead messages in bulk.
func (c *Client) ResurrectBatch(accessToken string, params ResurrectBatchParams) (*ResurrectBatchReply, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	body := api.QueueResurrectBatchRequestSchema{
		BackoffSeconds: &params.BackoffSeconds,
	}
	if params.OlderThan != "" {
		body.OlderThan = &params.OlderThan
	}
	if params.Limit > 0 {
		body.Limit = &params.Limit
	}

	resp, err := apiClient.API.ResurrectBatchQueueMessagesWithResponse(
		context.Background(), c.organizationID, &api.ResurrectBatchQueueMessagesParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// RetireParams holds the retire-old body.
//
// OlderThan is a PostgreSQL interval measured from enqueued_at — when the
// message was first put on the queue — with no idle condition at all. The same
// string means something different here and on ResurrectBatchParams.
type RetireParams struct {
	OlderThan string
	Limit     int
}

// Retire acknowledges very old soft-dead messages.
//
// It does NOT delete them: the UPDATE sets acknowledged = true and the rows
// stay listable. What makes it final is that no route un-acknowledges, so a
// retired message can never be rescheduled or resurrected again — both guard
// on acknowledged = false.
func (c *Client) Retire(accessToken string, params RetireParams) (*RetireReply, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	body := api.QueueRetireOldRequestSchema{}
	if params.OlderThan != "" {
		body.OlderThan = &params.OlderThan
	}
	if params.Limit > 0 {
		body.Limit = &params.Limit
	}

	resp, err := apiClient.API.RetireOldDeadMessagesWithResponse(
		context.Background(), c.organizationID, &api.RetireOldDeadMessagesParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
