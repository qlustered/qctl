package table_series

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// Suggest asks the server for the series name, frequency and period that request suggests.
func (c *Client) Suggest(token string, request api.TableSeriesSuggestionRequestSchema) (*api.TableSeriesSuggestionSchema, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.SuggestTableSeriesWithResponse(context.Background(), c.orgID, &api.SuggestTableSeriesParams{}, request)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "suggest table series failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "suggest table series failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty table series suggestion response")
	}
	return resp.JSON200, nil
}

// SuggestPeriod asks the server for the period fileName names, read in
// frequency when frequency is non-nil.
func (c *Client) SuggestPeriod(token, fileName string, frequency *api.SeriesFrequency) (*api.TableSeriesPeriodSuggestionSchema, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	request := api.TableSeriesPeriodSuggestionRequestSchema{FileName: fileName, Frequency: frequency}
	resp, err := apiClient.API.SuggestTableSeriesPeriodWithResponse(context.Background(), c.orgID, &api.SuggestTableSeriesPeriodParams{}, request)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "suggest table series period failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "suggest table series period failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty table series period suggestion response")
	}
	return resp.JSON200, nil
}

// SuggestionColumns are the default columns for a series suggestion.
const SuggestionColumns = "series_name,frequency,period_key,period_source,name,table_name"

// SuggestionDisplay is a series suggestion's table row.
type SuggestionDisplay struct {
	SeriesName   string `json:"series_name"`
	Frequency    string `json:"frequency"`
	PeriodKey    string `json:"period_key"`
	PeriodSource string `json:"period_source"`
	Name         string `json:"name"`
	TableName    string `json:"table_name"`
}

// SuggestionRow returns suggestion's table row, with "-" for a null value.
func SuggestionRow(suggestion *api.TableSeriesSuggestionSchema) SuggestionDisplay {
	text := func(value *string) string {
		if value == nil {
			return "-"
		}
		return *value
	}
	source := "-"
	if suggestion.PeriodSource != nil {
		source = string(*suggestion.PeriodSource)
	}
	return SuggestionDisplay{
		SeriesName: text(suggestion.SeriesName), Frequency: string(suggestion.Frequency),
		PeriodKey: text(suggestion.PeriodKey), PeriodSource: source,
		Name: text(suggestion.Name), TableName: text(suggestion.TableName),
	}
}
