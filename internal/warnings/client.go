package warnings

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

// Client handles HTTP requests for warning operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new warnings client
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
	WarningTiny    = api.WarningTinySchema
	WarningFull    = api.WarningSchema
	UserInfo       = api.UserInfoTinySchema
	WarningsPage   = api.WarningsListSchema
	IssueTypeEnum  = api.IssueTypeEnum
	WarningAction  = api.WarningAction
	PaginationInfo = api.PaginationSchema
)

// WarningsListResponse is an alias for WarningsPage (kept for backward compatibility)
type WarningsListResponse = WarningsPage

// GetWarningsParams holds parameters for listing warnings
type GetWarningsParams struct {
	// Filtering (pointers first for alignment)
	Resolved          *bool
	DatasetID         *int
	DataSourceModelID *int
	SearchQuery       *string

	// Pagination (internal)
	OrderBy string
	Page    int
	Limit   int // Chunk size per request
	Reverse bool
}

// GetWarnings retrieves the list of warnings using the generated client
func (c *Client) GetWarnings(accessToken string, params GetWarningsParams) (*WarningsPage, error) {
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
	apiParams := &api.GetWarningsParams{
		Resolved:          params.Resolved,
		DatasetID:         params.DatasetID,
		DataSourceModelID: params.DataSourceModelID,
		SearchQuery:       params.SearchQuery,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.WarningOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetWarningsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetWarning retrieves a single warning by ID using the generated client
func (c *Client) GetWarning(accessToken string, warningID int) (*WarningFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetWarningWithResponse(context.Background(), c.organizationID, warningID, &api.GetWarningParams{})
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
