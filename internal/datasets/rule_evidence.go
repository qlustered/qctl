package datasets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// RowRuleEvidence is the raw backend rule-evidence response for one row: every
// run that produced a verdict for it, newest run first.
type RowRuleEvidence = api.RowRuleEvidenceSchema

// GetRowRuleEvidence reads every rule verdict recorded for one row of a table.
//
// The route takes no is_bad_data and no query parameters at all: the backend
// resolves the id against the CLEAN table first and the QUARANTINED table
// second (atlas cettings/cettings/controllers/data_controller/rule_evidence.py
// resolve_live_row_signature), so one id reaches either side. Both probes
// exclude tombstoned rows, so a deleted row is a 404 rather than an empty
// result.
//
// A 200 with no results is a real answer, not an error: the dataset may simply
// have no <table>_rule_verdicts table yet.
func (c *Client) GetRowRuleEvidence(accessToken string, datasetID, rowID int) (*RowRuleEvidence, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetRowRuleEvidenceWithResponse(
		context.Background(), c.organizationID, datasetID, rowID, &api.GetRowRuleEvidenceParams{})
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
