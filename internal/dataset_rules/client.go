package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/org"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
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

// getAllDatasetRules retrieves every table-rule page matching params. The
// table-rule list uses ordered pagination, where Atlas reports TotalRows but
// does not populate Next, so exhaustive callers advance Page by count.
func (c *Client) getAllDatasetRules(accessToken string, datasetID int, params GetDatasetRulesParams) ([]DatasetRuleTiny, error) {
	if params.Limit <= 0 {
		params.Limit = 1000
	}
	if params.OrderBy == "" {
		// Position is unique within a table, giving the page walk stable order.
		params.OrderBy = "position"
	}

	var rules []DatasetRuleTiny
	for page := 1; ; page++ {
		params.Page = page
		resp, err := c.GetDatasetRules(accessToken, datasetID, params)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch table rules page %d: %w", page, err)
		}
		rules = append(rules, resp.Results...)

		if resp.TotalRows != nil {
			if len(rules) >= *resp.TotalRows || len(resp.Results) == 0 {
				return rules, nil
			}
			continue
		}
		if resp.Next == nil || len(resp.Results) == 0 {
			return rules, nil
		}
	}
}

// ListAllDatasetRules is every rule on the table, in position order, across
// every page. Soft-deleted rules are included: the list route applies no state
// filter, and they still hold (and shift) positions.
func (c *Client) ListAllDatasetRules(accessToken string, datasetID int) ([]DatasetRuleTiny, error) {
	return c.getAllDatasetRules(accessToken, datasetID, GetDatasetRulesParams{OrderBy: "position"})
}

