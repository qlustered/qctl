package alerts

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

// Client handles HTTP requests for alert operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new alerts client
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
	AlertTiny         = api.AlertTinySchema
	AlertFull         = api.AlertSchema
	UserInfo          = api.UserInfoTinySchema
	AlertsPage        = api.AlertsListSchema
	StoredItemToAlert = api.StoredItemToAlertSchema
	AlertType         = api.AlertType
	PaginationSchema  = api.PaginationSchema
)

// AlertsListResponse is an alias for AlertsPage (kept for backward compatibility)
type AlertsListResponse = AlertsPage

// GetAlertsParams holds parameters for listing alerts
type GetAlertsParams struct {
	// Filtering (pointers first for alignment)
	Resolved              *bool
	DatasetID             *int
	IsRowLevel            *bool
	ResolvableByUser      *bool
	ResolveAfterMigration *bool
	SearchQuery           *string

	// Pagination (internal)
	OrderBy string
	Page    int
	Limit   int // Chunk size per request
	Reverse bool
}

// GetAlerts retrieves the list of alerts using the generated client
func (c *Client) GetAlerts(accessToken string, params GetAlertsParams) (*AlertsPage, error) {
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
	apiParams := &api.GetAlertsParams{
		Resolved:              params.Resolved,
		DatasetID:             params.DatasetID,
		IsRowLevel:            params.IsRowLevel,
		ResolvableByUser:      params.ResolvableByUser,
		ResolveAfterMigration: params.ResolveAfterMigration,
		SearchQuery:           params.SearchQuery,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.AlertsOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetAlertsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetAlert retrieves a single alert by ID using the generated client
func (c *Client) GetAlert(accessToken string, alertID int) (*AlertFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetAlertWithResponse(context.Background(), c.organizationID, alertID, &api.GetAlertParams{})
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
