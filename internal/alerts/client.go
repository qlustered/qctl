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
		organizationID: orgUUID,
		verbosity:      verbosity,
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
	IssueStats        = api.IssueStatsSchema
)

// AlertsListResponse is an alias for AlertsPage (kept for backward compatibility)
type AlertsListResponse = AlertsPage

// GetAlertsParams holds parameters for listing alerts
type GetAlertsParams struct {
	// Filtering (pointers first for alignment)
	Resolved              *bool
	DatasetID             *int
	SeriesID              *openapi_types.UUID
	IsRowLevel            *bool
	ResolvableByUser      *bool
	ResolveAfterMigration *bool
	SearchQuery           *string
	// IssueType narrows to one alert type. `get alerts --issue-type` exposes it
	// as a passthrough filter, and `describe table` uses it to find the
	// severe_rule_bug blocker without pulling the table's whole alert list.
	IssueType *api.AlertType
	// ExcludeIssueTypes leaves out the alerts of these types; empty sends no
	// filter.
	ExcludeIssueTypes []api.AlertType
	// DatasetRuleID narrows to the alerts one table rule raised. The server
	// then drops its is_row_level=false and resolve_after_migration=false
	// defaults.
	DatasetRuleID *openapi_types.UUID
	// StoredItemID narrows to the row-level alerts holding live quarantined
	// rows of one file, and makes each Count that file's exact row count.
	StoredItemID *int
	// HasChangeProposal narrows to issues held (true) or not held (false) by
	// a change proposal.
	HasChangeProposal *bool
	// ChangeProposalStates narrows to issues held by a proposal in one of
	// these states; empty sends no filter.
	ChangeProposalStates []api.ChangeProposalState

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
		SeriesID:              params.SeriesID,
		IsRowLevel:            params.IsRowLevel,
		ResolvableByUser:      params.ResolvableByUser,
		ResolveAfterMigration: params.ResolveAfterMigration,
		SearchQuery:           params.SearchQuery,
		IssueType:             params.IssueType,
		StoredItemID:          params.StoredItemID,
		HasChangeProposal:     params.HasChangeProposal,
		DatasetRuleID:         params.DatasetRuleID,
	}
	if len(params.ChangeProposalStates) > 0 {
		apiParams.ChangeProposalState = &params.ChangeProposalStates
	}
	if len(params.ExcludeIssueTypes) > 0 {
		apiParams.ExcludeIssueTypes = &params.ExcludeIssueTypes
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

	preserveConflictNulls(resp.JSON200, resp.Body)
	return resp.JSON200, nil
}

// GetAlertStats retrieves the row statistics of a single alert.
func (c *Client) GetAlertStats(accessToken string, alertID int) (*IssueStats, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetAlertStatsWithResponse(context.Background(), c.organizationID, alertID, &api.GetAlertStatsParams{})
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
