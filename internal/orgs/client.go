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
	qerrors "github.com/qlustered/qctl/internal/errors"
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
		organizationID: orgUUID,
		verbosity:      verbosity,
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

// GetOrgs retrieves organizations through the operations-only listing.
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

	// The org is carried by the path parameter below; the spec no longer
	// duplicates it as an organization_id query param.
	apiParams := &api.GetOrganizationsListParams{
		SearchQuery: params.SearchQuery,
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
		if code, body, ok := apiClient.LastCapturedError(); ok {
			return nil, organizationListError(code, body)
		}
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, organizationListError(resp.StatusCode(), resp.Body)
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

func organizationListError(status int, body []byte) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound {
		opError, _ := apierror.ParseErrorResponse(body, status)
		hint := "The organization-list endpoint is restricted to operations staff " +
			"and currently requires browser cookie authentication.\n" +
			"Run `qctl auth me` to see your Active Organizations; re-login will not enable this endpoint."
		opError.RecoveryHint = &hint
		return qerrors.FromHTTPStatusWithCause(status, opError, "request failed")
	}
	return apierror.HandleHTTPErrorFromBytes(status, body, "request failed")
}
