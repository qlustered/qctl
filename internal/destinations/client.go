package destinations

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

// Client handles HTTP requests for destination operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new destinations client
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
	DestinationTiny     = api.DestinationTinySchema
	DestinationFull     = api.DestinationSchemaFull
	ListPage            = api.DestinationsListSchema
	DatabaseNamesResult = api.ListOfStrResponseSchema
)

func stringValue(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func stringPtrValue(ptr *string) *string {
	if ptr == nil {
		return nil
	}
	val := *ptr
	return &val
}

// GetDestinationsParams holds parameters for listing destinations
type GetDestinationsParams struct {
	// Filtering
	Name        *string
	SearchQuery *string

	// Sorting
	OrderBy string
	Reverse bool

	// Pagination
	Page  int
	Limit int // Chunk size per request
}

// GetDestinations retrieves the list of destinations using the generated client
func (c *Client) GetDestinations(accessToken string, params GetDestinationsParams) (*ListPage, error) {
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
	apiParams := &api.GetDestinationsParams{
		Name:        params.Name,
		SearchQuery: params.SearchQuery,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.DestinationOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetDestinationsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetDestination retrieves a single destination by ID
func (c *Client) GetDestination(accessToken string, destinationID int) (*DestinationFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetOneDestinationWithResponse(
		context.Background(),
		c.organizationID,
		destinationID,
		&api.GetOneDestinationParams{},
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

// GetDestinationByName retrieves a destination by exact name match
// Returns (nil, nil) if not found, (destination, nil) if found, or (nil, error) on error
func (c *Client) GetDestinationByName(accessToken string, name string) (*DestinationFull, error) {
	// Use the name filter parameter
	params := GetDestinationsParams{
		Name: &name,
	}

	resp, err := c.GetDestinations(accessToken, params)
	if err != nil {
		return nil, err
	}

	// Filter for exact match since API might do partial matching
	for _, dest := range resp.Results {
		if dest.Name == name {
			// Get full details
			return c.GetDestination(accessToken, dest.ID)
		}
	}

	return nil, nil // Not found
}

// GetDestinationDatabaseNames retrieves available database names for a destination
func (c *Client) GetDestinationDatabaseNames(accessToken string, destinationID int) ([]string, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDestinationDatabaseNamesWithResponse(
		context.Background(),
		c.organizationID,
		destinationID,
		&api.GetDestinationDatabaseNamesParams{},
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

	return resp.JSON200.Results, nil
}

// CreateDestination creates a new destination
func (c *Client) CreateDestination(accessToken string, manifest *DestinationManifest) (*DestinationFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Map manifest to API request
	reqBody := api.DestiantionPostRequestSchema{
		Name:            &manifest.Metadata.Name,
		DestinationType: api.DestinationType(*manifest.Spec.Type),
		Host:            stringValue(manifest.Spec.Host),
		Port:            int(manifest.Spec.Port),
		DatabaseName:    stringValue(manifest.Spec.DatabaseName),
		User:            stringPtrValue(manifest.Spec.User),
		Password:        manifest.Spec.Password,
		ConnectTimeout:  manifest.Spec.ConnectTimeout,
		// RedirectPostSubmitTo is handled internally (nil) - never exposed to user
		RedirectPostSubmitTo: nil,
	}

	resp, err := apiClient.API.PostDestinationModelWithResponse(
		context.Background(),
		c.organizationID,
		&api.PostDestinationModelParams{},
		reqBody,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "create destination failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "create destination failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// UpdateDestination updates an existing destination
func (c *Client) UpdateDestination(accessToken string, destinationID int, manifest *DestinationManifest) (*DestinationFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Map manifest to API request
	port := int(manifest.Spec.Port)
	host := stringValue(manifest.Spec.Host)
	databaseName := stringValue(manifest.Spec.DatabaseName)
	reqBody := api.DestinationPatchRequestSchema{
		ID:             destinationID,
		Name:           &manifest.Metadata.Name,
		Host:           &host,
		Port:           &port,
		DatabaseName:   &databaseName,
		User:           stringPtrValue(manifest.Spec.User),
		Password:       manifest.Spec.Password,
		ConnectTimeout: manifest.Spec.ConnectTimeout,
		// RedirectPostSubmitTo is handled internally (nil) - never exposed to user
		RedirectPostSubmitTo: nil,
	}

	resp, err := apiClient.API.PatchDestinationModelWithResponse(
		context.Background(),
		c.organizationID,
		&api.PatchDestinationModelParams{},
		reqBody,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "update destination failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "update destination failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// Apply implements the idempotent apply operation
// It creates the destination if it doesn't exist, or updates it if it does
func (c *Client) Apply(accessToken string, manifest *DestinationManifest) (*ApplyResult, error) {
	// Validate manifest
	validationErrors := manifest.Validate()
	if len(validationErrors) > 0 {
		return nil, fmt.Errorf("manifest validation failed: %s", validationErrors[0].Error())
	}

	// Look up existing destination by name
	existing, err := c.GetDestinationByName(accessToken, manifest.Metadata.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to look up destination: %w", err)
	}

	if existing == nil {
		// Create new destination
		result, err := c.CreateDestination(accessToken, manifest)
		if err != nil {
			return nil, err
		}
		return &ApplyResult{
			Status:  "applied",
			Name:    result.Name,
			ID:      result.ID,
			Action:  "created",
			Message: "destination created successfully",
		}, nil
	}

	// Update existing destination
	result, err := c.UpdateDestination(accessToken, existing.ID, manifest)
	if err != nil {
		return nil, err
	}
	return &ApplyResult{
		Status:  "applied",
		Name:    result.Name,
		ID:      result.ID,
		Action:  "updated",
		Message: "destination updated successfully",
	}, nil
}

// CountDestinationsByName counts destinations matching the exact name
// Used to detect integrity violations (multiple destinations with same name)
func (c *Client) CountDestinationsByName(accessToken string, name string) (int, error) {
	params := GetDestinationsParams{
		Name: &name,
	}

	resp, err := c.GetDestinations(accessToken, params)
	if err != nil {
		return 0, err
	}

	// Count exact matches
	count := 0
	for _, dest := range resp.Results {
		if dest.Name == name {
			count++
		}
	}

	return count, nil
}
