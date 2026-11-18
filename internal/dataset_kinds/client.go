package dataset_kinds //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/org"
)

// Client handles HTTP requests for dataset kind operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
	// listLimit is the first-page size of an exhaustive table-kind listing;
	// tests lower it to put a page boundary inside a small catalog.
	listLimit int
}

// NewClient creates a new dataset kinds client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
		listLimit:      500,
	}
}

// GetDatasetKindsParams holds parameters for listing dataset kinds.
type GetDatasetKindsParams struct {
	SearchQuery    *string
	IncludeBuiltin *bool
	OrderBy        string
	Page           int
	Limit          int
	Reverse        bool
}

// GetDatasetKinds retrieves the list of dataset kinds.
func (c *Client) GetDatasetKinds(accessToken string, params GetDatasetKindsParams) (*DatasetKindsPage, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	apiParams := &api.GetProblemDomainsParams{
		SearchQuery:    params.SearchQuery,
		IncludeBuiltin: params.IncludeBuiltin,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.ProblemDomainOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		reversed := true
		apiParams.Reverse = &reversed
	}

	resp, err := apiClient.API.GetProblemDomainsWithResponse(context.Background(), c.organizationID, apiParams)
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

// ErrIncompleteListing reports a table-kind listing that returned fewer
// distinct kinds than the total the server counted. Resolution refuses it
// rather than decide uniqueness from a partial inventory.
var ErrIncompleteListing = errors.New("incomplete table kind listing")

// getAllDatasetKinds retrieves every table kind matching params, deduplicated
// by ID.
func (c *Client) getAllDatasetKinds(accessToken string, params GetDatasetKindsParams) ([]DatasetKindTiny, error) {
	if params.Limit <= 0 {
		params.Limit = 500
	}
	if params.OrderBy == "" {
		params.OrderBy = "slug"
	}
	params.Page = 1

	resp, err := c.GetDatasetKinds(accessToken, params)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch table kinds: %w", err)
	}
	if resp.TotalRows == nil {
		// A backend without TotalRows gives nothing to size a request to or
		// verify against; Next is the only continuation signal it offers.
		return c.followDatasetKindPages(accessToken, params, resp)
	}
	// Before Atlas c702b9f1f, plain-column OFFSET sorting had no ID
	// tiebreaker, so tied slugs could repeat across pages. Newer builds
	// append ID, but a roomy first page also avoids the count/select race.
	// Accept only a page with at least TotalRows distinct kinds and room
	// below its limit: an insertion between Atlas's count and select can
	// otherwise push a row off an exactly full page. A capped or changing
	// listing fails after datasetKindListAttempts requests.
	for attempt := 1; ; attempt++ {
		kinds := distinctDatasetKinds(nil, resp.Results)
		if len(resp.Results) < params.Limit && len(kinds) >= *resp.TotalRows {
			return kinds, nil
		}
		if attempt == datasetKindListAttempts {
			return nil, fmt.Errorf("%w: the server counted %d matching table kinds but returned %d (%d distinct) with limit %d; retry, or pass the table kind's full UUID",
				ErrIncompleteListing, *resp.TotalRows, len(resp.Results), len(kinds), params.Limit)
		}

		params.Limit = max(*resp.TotalRows, len(resp.Results)) + 1
		resp, err = c.GetDatasetKinds(accessToken, params)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch all table kinds with limit %d: %w", params.Limit, err)
		}
		if resp.TotalRows == nil {
			return nil, fmt.Errorf("%w: the server stopped reporting a total", ErrIncompleteListing)
		}
	}
}

// datasetKindListAttempts bounds how many first-page requests
// getAllDatasetKinds makes before refusing a listing that never comes back
// complete.
const datasetKindListAttempts = 3