// ReorderDatasetRule sends one rule to newPosition, a STORED 1-based position
// value. The server clamps it into [1, max(position)] without an error, shifts
// every rule between the old and new positions by one, and answers
// {"result":"ok"} whether or not anything moved. Callers that want to report
// the outcome compute it with PlanMove first.
func (c *Client) ReorderDatasetRule(accessToken string, datasetID int, datasetRuleID string, newPosition int) error {
	ruleUUID, err := uuid.Parse(datasetRuleID)
	if err != nil {
		return fmt.Errorf("invalid dataset rule ID: %w", err)
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

	resp, err := apiClient.API.ReorderDatasetRuleWithResponse(
		context.Background(), c.organizationID, datasetID, ruleUUID, &api.ReorderDatasetRuleParams{},
		api.RuleReorderRequestSchema{NewPosition: newPosition},
	)
	if err != nil {
		return apiClient.HandleError(err, "move failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "move failed")
	}
	return nil
}

// GetDatasetRuleDetail retrieves the detail of a specific dataset rule. Numbers
// in its interface{} fields decode as jsonnum.Unmarshal decodes them.
func (c *Client) GetDatasetRuleDetail(accessToken string, datasetRuleID string) (*DatasetRuleDetail, error) {
	return c.GetDatasetRuleDetailContext(context.Background(), accessToken, datasetRuleID)
}

// GetDatasetRuleDetailContext is GetDatasetRuleDetail with ctx controlling the request.
func (c *Client) GetDatasetRuleDetailContext(ctx context.Context, accessToken string, datasetRuleID string) (*DatasetRuleDetail, error) {
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
		ctx, c.organizationID, ruleUUID, &api.GetDatasetRuleDetailParams{},
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

	var detail DatasetRuleDetail
	if err := jsonnum.Unmarshal(resp.Body, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode table rule response: %w", err)
	}
	return &detail, nil
}

// ResolveDatasetRuleID resolves a user-provided name or ID to a dataset rule UUID.
// Full UUIDs are returned immediately without an API call. Every other input
// first uses the server-side instance_name filter for an exact match, so a
// hex-only name wins over an identically spelled UUID prefix. UUID-like inputs
// with no exact name fall back to listing all rules for prefix matching.
func (c *Client) ResolveDatasetRuleID(accessToken string, datasetID int, input string) (string, error) {
	// Fast path: full UUID
	resolved, err := ResolveDatasetRule(nil, input)
	if err == nil {
		return resolved.ID, nil
	}

	// Exact-name lookup precedes UUID-prefix matching for every non-full UUID.
	resp, err := c.GetDatasetRules(accessToken, datasetID, GetDatasetRulesParams{
		InstanceName: &input,
		// Atlas applies exact equality and enforces one instance_name per
		// table, so later pages cannot contain another candidate.
		Limit: 1,
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

	if !org.IsUUIDLike(input) {
		//nolint:revive,staticcheck // Keep the established multi-line CLI error and Hint capitalization.
		return "", fmt.Errorf("no table-rule found matching '%s'.\n\nHint: Use 'qctl get table-rules --table-id <id>' to see all table rules.", input)
	}

	// UUID-like short ID: list all and prefix-match
	rules, err := c.getAllDatasetRules(accessToken, datasetID, GetDatasetRulesParams{Limit: 1000})
	if err != nil {
		return "", fmt.Errorf("failed to list table rules for resolution: %w", err)
	}
	resolved, err = ResolveDatasetRule(rules, input)
	if err != nil {
		return "", err
	}
	return resolved.ID, nil
}

// PatchOutcome is the result of a dataset-rule PATCH. Exactly one field is
// non-nil: Applied on 200 (patch fully applied), Staged on 202 (the backend
// staged params/state as a rule suggestion and applied the rest).
type PatchOutcome struct {
	Applied *DatasetRuleDetail
	Staged  *Suggestion
}

// PatchDatasetRule patches a dataset rule's mutable fields. The patch is a
// hand-built merge-patch map (see BuildPatchMap): the generated request
// schema serializes absent optional fields as explicit JSON null, which
// older backends misread as a broad reconfiguration, so the typed struct is
// bypassed and exactly the caller's keys go on the wire.
func (c *Client) PatchDatasetRule(accessToken string, datasetID int, datasetRuleID string, patch map[string]interface{}) (*PatchOutcome, error) {
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

	body, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("failed to encode patch request: %w", err)
	}

	resp, err := apiClient.API.PatchDatasetRuleWithBodyWithResponse(
		context.Background(), c.organizationID, datasetID, ruleUUID, &api.PatchDatasetRuleParams{},
		"application/json", bytes.NewReader(body),
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "patch failed")
	}

	switch resp.StatusCode() {
	case http.StatusOK:
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("unexpected empty response")
		}
		return &PatchOutcome{Applied: resp.JSON200}, nil
	case http.StatusAccepted:
		if resp.JSON202 == nil {
			return nil, fmt.Errorf("unexpected empty 202 response")
		}
		return &PatchOutcome{Staged: resp.JSON202}, nil
	default:
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "patch failed")
	}
}

// InstantiateOutcome is the result of an instantiate. Exactly one field is
// non-nil: Created on 201, Staged on 202 (a draft migration was open and the
// rule would have touched the schema, so the backend staged the whole
// instantiation as a rule suggestion instead of creating the rule).
type InstantiateOutcome struct {
	Created *DatasetRuleDetail
	Staged  *Suggestion
}

// InstantiateRule instantiates a rule revision onto a dataset. A 201 creates
// the dataset rule; a 202 means the backend accepted the request but staged
// the whole instantiation as a rule suggestion — an acceptance, not a
// failure. See InstantiateOutcome.
func (c *Client) InstantiateRule(accessToken string, ruleRevisionID string, req api.InstantiateRuleJSONRequestBody) (*InstantiateOutcome, error) {
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

	switch resp.StatusCode() {
	case http.StatusCreated:
		if resp.JSON201 == nil {
			return nil, fmt.Errorf("unexpected empty 201 response")
		}
		return &InstantiateOutcome{Created: resp.JSON201}, nil
	case http.StatusAccepted:
		if resp.JSON202 == nil {
			return nil, fmt.Errorf("unexpected empty 202 response")
		}
		return &InstantiateOutcome{Staged: resp.JSON202}, nil
	default:
		var header http.Header
		if resp.HTTPResponse != nil {
			header = resp.HTTPResponse.Header
		}
		return nil, apierror.HandleHTTPErrorWithHeader(resp.StatusCode(), resp.Body, header, "instantiate failed")
	}
}
