package errorincidents

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

// Client handles HTTP requests for error incident operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new error incidents client
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
	ErrorIncidentTiny = api.ErrorIncidentTinySchema
	ErrorIncidentFull = api.ErrorIncidentSchema
	ListPage          = api.ErrorIncidentsListSchema
)

// GetErrorIncidentsParams holds parameters for listing error incidents
type GetErrorIncidentsParams struct {
	// Filtering
	SearchQuery *string
	JobName     *string
	Module      *string
	Deleted     *bool

	// Sorting
	OrderBy string
	Reverse bool

	// Pagination
	Page  int
	Limit int // Chunk size per request
}

// GetErrorIncidents retrieves the list of error incidents using the generated client
func (c *Client) GetErrorIncidents(accessToken string, params GetErrorIncidentsParams) (*ListPage, error) {
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
	apiParams := &api.GetErrorsParams{
		SearchQuery: params.SearchQuery,
		JobName:     params.JobName,
		Module:      params.Module,
		Deleted:     params.Deleted,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.ErrorIncidentOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetErrorsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetErrorIncident retrieves a single error incident by ID
func (c *Client) GetErrorIncident(accessToken string, errorID int) (*ErrorIncidentFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetErrorIncidentWithResponse(
		context.Background(),
		c.organizationID,
		errorID,
		&api.GetErrorIncidentParams{},
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

// DeleteErrorIncident deletes an error incident by ID
func (c *Client) DeleteErrorIncident(accessToken string, errorID int) error {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	ids := []int{errorID}
	resp, err := apiClient.API.DeleteErrorIncidentWithResponse(
		context.Background(),
		c.organizationID,
		&api.DeleteErrorIncidentParams{},
		api.DeleteErrorIncidentJSONRequestBody{
			Ids: &ids,
		},
	)
	if err != nil {
		return apiClient.HandleError(err, "delete failed")
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "delete failed")
	}

	return nil
}
