package cloud_sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// GetCorrectedRowCount counts manually fixed rows an upload may drop.
func (c *Client) GetCorrectedRowCount(accessToken string, cloudSourceID int, fileName string, periodKey *string) (int, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return 0, err
	}
	resp, err := apiClient.API.GetCorrectedRowCountWithResponse(
		context.Background(), c.organizationID, cloudSourceID,
		&api.GetCorrectedRowCountParams{FileName: fileName, PeriodKey: periodKey})
	if err != nil {
		return 0, wrapAPIError(apiClient, err, "manual-fix count failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return 0, handleError(resp.StatusCode(), resp.Body, "manual-fix count failed")
	}
	var result struct {
		CorrectedRowCount *int `json:"corrected_row_count"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return 0, fmt.Errorf("invalid manual-fix count: %w", err)
	}
	if result.CorrectedRowCount == nil || *result.CorrectedRowCount < 0 {
		return 0, fmt.Errorf("manual-fix count response has no non-negative corrected_row_count")
	}
	return *result.CorrectedRowCount, nil
}
