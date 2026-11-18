package table_series

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
)

// ListUploadable reads the caller's series upload targets.
func (c *Client) ListUploadable(token string) ([]api.UploadableTableSeriesSchema, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ListUploadableTableSeriesWithResponse(context.Background(), c.orgID, &api.ListUploadableTableSeriesParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list uploadable table series failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list uploadable table series failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty uploadable table series response")
	}
	return *resp.JSON200, nil
}

// UploadableColumns are the default columns for series upload targets.
const UploadableColumns = "name,frequency,cloud_source_id,routing_mode,period_key,label,table_id"

// UploadableDisplay is a series upload target's table row.
type UploadableDisplay struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Frequency     string `json:"frequency"`
	CloudSourceID string `json:"cloud_source_id"`
	RoutingMode   string `json:"routing_mode"`
	PeriodKey     string `json:"period_key"`
	Label         string `json:"label"`
	TableID       string `json:"table_id"`
}

// UploadableRows returns one row per period, or one for a source with no periods.
func UploadableRows(items []api.UploadableTableSeriesSchema) []UploadableDisplay {
	rows := make([]UploadableDisplay, 0)
	for _, item := range items {
		base := UploadableDisplay{
			ID: item.ID.String(), Name: item.Name, Frequency: string(item.Frequency),
			CloudSourceID: strconv.Itoa(item.DataSourceModelID), RoutingMode: item.RoutingMode,
			PeriodKey: "-", Label: "-", TableID: "-",
		}
		if len(item.Periods) == 0 {
			rows = append(rows, base)
		}
		for _, period := range item.Periods {
			row := base
			row.PeriodKey, row.Label = period.PeriodKey, period.Label
			row.TableID = periodTableText(period.DatasetID)
			rows = append(rows, row)
		}
	}
	return rows
}
