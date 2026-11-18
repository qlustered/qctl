package alerts

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// SubmitUserIssues raises an ad-hoc, human-authored alert on named cells of
// named QUARANTINED rows. It holds each flagged row in quarantine until the
// flag is retracted or the flagged value changes.
//
// The 200 is a PER-ROW partial success: every row comes back with its own
// status, and a row that failed is reported in the body rather than by the
// status code. Callers render it with user_submitted.ReportSubmit.
func (c *Client) SubmitUserIssues(accessToken string, datasetID int,
	body api.UserSubmittedIssueCreateSchema,
) (*api.UserSubmittedIssueCreateResponseSchema, error) {
	apiClient, err := c.newUserIssueClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.SubmitUserSubmittedAlertsWithResponse(
		context.Background(), c.organizationID, datasetID,
		&api.SubmitUserSubmittedAlertsParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "submit alert failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "submit alert failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// RetractUserIssues withdraws a user-submitted alert from the named rows.
//
// Retract is NOT resolve: it means "I flagged the wrong cell", not "this is
// fixed". It is keyed by (issue_item_id, row_ids) rather than by the per-row
// record UUID.
func (c *Client) RetractUserIssues(accessToken string, datasetID int,
	body api.UserSubmittedIssueRetractSchema,
) (*api.UserSubmittedIssueRetractResponseSchema, error) {
	apiClient, err := c.newUserIssueClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.RetractUserSubmittedAlertsWithResponse(
		context.Background(), c.organizationID, datasetID,
		&api.RetractUserSubmittedAlertsParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "retract alert failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "retract alert failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// newUserIssueClient is the standard construction the other methods on this
// client inline; it is a helper here only because the two verbs above would
// otherwise spell it twice.
func (c *Client) newUserIssueClient(accessToken string) (*client.Client, error) {
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
