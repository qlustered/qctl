package alerts

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// PutAlertReportForDownload queues an Excel report of a table's unresolved
// row-level alerts and warnings and returns its download_handle. body is sent
// as given; a non-200 response is an *APIError.
//
// The handle identifies THIS request's workbook on the file_ready_to_download
// notification that announces it (notifications.WaitOptions.DownloadHandle).
// It is "" against a server before atlas d117e8429, which answers only
// {"result": "ok"}.
func (c *Client) PutAlertReportForDownload(accessToken string, datasetID int, body api.AlertReportRequestSchema) (string, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.PutAlertReportForDownloadWithResponse(
		context.Background(), c.organizationID, datasetID, nil, body)
	if err != nil {
		return "", wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return "", handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	// An unparsable 200 costs precision in the wait, never the report; see
	// datasets.Client.PutDataForDownload.
	if resp.JSON200 == nil {
		return "", nil
	}
	return resp.JSON200.DownloadHandle, nil
}
