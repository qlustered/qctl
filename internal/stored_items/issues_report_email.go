package stored_items

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// IssuesReportEmail is the answer of a queued issues report email: result and
// the send's id.
type IssuesReportEmail = api.IssuesReportEmailResponseSchema

// SendIssuesReportEmail queues an email of a file's issues report with
// POST /stored-items/{id}/issues-report-emails. A non-2xx answer is an
// *APIError carrying the response's error_code and context.
func (c *Client) SendIssuesReportEmail(accessToken string, storedItemID int) (*IssuesReportEmail, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.SendBrokerIssuesReportEmailWithResponse(
		context.Background(), c.organizationID, storedItemID, &api.SendBrokerIssuesReportEmailParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusAccepted {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON202 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON202, nil
}