// followDatasetKindPages continues a listing by Next for backends that do not
// report TotalRows. first is the already-fetched page 1.
func (c *Client) followDatasetKindPages(accessToken string, params GetDatasetKindsParams, first *DatasetKindsPage) ([]DatasetKindTiny, error) {
	kinds := distinctDatasetKinds(nil, first.Results)
	resp := first
	for page := 2; resp.Next != nil && len(resp.Results) > 0; page++ {
		params.Page = page
		var err error
		resp, err = c.GetDatasetKinds(accessToken, params)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch table kinds page %d: %w", page, err)
		}
		kinds = distinctDatasetKinds(kinds, resp.Results)
	}
	return kinds, nil
}

// distinctDatasetKinds appends to kinds each of more whose ID kinds does not
// already hold.
func distinctDatasetKinds(kinds, more []DatasetKindTiny) []DatasetKindTiny {
	seen := make(map[uuid.UUID]bool, len(kinds)+len(more))
	for _, kind := range kinds {
		seen[kind.ID] = true
	}
	for _, kind := range more {
		if !seen[kind.ID] {
			seen[kind.ID] = true
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// GetDatasetKind retrieves a single dataset kind by ID, including its concepts.
func (c *Client) GetDatasetKind(accessToken string, kindID openapi_types.UUID) (*DatasetKindWithConcepts, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetOneProblemDomainWithResponse(context.Background(), c.organizationID, kindID, &api.GetOneProblemDomainParams{})
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

// BySlug reads a table kind by its exact slug.
func (c *Client) BySlug(token, slug string) (*DatasetKindWithConcepts, error) {
	kinds, err := c.getAllDatasetKinds(token, GetDatasetKindsParams{Limit: c.listLimit, OrderBy: "slug"})
	if err != nil {
		return nil, fmt.Errorf("list table kinds: %w", err)
	}
	var match *DatasetKindTiny
	for i := range kinds {
		if kinds[i].Slug != slug {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("multiple table kinds have slug %q", slug)
		}
		match = &kinds[i]
	}
	if match == nil {
		return nil, nil
	}
	return c.GetDatasetKind(token, match.ID)
}

// Apply sends a prepared create or patch request.
func (c *Client) Apply(token string, plan *Plan) (*DatasetKindWithConcepts, error) {
	body, err := plan.RequestJSON()
	if err != nil {
		return nil, fmt.Errorf("encode table kind request: %w", err)
	}
	apiClient, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: client.ResolveTimeout(c.timeout), Verbosity: c.verbosity})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	if plan.Create {
		resp, err := apiClient.API.CreateProblemDomainWithBodyWithResponse(context.Background(), c.organizationID, &api.CreateProblemDomainParams{}, "application/json", bytes.NewReader(body))
		if err != nil {
			return nil, wrapAPIError(apiClient, err, "failed to create table kind")
		}
		if resp.StatusCode() != http.StatusCreated {
			return nil, comments.HandleError(resp.StatusCode(), resp.Body, "failed to create table kind")
		}
		if resp.JSON201 == nil {
			return nil, fmt.Errorf("unexpected empty create table kind response")
		}
		return resp.JSON201, nil
	}
	resp, err := apiClient.API.PatchProblemDomainWithBodyWithResponse(context.Background(), c.organizationID, plan.ID, &api.PatchProblemDomainParams{}, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "failed to update table kind")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, "failed to update table kind")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty update table kind response")
	}
	return resp.JSON200, nil
}

// DeleteDatasetKind deletes a table kind by ID.
func (c *Client) DeleteDatasetKind(accessToken string, kindID openapi_types.UUID) error {
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: accessToken,
		Timeout: c.timeout, Verbosity: c.verbosity,
	})
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.DeleteProblemDomainWithResponse(context.Background(), c.organizationID, kindID, &api.DeleteProblemDomainParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, "failed to delete table kind")
	}
	if resp.StatusCode() != http.StatusNoContent {
		return comments.HandleError(resp.StatusCode(), resp.Body, "failed to delete table kind")
	}
	return nil
}

