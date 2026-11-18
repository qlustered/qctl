package dataset_rules //nolint:revive // Public package name matches the resource domain.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// RowFilterCount reads the server's count of the table's rows the rule's
// stored row filter keeps.
func (c *Client) RowFilterCount(token string, tableID int, ruleID string) (*api.RowFilterCountSchema, error) {
	id, err := uuid.Parse(ruleID)
	if err != nil {
		return nil, fmt.Errorf("invalid table rule ID: %w", err)
	}
	cl, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: c.timeout, Verbosity: c.verbosity})
	if err != nil {
		return nil, err
	}
	resp, err := cl.API.CountRowsKeptByDatasetRuleFilterWithResponse(context.Background(), c.organizationID, tableID, id, &api.CountRowsKeptByDatasetRuleFilterParams{})
	if err != nil {
		return nil, cl.HandleError(err, "row filter count failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "row filter count failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty row filter count")
	}
	return resp.JSON200, nil
}
