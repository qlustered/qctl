package warnings

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// msgsListMaxIDs chunks a MsgsList request.
//
// The handler authorizes the requested ids with
// `select(WarningItem.id).where(...).limit(1000)`
// (capi/capi/views/warning_view_core.py), so an id past that limit comes back
// missing rather than as an error — indistinguishable from an id this table
// has no warning for.
const msgsListMaxIDs = 1000

// MsgsList hydrates warning ids into their display messages and the user flags
// on each (user_submitted_issues), keyed by id.
//
// This is the batch join the web grid makes for a page of data: one request per
// page rather than one `describe warning` per id. It needs `warning:list`,
// which `get data` does not. Warnings ride on CLEAN rows as well as quarantined
// ones, so this is the hydration a clean-table page needs.
//
// An id the server does not answer for is absent from the map, not an error:
// the handler selects on id plus settings model and, for an external user,
// associated data sources, so an id this table has no warning for simply is
// not in the response. Callers report the gap; they must not read it as an
// empty message.
//
// It never reads `resolved`: a warning whose token is still on a row answers
// with its message whether or not somebody has resolved it.
//
// The alert and warning id spaces are separate — alert 12 and warning 12 are
// different objects on different routes — so never merge the two maps.
func (c *Client) MsgsList(accessToken string, datasetID int, ids []int) (map[int]api.WarningsMsgsListResponseSchema, error) {
	msgs := map[int]api.WarningsMsgsListResponseSchema{}
	if len(ids) == 0 {
		return msgs, nil
	}
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	for start := 0; start < len(ids); start += msgsListMaxIDs {
		end := start + msgsListMaxIDs
		if end > len(ids) {
			end = len(ids)
		}
		resp, err := apiClient.API.GetWarningsMsgsListWithResponse(
			context.Background(), c.organizationID, datasetID,
			&api.GetWarningsMsgsListParams{},
			api.WarningsMsgsListRequestSchema{Ids: ids[start:end]})
		if err != nil {
			return nil, apiClient.HandleError(err, "failed to get warning messages")
		}
		if resp.StatusCode() != http.StatusOK {
			return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to get warning messages")
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("unexpected empty response")
		}
		for _, item := range *resp.JSON200 {
			msgs[item.ID] = item
		}
	}
	return msgs, nil
}
