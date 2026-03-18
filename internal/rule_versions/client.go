package rule_versions

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

// Client handles HTTP requests for rule version operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new rule versions client
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

// RuleVersionSubmitRequest represents the request to submit rule version file content
// This is a compatibility wrapper around api.RuleRevisionSubmitRequestSchema
type RuleVersionSubmitRequest struct {
	Force    *bool  `json:"force,omitempty"`
	FileText string `json:"file_text"`
}

// RuleVersionSubmitResponse represents the response from rule version submission
// This is a compatibility wrapper around api.RuleRevisionSubmitResponseSchema
type RuleVersionSubmitResponse struct {
	Message    string     `json:"message"`     // Success message
	Added      [][]string `json:"added"`       // List of [rule_name, version] tuples
	NotChanged [][]string `json:"not_changed"` // List of [rule_name, version] tuples that were unchanged
}

// SubmitRuleVersion submits a rule version file for import using the generated client
func (c *Client) SubmitRuleVersion(accessToken string, req RuleVersionSubmitRequest) (*RuleVersionSubmitResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Convert to generated request type
	apiReq := api.SubmitRuleRevisionJSONRequestBody{
		FileText: req.FileText,
		Force:    req.Force,
	}

	resp, err := apiClient.API.SubmitRuleRevisionWithResponse(context.Background(), c.organizationID, &api.SubmitRuleRevisionParams{}, apiReq)
	if err != nil {
		return nil, apiClient.HandleError(err, "rule import failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "rule import failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	// Convert response to compatibility type
	result := &RuleVersionSubmitResponse{
		Message: resp.JSON200.Message,
	}

	// Convert Added from [][]interface{} to [][]string
	for _, added := range resp.JSON200.Added {
		var strTuple []string
		for _, item := range added {
			if s, ok := item.(string); ok {
				strTuple = append(strTuple, s)
			} else {
				strTuple = append(strTuple, fmt.Sprintf("%v", item))
			}
		}
		result.Added = append(result.Added, strTuple)
	}

	// Convert NotChanged from [][]interface{} to [][]string
	if resp.JSON200.NotChanged != nil {
		for _, nc := range *resp.JSON200.NotChanged {
			var strTuple []string
			for _, item := range nc {
				if s, ok := item.(string); ok {
					strTuple = append(strTuple, s)
				} else {
					strTuple = append(strTuple, fmt.Sprintf("%v", item))
				}
			}
			result.NotChanged = append(result.NotChanged, strTuple)
		}
	}

	return result, nil
}

// GetRuleRevisionsParams holds parameters for listing rule revisions
type GetRuleRevisionsParams struct {
	SearchQuery          *string
	OnlyDefault *bool
	StateFilter          *string
	HasUpgradeAvailable  *bool
	OrderBy              string
	Page                 int
	Limit                int
	Reverse              bool
}

// GetRuleRevisions retrieves the list of rule revisions
func (c *Client) GetRuleRevisions(accessToken string, params GetRuleRevisionsParams) (*RuleRevisionList, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	apiParams := &api.GetRuleRevisionListParams{
		SearchQuery:          params.SearchQuery,
		OnlyDefault: params.OnlyDefault,
	}

	if params.HasUpgradeAvailable != nil {
		apiParams.HasUpgradeAvailable = params.HasUpgradeAvailable
	}
	if params.StateFilter != nil {
		state := api.RuleState(*params.StateFilter)
		apiParams.StateFilter = &state
	}
	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.RuleRevisionOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetRuleRevisionListWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetRuleRevisionAllReleases retrieves all releases for a rule revision family
func (c *Client) GetRuleRevisionAllReleases(accessToken string, ruleRevisionID string) (*RuleRevisionsFamily, error) {
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

	resp, err := apiClient.API.GetRuleRevisionAllReleasesWithResponse(
		context.Background(), c.organizationID, revisionUUID, &api.GetRuleRevisionAllReleasesParams{},
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

// GetRuleRevisionDetails retrieves the full detail (including code) for a rule revision
func (c *Client) GetRuleRevisionDetails(accessToken string, ruleRevisionID string) (*RuleRevisionFull, error) {
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

	resp, err := apiClient.API.GetOneRuleRevisionDetailsWithResponse(
		context.Background(), c.organizationID, revisionUUID, &api.GetOneRuleRevisionDetailsParams{},
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

// RuleVersionUnsubmitResponse represents the response from rule version unsubmit
type RuleVersionUnsubmitResponse struct {
	Message  string               `json:"message"`
	Deleted  []UnsubmitResultItem `json:"deleted,omitempty"`
	NotFound []UnsubmitResultItem `json:"not_found,omitempty"`
	Skipped  []UnsubmitSkippedGroup `json:"skipped,omitempty"`
}

// PatchRuleRevision patches a rule revision's mutable fields
func (c *Client) PatchRuleRevision(accessToken string, ruleRevisionID string, req api.PatchRuleRevisionJSONRequestBody) (*api.PatchRuleRevisionResponseSchema, error) {
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

	resp, err := apiClient.API.PatchRuleRevisionWithResponse(
		context.Background(), c.organizationID, revisionUUID, &api.PatchRuleRevisionParams{}, req,
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

// DeleteRuleRevision deletes a rule revision by ID
func (c *Client) DeleteRuleRevision(accessToken string, ruleRevisionID string) error {
	revisionUUID, err := uuid.Parse(ruleRevisionID)
	if err != nil {
		return fmt.Errorf("invalid rule revision ID: %w", err)
	}

	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.DeleteRuleRevisionWithResponse(
		context.Background(), c.organizationID, revisionUUID, &api.DeleteRuleRevisionParams{},
	)
	if err != nil {
		return apiClient.HandleError(err, "delete failed")
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "delete failed")
	}

	return nil
}

// ResolveRuleID resolves a name, short ID, or full UUID to a rule revision UUID string.
// It requires a release filter when the rule has multiple releases (strict mode).
func (c *Client) ResolveRuleID(accessToken, input, release string) (string, error) {
	resolved, err := c.ResolveRuleFull(accessToken, input, release)
	if err != nil {
		return "", err
	}
	return resolved.ID, nil
}

// ResolveRuleIDAny resolves a name, short ID, or full UUID to any matching rule revision UUID.
// Unlike ResolveRuleID, it does NOT error when multiple releases exist — it picks the first match.
func (c *Client) ResolveRuleIDAny(accessToken, input string) (string, error) {
	// Fast path: full UUID doesn't need a list call
	resolved, err := ResolveRuleAny(nil, input)
	if err == nil {
		return resolved.ID, nil
	}

	// Need to list rules to resolve by name/prefix
	resp, err := c.GetRuleRevisions(accessToken, GetRuleRevisionsParams{
		Limit:       1000,
		OnlyDefault: BoolPtr(false),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list rules for resolution: %w", err)
	}

	resolved, err = ResolveRuleAny(resp.Results, input)
	if err != nil {
		return "", err
	}

	return resolved.ID, nil
}

// ResolveRuleFull resolves a name, short ID, or full UUID to a full ResolvedRule struct.
// It requires a release filter when the rule has multiple releases (strict mode).
func (c *Client) ResolveRuleFull(accessToken, input, release string) (*ResolvedRule, error) {
	// Fast path: full UUID doesn't need a list call
	resolved, err := ResolveRule(nil, input, release)
	if err == nil {
		return resolved, nil
	}

	// Need to list rules to resolve by name/prefix
	resp, err := c.GetRuleRevisions(accessToken, GetRuleRevisionsParams{
		Limit:       1000,
		OnlyDefault: BoolPtr(false),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list rules for resolution: %w", err)
	}

	return ResolveRule(resp.Results, input, release)
}

// UnsubmitRuleVersion unsubmits rule versions matching the provided source file
func (c *Client) UnsubmitRuleVersion(accessToken string, fileText string) (*RuleVersionUnsubmitResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	apiReq := api.UnsubmitRuleRevisionJSONRequestBody{
		FileText: fileText,
	}

	resp, err := apiClient.API.UnsubmitRuleRevisionWithResponse(
		context.Background(), c.organizationID, &api.UnsubmitRuleRevisionParams{}, apiReq,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "unsubmit failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "unsubmit failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	result := &RuleVersionUnsubmitResponse{
		Message: resp.JSON200.Message,
	}

	if resp.JSON200.Deleted != nil {
		result.Deleted = *resp.JSON200.Deleted
	}

	if resp.JSON200.NotFound != nil {
		result.NotFound = *resp.JSON200.NotFound
	}

	if resp.JSON200.Skipped != nil {
		result.Skipped = *resp.JSON200.Skipped
	}

	return result, nil
}
