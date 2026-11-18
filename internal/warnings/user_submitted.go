package warnings

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// SubmitUserIssues raises an ad-hoc, human-authored warning on named cells of
// named QUARANTINED rows.
//
// A warning does NOT hold the row: it is advisory only. Once the row's other
// issues clear, the row goes clean carrying the warning, and from then on the
// note can go stale — editing the flagged cell on a clean row does not update
// or clear it, because no producer revisits clean rows. An objection that must
// survive that is an alert, not a warning.
//
// The 200 is a PER-ROW partial success; callers render it with
// user_submitted.ReportSubmit.
func (c *Client) SubmitUserIssues(accessToken string, datasetID int,
	body api.UserSubmittedIssueCreateSchema,
) (*api.UserSubmittedIssueCreateResponseSchema, error) {
	apiClient, err := c.newUserIssueClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.SubmitUserSubmittedWarningsWithResponse(
		context.Background(), c.organizationID, datasetID,
		&api.SubmitUserSubmittedWarningsParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "submit warning failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "submit warning failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// RetractUserIssues withdraws a user-submitted warning from the named rows.
//
// Retract is NOT resolve: it means "I flagged the wrong cell", not "this is
// fixed". `retracted` describes the RECORD, not the row, and for warnings that
// gap is visible: a warning whose row has already left quarantine keeps its
// token and stays on display, even though the record is retracted and will not
// survive the next reprocess. The backend has no status to say that in — it
// logs it — so the command layer states it in --help instead.
func (c *Client) RetractUserIssues(accessToken string, datasetID int,
	body api.UserSubmittedIssueRetractSchema,
) (*api.UserSubmittedIssueRetractResponseSchema, error) {
	apiClient, err := c.newUserIssueClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.RetractUserSubmittedWarningsWithResponse(
		context.Background(), c.organizationID, datasetID,
		&api.RetractUserSubmittedWarningsParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "retract warning failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "retract warning failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// newUserIssueClient is the standard construction GetWarnings and GetWarning
// inline; it is a helper here only so the two verbs above do not spell it
// twice.
//
// These two deliberately do NOT introduce a warnings.APIError: three
// hand-rolled copies of that type already exist in the tree and a fourth is
// the thing to avoid. client.HandleError already falls back to the captured
// 4xx/5xx body, so the exit code and message survive without one.
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
