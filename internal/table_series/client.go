// Package table_series manages table series and their members.
package table_series

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// Series is a table series as the API returns it.
type Series api.TableSeriesSchema

type Member = api.TableSeriesMemberSchema

func seriesList(items []api.TableSeriesSchema) []Series {
	out := make([]Series, len(items))
	for i := range items {
		out[i] = Series(items[i])
	}
	return out
}

// Client manages table series in one organization.
type Client struct {
	baseURL   string
	orgID     uuid.UUID
	verbosity int
}

// NewClient creates a table series client.
func NewClient(baseURL, orgID string, verbosity int) *Client {
	id, _ := uuid.Parse(orgID)
	return &Client{baseURL: baseURL, orgID: id, verbosity: verbosity}
}

func (c *Client) newAPIClient(token string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: token,
		Timeout: client.ResolveTimeout(0), Verbosity: c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	return apiClient, nil
}

// List reads all table series with their members.
func (c *Client) List(token string) ([]Series, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ListTableSeriesWithResponse(context.Background(), c.orgID, &api.ListTableSeriesParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list table series failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list table series failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty table series list response")
	}
	return seriesList(*resp.JSON200), nil
}

// Get reads one table series by UUID.
func (c *Client) Get(token string, id uuid.UUID) (*Series, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetTableSeriesWithResponse(context.Background(), c.orgID, id, &api.GetTableSeriesParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get table series failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get table series failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty table series response")
	}
	return (*Series)(resp.JSON200), nil
}

// ListSubmissionPeriods reads the caller's accessible submission periods.
func (c *Client) ListSubmissionPeriods(token string, id uuid.UUID) ([]api.TableSeriesSubmissionPeriodSchema, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ListSubmissionPeriodsWithResponse(context.Background(), c.orgID, id, &api.ListSubmissionPeriodsParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list submission periods failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list submission periods failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty submission periods response")
	}
	return *resp.JSON200, nil
}
