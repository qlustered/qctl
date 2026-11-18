package notifications

import (
	"context"
	"fmt"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
)

// DownloadURL is the presigned GET minted for one file_ready_to_download
// notification. The link does not exist until this call: nothing on the wire
// and nothing in the database holds it, so it can be neither cached nor parsed
// out of a notification's msg.
type DownloadURL = api.NotificationDownloadURLSchema

// DownloadURL mints a presigned GET for the file a file_ready_to_download
// notification announces.
//
// The id is an openapi_types.UUID rather than a string because it is never
// user input -- it comes from a Notification the wait loop already matched.
// The string-taking methods in this package run parseNotificationID to turn a
// user's typo into exit 2; there is no typo to catch here.
//
// The route has two distinct failure modes and they map to DIFFERENT exit
// codes, so do not conflate them:
//
//   - 404 "Notification not found" -- the row does not exist, is not this
//     user's, or belongs to another organization. atlas answers 404 rather
//     than 403 on purpose, so the route is not an existence oracle
//     (capi/views/notification_view.py, get_notification_download_url).
//     GetExitCode maps it to ExitNotFound (3).
//   - 409 "Notification {id} carries no downloadable file." -- the row exists
//     but is the wrong notification_type, or carries no file_download_key or
//     no backup_settings_id. The same 409 also says "points at a backup
//     connection that no longer exists" when the BackupSettings row is gone
//     (capi/controllers/notification_download_controller.py,
//     mint_download_url_for_notification). GetExitCode maps it to
//     ExitBadUsage (2).
//
// A missing OBJECT in the bucket is not a 409, and is not visible here at all:
// every storage backend's get_download_url checks the BUCKET and never touches
// the key, so an absent object surfaces only later, from the presigned GET
// itself. A missing bucket does fail this call, but as a storage error rather
// than a 409.
//
// Both codes arrive as the backend's own sentence through apierror, which is
// deliberate: those sentences are more informative than anything qctl would
// write over them.
func (c *Client) DownloadURL(accessToken string, notificationID openapi_types.UUID) (*DownloadURL, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetNotificationDownloadURLWithResponse(
		context.Background(), c.organizationID, notificationID, &api.GetNotificationDownloadURLParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "get download url failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "get download url failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
