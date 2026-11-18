package notifications

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
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Type aliases to the generated schemas.
type (
	Notification = api.NotificationSchema
	List         = api.NotificationListSchema
	UnreadCount  = api.UnreadCountSchema
	Actor        = api.UserInfoTinyDictSchema
)

// Client handles HTTP requests for notification inbox operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new notifications client (standard pattern).
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

// parseNotificationID keeps the message identical to cmdutil.ValidateUUIDArgs',
// which the command layer runs first: notification ids are never prefix
// resolved, so a short one is a typo rather than something to look up.
// That twin message is built with qerrors.NewBadUsagef and exits 2, so this
// one is too and both paths agree on the exit code as well as the wording.
func parseNotificationID(id string) (openapi_types.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return openapi_types.UUID{}, qerrors.NewBadUsagef("invalid notification id %q: expected a full UUID", id)
	}
	return parsed, nil
}

// ListOptions are the two independent axes plus paging.
//
// UnreadOnly narrows the READ axis; IncludeDismissed widens the DISMISSED
// axis. They are deliberately asymmetric because the endpoint is: the backend
// hides dismissed rows by default and shows read ones by default, so "only
// unread" and "also dismissed" are the two moves it offers.
type ListOptions struct {
	Types            []Type
	UnreadOnly       bool
	IncludeDismissed bool
	Page             int
	Limit            int
}

// List returns one page of the caller's inbox, newest first.
func (c *Client) List(accessToken string, opts ListOptions) (*List, error) {
	return c.ListContext(context.Background(), accessToken, opts)
}

// ListContext is List with ctx controlling the request.
func (c *Client) ListContext(ctx context.Context, accessToken string, opts ListOptions) (*List, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.GetNotificationsParams{}
	if len(opts.Types) > 0 {
		types := opts.Types
		params.NotificationType = &types
	}
	// Each flag is sent only when set: the backend defaults both to false, so
	// sending an explicit false would put a filter on every request that the
	// user never typed.
	if opts.UnreadOnly {
		unread := true
		params.UnreadOnly = &unread
	}
	if opts.IncludeDismissed {
		include := true
		params.IncludeDismissed = &include
	}
	if opts.Page > 0 {
		params.Page = &opts.Page
	}
	if opts.Limit > 0 {
		params.Limit = &opts.Limit
	}

	resp, err := apiClient.API.GetNotificationsWithResponse(ctx, c.organizationID, params)
	if err != nil {
		return nil, apiClient.HandleError(err, "list notifications failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "list notifications failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// UnreadCount answers with the badge's two numbers. Both are over rows that
// are unread AND undismissed, so dismissing lowers them exactly as reading
// does; needs_you is the actionable subset (see IsNeedsYou).
func (c *Client) UnreadCount(accessToken string) (*UnreadCount, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetUnreadNotificationCountWithResponse(
		context.Background(), c.organizationID, &api.GetUnreadNotificationCountParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "get unread notification count failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "get unread notification count failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// MarkRead marks one notification read. Idempotent on the backend
// (read_at = coalesce(read_at, now)), and it does NOT dismiss anything. It
// also works on an already-dismissed notification, which the bulk form does
// not.
func (c *Client) MarkRead(accessToken, notificationID string) error {
	id, err := parseNotificationID(notificationID)
	if err != nil {
		return err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.MarkNotificationReadWithResponse(
		context.Background(), c.organizationID, id, &api.MarkNotificationReadParams{})
	if err != nil {
		return apiClient.HandleError(err, "mark notification read failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "mark notification read failed")
	}
	return nil
}

// MarkAllRead marks every active unread notification read and returns how many
// moved. types narrows it; nil means all types. This is ONE request however
// many types are given -- the endpoint takes the repeatable filter as a query
// parameter, which is what lets the bulk mark be atomic where dismiss cannot.
//
// It skips dismissed notifications (dismissed_at IS NULL is in the backend's
// filter), so it is deliberately narrower than MarkRead.
func (c *Client) MarkAllRead(accessToken string, types []Type) (int, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return 0, err
	}
	params := &api.MarkAllNotificationsReadParams{}
	if len(types) > 0 {
		t := types
		params.NotificationType = &t
	}
	resp, err := apiClient.API.MarkAllNotificationsReadWithResponse(context.Background(), c.organizationID, params)
	if err != nil {
		return 0, apiClient.HandleError(err, "mark notifications read failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return 0, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "mark notifications read failed")
	}
	if resp.JSON200 == nil {
		return 0, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200.ReadCount, nil
}

// Dismiss takes one notification out of the inbox.
//
// NOT idempotent: the backend's update filters dismissed_at IS NULL and the
// view turns a zero rowcount into a 404, so dismissing an already-dismissed
// notification is an error (exit 3), not a no-op. Dismissing does not mark it
// read.
func (c *Client) Dismiss(accessToken, notificationID string) error {
	id, err := parseNotificationID(notificationID)
	if err != nil {
		return err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DismissNotificationWithResponse(
		context.Background(), c.organizationID, id, &api.DismissNotificationParams{})
	if err != nil {
		return apiClient.HandleError(err, "dismiss notification failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "dismiss notification failed")
	}
	return nil
}

// DismissAll dismisses every active notification in the org and returns how
// many moved. Unlike Dismiss, an empty inbox is a 200 with a count of 0.
func (c *Client) DismissAll(accessToken string) (int, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return 0, err
	}
	resp, err := apiClient.API.DismissAllNotificationsWithResponse(
		context.Background(), c.organizationID, &api.DismissAllNotificationsParams{})
	if err != nil {
		return 0, apiClient.HandleError(err, "dismiss notifications failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return 0, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "dismiss notifications failed")
	}
	if resp.JSON200 == nil {
		return 0, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200.DismissedCount, nil
}

// DismissByType dismisses every active notification of ONE type and returns
// how many moved. One type per call: the type is a PATH segment, so there is
// no multi-type form on the wire and a caller wanting several must loop.
func (c *Client) DismissByType(accessToken string, t Type) (int, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return 0, err
	}
	resp, err := apiClient.API.DismissNotificationsByTypeWithResponse(
		context.Background(), c.organizationID, t, &api.DismissNotificationsByTypeParams{})
	if err != nil {
		return 0, apiClient.HandleError(err, "dismiss notifications failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return 0, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "dismiss notifications failed")
	}
	if resp.JSON200 == nil {
		return 0, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200.DismissedCount, nil
}
