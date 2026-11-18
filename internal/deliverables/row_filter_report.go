package deliverables

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/row_filters"
)

// RowFilterReportResult is a sheet's row filter report and the original response.
type RowFilterReportResult struct {
	row_filters.Report
	Body json.RawMessage `json:"-"`
}

// SheetRowFilterReport reads the row filter report of a revision's sheet
// binding, sending periodKey as period_key unless it is empty.
func (c *Client) SheetRowFilterReport(token string, revisionID, sheetBindingID uuid.UUID, periodKey string) (*RowFilterReportResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	params := &api.GetDeliverableSheetRowFilterReportParams{}
	if periodKey != "" {
		params.PeriodKey = &periodKey
	}
	resp, err := apiClient.API.GetDeliverableSheetRowFilterReportWithResponse(context.Background(), c.orgID, revisionID, sheetBindingID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "row filter report failed", "get_deliverable_sheet_row_filter_report")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "row filter report failed", "get_deliverable_sheet_row_filter_report")
	}
	var result RowFilterReportResult
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty row filter report response")
	}
	if err := jsonnum.Unmarshal(resp.Body, &result.Report); err != nil {
		return nil, fmt.Errorf("decode row filter report: %w", err)
	}
	result.Body = resp.Body
	return &result, nil
}
