package stored_items

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// ReportingPeriodAssignment is the answer of a reporting-period assignment:
// the file, the table it now belongs to, and the period it was sent to.
type ReportingPeriodAssignment = api.StoredItemReportingPeriodResponseSchema

// AssignReportingPeriod sends a file that waits for its reporting period to
// the period periodKey, with PUT /stored-items/{id}/reporting-period. A non-2xx
// answer is an *APIError carrying the response's error_code.
func (c *Client) AssignReportingPeriod(accessToken string, storedItemID int, periodKey string) (*ReportingPeriodAssignment, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.AssignStoredItemReportingPeriodWithResponse(
		context.Background(), c.organizationID, storedItemID,
		&api.AssignStoredItemReportingPeriodParams{},
		api.StoredItemReportingPeriodRequestSchema{PeriodKey: periodKey})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
