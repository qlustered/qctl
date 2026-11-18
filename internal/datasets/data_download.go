package datasets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// PutDataForDownload enqueues an export of the rows matching params and
// returns its download_handle.
//
// The route stays fire-and-forget otherwise: it validates, publishes one queue
// message and answers with no job id, no stored-item id and no URL. The
// produced CSV is announced later by a per-user file_ready_to_download
// NOTIFICATION, and the link to it is minted on demand from that notification
// (notifications.Client.DownloadURL).
//
// The handle is the digest half of the object key atlas built for this
// request, and the announcement of THIS request's file carries the same value
// (notifications.WaitOptions.DownloadHandle). It is "" against a server before
// atlas d117e8429, which sends only {"result": "ok"} -- the field is required
// in the spec, so the generated struct cannot tell an absent one from an empty
// one, and both mean the same thing to a caller: correlate some other way.
//
// params is sent unmodified; nil is treated as an empty
// PutDataForDownloadParams. The backend remains authoritative for validation,
// including the 409 it raises when an identical view is already queued -- what
// that means to the user depends on whether the caller is waiting, so it is
// not interpreted here.
//
// opts carry the query parameters api.PutDataForDownloadParams cannot hold —
// today only WithAlertItemID, whose generated field is unusable; see
// alert_item_filter.go.
func (c *Client) PutDataForDownload(
	accessToken string, datasetID int, params *api.PutDataForDownloadParams, opts ...DataQueryOption,
) (string, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create API client: %w", err)
	}
	if params == nil {
		params = &api.PutDataForDownloadParams{}
	}

	var resp *api.PutDataForDownloadResponse
	if c.thirdParty {
		rawResponse, requestErr := apiClient.API.PutThirdPartyDataForDownload(
			context.Background(), c.organizationID, datasetID, (*api.PutThirdPartyDataForDownloadParams)(params), applyDataQueryOptions(opts)...)
		err = requestErr
		if err == nil {
			resp, err = api.ParsePutDataForDownloadResponse(rawResponse)
		}
	} else {
		resp, err = apiClient.API.PutDataForDownloadWithResponse(
			context.Background(), c.organizationID, datasetID, params, applyDataQueryOptions(opts)...)
	}
	if err != nil {
		return "", apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return "", apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	// A 200 whose body did not parse is not an error here: the handle is an
	// optimisation over the wait's older correlation, so an unreadable one
	// costs precision and never the export.
	if resp.JSON200 == nil {
		return "", nil
	}
	return resp.JSON200.DownloadHandle, nil
}
