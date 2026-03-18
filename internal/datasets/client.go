package datasets

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// Client handles HTTP requests for dataset operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new datasets client
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: openapi_types.UUID(orgUUID),
		verbosity:      verbosity,
		timeout:        30 * time.Second,
	}
}

// Type aliases for generated types - use these directly
type (
	DatasetTiny             = api.DataSetTinySchema
	DatasetFull             = api.DataSetSchemaFull
	DatasetsPage            = api.DatasetsListSchema
	DatasetStats            = api.DataSetMiniStatsSchema
	JobRunningCountRequest  = api.JobRunningCountRequestSchema
	JobRunningCountResponse = api.JobRunningCountResponseSchema
	DataSetState            = api.DataSetState
	DataLoadingProcess      = api.DataLoadingProcess
	MigrationPolicy         = api.MigrationPolicy
	UserInfoTiny            = api.UserInfoTinyDictSchema
	SettingsSchema          = api.SettingsSchema
)

// DatasetsListResponse is an alias for DatasetsPage (kept for backward compatibility)
type DatasetsListResponse = DatasetsPage

// DatasetStatsResponse is an alias for DatasetStats (kept for backward compatibility)
type DatasetStatsResponse = DatasetStats

// GetDatasetsParams holds parameters for listing datasets
type GetDatasetsParams struct {
	States          []string // slice header: 24 bytes
	DestinationID   *int
	DestinationName *string
	Name            *string
	SearchQuery     *string
	OrderBy         string
	Page            int
	Limit           int // Chunk size per request
	Reverse         bool
}

// ResolveID resolves a user-provided name or integer ID string to a dataset ID.
// If the input is an integer, it's used as the ID directly.
// Otherwise, it's treated as a name and looked up via the list API.
func (c *Client) ResolveID(accessToken, input string) (int, error) {
	// Fast path: integer ID
	if id, err := strconv.Atoi(input); err == nil {
		return id, nil
	}

	// Name lookup: fetch datasets filtered by name
	name := input
	result, err := c.GetDatasets(accessToken, GetDatasetsParams{
		Name:  &name,
		Limit: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to look up table by name: %w", err)
	}

	resolved, err := ResolveDataset(result.Results, input)
	if err != nil {
		return 0, err
	}

	return resolved.ID, nil
}

// GetDatasets retrieves the list of datasets using the generated client
func (c *Client) GetDatasets(accessToken string, params GetDatasetsParams) (*DatasetsPage, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Convert params to generated type
	apiParams := &api.GetDatasetsParams{
		DestinationID:   params.DestinationID,
		DestinationName: params.DestinationName,
		Name:            params.Name,
		SearchQuery:     params.SearchQuery,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.DataSetsOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	// Convert states to generated type
	if len(params.States) > 0 {
		states := make([]api.DataSetState, len(params.States))
		for i, s := range params.States {
			states[i] = api.DataSetState(s)
		}
		apiParams.States = &states
	}

	resp, err := apiClient.API.GetDatasetsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetDataset retrieves a single dataset by ID using the generated client
func (c *Client) GetDataset(accessToken string, datasetID int) (*DatasetFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDatasetWithResponse(context.Background(), c.organizationID, datasetID, &api.GetDatasetParams{})
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

// GetDatasetStats retrieves statistics for a dataset using the generated client
func (c *Client) GetDatasetStats(accessToken string, datasetID int) (*DatasetStats, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDatasetStatsWithResponse(context.Background(), c.organizationID, datasetID, &api.GetDatasetStatsParams{})
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

// GetDatasetJobActivity retrieves job activity for a dataset using the generated client
func (c *Client) GetDatasetJobActivity(accessToken string, datasetID int, lastUpdate *int) (*JobRunningCountResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	reqBody := api.JobRunningCountRequestSchema{
		LastUpdate: lastUpdate,
	}

	resp, err := apiClient.API.GetDatasetJobActivityWithResponse(
		context.Background(),
		c.organizationID,
		datasetID,
		&api.GetDatasetJobActivityParams{},
		reqBody,
	)
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

// GetJobActivity retrieves job activity globally using the generated client
func (c *Client) GetJobActivity(accessToken string, lastUpdate *int) (*JobRunningCountResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	reqBody := api.JobRunningCountRequestSchema{
		LastUpdate: lastUpdate,
	}

	resp, err := apiClient.API.GetJobActivityWithResponse(
		context.Background(),
		c.organizationID,
		&api.GetJobActivityParams{},
		reqBody,
	)
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
