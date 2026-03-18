package cloud_sources

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// Client handles HTTP requests for cloud source operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new cloud sources client
func NewClient(baseURL string, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgID,
		verbosity:      verbosity,
		timeout:        30 * time.Second,
	}, nil
}

// Type aliases for generated types - use these directly
type (
	CloudSourceTiny   = api.DataSourceModelTinySchema
	CloudSourceFull   = api.DataSourceModelAllOptionalSchema
	CloudSourcesPage  = api.DataSourceModelListSchema
	DataSourceType    = api.DataSourceType
	DataSourceState   = api.DataSourceState
	DataSourceOrderBy = api.DataSourceModelOrderBy
	PaginationInfo    = api.PaginationSchema
)

// CloudSourcesListResponse is an alias for CloudSourcesPage (kept for backward compatibility)
type CloudSourcesListResponse = CloudSourcesPage

// GetCloudSourcesParams holds parameters for listing cloud sources
type GetCloudSourcesParams struct {
	States      []string // slice header: 24 bytes
	Start       *int
	End         *int
	DatasetID   *int
	SearchQuery *string
	OrderBy     string
	Page        int
	Limit       int // Chunk size per request
	Reverse     bool
}

// GetCloudSources retrieves the list of cloud sources using the generated client
func (c *Client) GetCloudSources(accessToken string, params GetCloudSourcesParams) (*CloudSourcesPage, error) {
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
	apiParams := &api.GetDataSourcesParams{
		DatasetID:   params.DatasetID,
		SearchQuery: params.SearchQuery,
	}

	if params.Start != nil {
		apiParams.Start = params.Start
	}
	if params.End != nil {
		apiParams.End = params.End
	}
	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.DataSourceModelOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}
	if len(params.States) > 0 {
		states := make([]api.DataSourceState, len(params.States))
		for i, s := range params.States {
			states[i] = api.DataSourceState(s)
		}
		apiParams.States = &states
	}

	resp, err := apiClient.API.GetDataSourcesWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetAllCloudSources fetches all cloud sources with auto-pagination
func (c *Client) GetAllCloudSources(
	accessToken string,
	params GetCloudSourcesParams,
	maxResults int, // 0 = unlimited
) ([]CloudSourceTiny, error) {
	var allResults []CloudSourceTiny
	page := 1
	chunkSize := params.Limit
	if chunkSize == 0 {
		chunkSize = 100 // Default chunk size
	}

	// Default to stable sort
	if params.OrderBy == "" {
		params.OrderBy = "id"
	}

	for {
		// Adjust limit for final page if maxResults set
		remainingSlots := maxResults - len(allResults)
		if maxResults > 0 && remainingSlots < chunkSize {
			chunkSize = remainingSlots
			if chunkSize <= 0 {
				break
			}
		}

		// Fetch page
		params.Page = page
		params.Limit = chunkSize

		resp, err := c.GetCloudSources(accessToken, params)
		if err != nil {
			// If we have partial results, return them with the error
			if len(allResults) > 0 {
				return allResults, fmt.Errorf("partial results (got %d): %w", len(allResults), err)
			}
			return nil, err
		}

		allResults = append(allResults, resp.Results...)

		// Check if we should continue
		if resp.Next == nil || len(resp.Results) == 0 {
			break // No more pages
		}

		if maxResults > 0 && len(allResults) >= maxResults {
			break // Limit reached
		}

		page++
	}

	return allResults, nil
}

// GetCloudSource retrieves a single cloud source by ID using the generated client
func (c *Client) GetCloudSource(accessToken string, cloudSourceID int) (*CloudSourceFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDataSourceWithResponse(context.Background(), c.organizationID, cloudSourceID, &api.GetDataSourceParams{})
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
