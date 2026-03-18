package dataset_rules

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
	"github.com/qlustered/qctl/internal/org"
)

// Client handles HTTP requests for dataset rule operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new dataset rules client
func NewClient(baseURL string, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgID,
		verbosity:      verbosity,
		timeout:        60 * time.Second,
	}, nil
}

// GetDatasetRulesParams holds parameters for listing dataset rules
type GetDatasetRulesParams struct {
	SearchQuery  *string
	InstanceName *string
	OrderBy      string
	Page         int
	Limit        int
	Reverse      bool
}

// GetDatasetRules retrieves the list of dataset rules for a table
func (c *Client) GetDatasetRules(accessToken string, datasetID int, params GetDatasetRulesParams) (*DatasetRuleList, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	apiParams := &api.GetDatasetRulesListParams{
		SearchQuery:  params.SearchQuery,
		InstanceName: params.InstanceName,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.DatasetRuleOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetDatasetRulesListWithResponse(context.Background(), c.organizationID, datasetID, apiParams)
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

// GetDatasetRuleDetail retrieves the detail of a specific dataset rule
func (c *Client) GetDatasetRuleDetail(accessToken string, datasetRuleID string) (*DatasetRuleDetail, error) {
	ruleUUID, err := uuid.Parse(datasetRuleID)
	if err != nil {
		return nil, fmt.Errorf("invalid dataset rule ID: %w", err)
	}

	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDatasetRuleDetailWithResponse(
		context.Background(), c.organizationID, ruleUUID, &api.GetDatasetRuleDetailParams{},
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

// ResolveDatasetRuleID resolves a user-provided name or ID to a dataset rule UUID.
// Full UUIDs are returned immediately without an API call. Name inputs use the
// server-side instance_name filter for an exact match. UUID-like short IDs fall
// back to listing all rules for prefix matching.
func (c *Client) ResolveDatasetRuleID(accessToken string, datasetID int, input string) (string, error) {
	// Fast path: full UUID
	resolved, err := ResolveDatasetRule(nil, input)
	if err == nil {
		return resolved.ID, nil
	}

	// Name-based lookup: use server-side instance_name filter
	if !org.IsUUIDLike(input) {
		resp, err := c.GetDatasetRules(accessToken, datasetID, GetDatasetRulesParams{
			InstanceName: &input,
		})
		if err != nil {
			return "", fmt.Errorf("failed to list table rules for resolution: %w", err)
		}
		if len(resp.Results) == 1 {
			return resp.Results[0].ID.String(), nil
		}
		if len(resp.Results) > 1 {
			resolved, err := ResolveDatasetRule(resp.Results, input)
			if err != nil {
				return "", err
			}
			return resolved.ID, nil
		}
		return "", fmt.Errorf("no table-rule found matching '%s'.\n\nHint: Use 'qctl get table-rules --table <id>' to see all table rules.", input)
	}

	// UUID-like short ID: list all and prefix-match
	resp, err := c.GetDatasetRules(accessToken, datasetID, GetDatasetRulesParams{Limit: 1000})
	if err != nil {
		return "", fmt.Errorf("failed to list table rules for resolution: %w", err)
	}
	resolved, err = ResolveDatasetRule(resp.Results, input)
	if err != nil {
		return "", err
	}
	return resolved.ID, nil
}

// PatchDatasetRule patches a dataset rule's mutable fields
func (c *Client) PatchDatasetRule(accessToken string, datasetID int, datasetRuleID string, req api.PatchDatasetRuleJSONRequestBody) (*DatasetRuleDetail, error) {
	ruleUUID, err := uuid.Parse(datasetRuleID)
	if err != nil {
		return nil, fmt.Errorf("invalid dataset rule ID: %w", err)
	}

	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.PatchDatasetRuleWithResponse(
		context.Background(), c.organizationID, datasetID, ruleUUID, &api.PatchDatasetRuleParams{}, req,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "patch failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "patch failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// InstantiateRule instantiates a rule revision onto a dataset, creating a new dataset rule
func (c *Client) InstantiateRule(accessToken string, ruleRevisionID string, req api.InstantiateRuleJSONRequestBody) (*DatasetRuleDetail, error) {
	revisionUUID, err := uuid.Parse(ruleRevisionID)
	if err != nil {
		return nil, fmt.Errorf("invalid rule revision ID: %w", err)
	}

	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.InstantiateRuleWithResponse(
		context.Background(), c.organizationID, revisionUUID, &api.InstantiateRuleParams{}, req,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "instantiate failed")
	}

	if resp.StatusCode() != http.StatusCreated {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "instantiate failed")
	}

	if resp.JSON201 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON201, nil
}