// RemoveFromDatasets removes a table kind from the requested tables.
func (c *Client) RemoveFromDatasets(accessToken string, kindID openapi_types.UUID, ids []int) (*api.RemoveProblemDomainFromDatasetsResponseSchema, error) {
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: accessToken,
		Timeout: c.timeout, Verbosity: c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	resp, err := apiClient.API.RemoveProblemDomainFromDatasetsWithResponse(context.Background(), c.organizationID, kindID,
		&api.RemoveProblemDomainFromDatasetsParams{}, api.RemoveProblemDomainFromDatasetsJSONRequestBody{DatasetIds: ids})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "failed to untag tables")
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusMultiStatus {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, "failed to untag tables")
	}
	result := resp.JSON200
	if resp.StatusCode() == http.StatusMultiStatus {
		result = resp.JSON207
	}
	if result == nil {
		return nil, fmt.Errorf("unexpected empty untag response")
	}
	return result, nil
}

// ImportResult is what the upsert did. The endpoint returns 201 on create and
// 200 on update (submit_problem_domain_from_file,
// capi/capi/views/problem_domain_view.py) — same body either way.
type ImportResult struct {
	Kind    *DatasetKindWithConcepts
	Created bool
}

// ImportFromConfig imports a dataset kind from a TOML or YAML config file content.
func (c *Client) ImportFromConfig(accessToken string, fileContent string, format DatasetKindImportFormat) (*ImportResult, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	req := api.SubmitProblemDomainFromFileJSONRequestBody{
		Content: fileContent,
		Format:  format,
	}

	resp, err := apiClient.API.SubmitProblemDomainFromFileWithResponse(context.Background(), c.organizationID, &api.SubmitProblemDomainFromFileParams{}, req)
	if err != nil {
		return nil, apiClient.HandleError(err, "import failed")
	}

	created := resp.StatusCode() == http.StatusCreated
	if !created && resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "import failed")
	}

	kind := resp.JSON200
	if created {
		kind = resp.JSON201
	}
	if kind == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return &ImportResult{Kind: kind, Created: created}, nil
}

// ResolveDatasetKindID resolves a user-provided input (slug, UUID, name) to a dataset kind UUID.
func (c *Client) ResolveDatasetKindID(accessToken, input string) (openapi_types.UUID, error) {
	// Full UUID fast path — no list call needed
	if len(input) == 36 && isFullUUID(input) {
		id, err := uuid.Parse(input)
		if err != nil {
			return openapi_types.UUID{}, fmt.Errorf("invalid UUID: %w", err)
		}
		return id, nil
	}

	includeBuiltin := true
	params := GetDatasetKindsParams{
		IncludeBuiltin: &includeBuiltin,
		Limit:          c.listLimit,
	}
	// The server search is a substring match on name and slug only, never
	// id, so sending a SHORT-ID would filter its own kind out. A UUID-like
	// input lists the whole visible catalog instead. ResolveDatasetKind still
	// tries an exact slug or name first, so those resolve as before; a partial
	// hex-only input such as "abc" now meets the ID-prefix step, which runs
	// before the substring step, over every kind rather than only those whose
	// name or slug contains it.
	if !org.IsUUIDLike(input) {
		params.SearchQuery = &input
	}
	kinds, err := c.getAllDatasetKinds(accessToken, params)
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("failed to list table kinds: %w", err)
	}

	resolved, err := ResolveDatasetKind(kinds, input)
	if err != nil {
		return openapi_types.UUID{}, err
	}

	id, err := uuid.Parse(resolved.ID)
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("invalid UUID: %w", err)
	}

	return id, nil
}

// FormatForFile determines the import format from a file extension.
func FormatForFile(filePath string) (DatasetKindImportFormat, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".toml":
		return api.Toml, nil
	case ".yaml", ".yml":
		return api.Yaml, nil
	default:
		return "", fmt.Errorf("unsupported file type %q (only .toml, .yaml, .yml are supported)", ext)
	}
}
