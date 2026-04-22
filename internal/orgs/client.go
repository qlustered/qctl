package orgs

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

// Client handles HTTP requests for organization list operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new organizations client
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: openapi_types.UUID(orgUUID),
		verbosity:      verbosity,
		timeout:        30 * time.Second,
	}
}

// GetOrgsParams holds parameters for listing organizations
type GetOrgsParams struct {
	SearchQuery *string
	OrderBy     string
	Page        int
	Limit       int
	Reverse     bool
	ActiveOnly  bool
}

// GetOrgs retrieves the list of organizations accessible to the current user
func (c *Client) GetOrgs(accessToken string, params GetOrgsParams) (*OrgList, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	apiParams := &api.GetOrganizationsListParams{
		SearchQuery:    params.SearchQuery,
		OrganizationID: c.organizationID,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.OrganizationOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		reverse := true
		apiParams.Reverse = &reverse
	}
	if params.ActiveOnly {
		isActive := true
		apiParams.IsActive = &isActive
	}

	resp, err := apiClient.API.GetOrganizationsListWithResponse(context.Background(), c.organizationID, apiParams)
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
