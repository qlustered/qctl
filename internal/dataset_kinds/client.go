package dataset_kinds

import (
	"context"
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
)

// Client handles HTTP requests for dataset kind operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new dataset kinds client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: openapi_types.UUID(orgUUID),
		verbosity:      verbosity,
		timeout:        30 * time.Second,
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

	apiParams := &api.GetDatasetKindsParams{
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
		orderBy := api.DatasetKindOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetDatasetKindsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetDatasetKind retrieves a single dataset kind by ID, including its field kinds.
func (c *Client) GetDatasetKind(accessToken string, kindID openapi_types.UUID) (*DatasetKindWithFieldKinds, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetOneDatasetKindWithResponse(context.Background(), c.organizationID, kindID, &api.GetOneDatasetKindParams{})
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

// GetDatasetFieldKinds retrieves all field kinds for a dataset kind, with full detail.
func (c *Client) GetDatasetFieldKinds(accessToken string, kindID openapi_types.UUID) ([]DatasetFieldKindFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Fetch list with high limit to get all field kinds for this dataset kind
	limit := 500
	listParams := &api.GetDatasetFieldKindsParams{
		DatasetKindID: &kindID,
		Limit:         &limit,
	}

	listResp, err := apiClient.API.GetDatasetFieldKindsWithResponse(context.Background(), c.organizationID, listParams)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if listResp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(listResp.StatusCode(), listResp.Body, "request failed")
	}

	if listResp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	// Fetch full detail for each field kind (tiny schema lacks aliases)
	fieldKinds := make([]DatasetFieldKindFull, 0, len(listResp.JSON200.Results))
	for _, tiny := range listResp.JSON200.Results {
		detailResp, err := apiClient.API.GetOneDatasetFieldKindWithResponse(context.Background(), c.organizationID, tiny.ID, &api.GetOneDatasetFieldKindParams{})
		if err != nil {
			return nil, apiClient.HandleError(err, "request failed")
		}

		if detailResp.StatusCode() != http.StatusOK {
			return nil, apierror.HandleHTTPErrorFromBytes(detailResp.StatusCode(), detailResp.Body, "request failed")
		}

		if detailResp.JSON200 == nil {
			return nil, fmt.Errorf("unexpected empty response for field kind %s", tiny.ID.String())
		}

		fieldKinds = append(fieldKinds, *detailResp.JSON200)
	}

	return fieldKinds, nil
}

// ImportFromConfig imports a dataset kind from a TOML or YAML config file content.
func (c *Client) ImportFromConfig(accessToken string, fileContent string, format DatasetKindImportFormat) (*DatasetKindWithFieldKinds, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	req := api.SubmitDatasetKindFromFileJSONRequestBody{
		Content: fileContent,
		Format:  format,
	}

	resp, err := apiClient.API.SubmitDatasetKindFromFileWithResponse(context.Background(), c.organizationID, &api.SubmitDatasetKindFromFileParams{}, req)
	if err != nil {
		return nil, apiClient.HandleError(err, "import failed")
	}

	if resp.StatusCode() != http.StatusCreated {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "import failed")
	}

	if resp.JSON201 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON201, nil
}

// ResolveDatasetKindID resolves a user-provided input (slug, UUID, name) to a dataset kind UUID.
func (c *Client) ResolveDatasetKindID(accessToken, input string) (openapi_types.UUID, error) {
	// Full UUID fast path — no list call needed
	if len(input) == 36 && isFullUUID(input) {
		id, err := uuid.Parse(input)
		if err != nil {
			return openapi_types.UUID{}, fmt.Errorf("invalid UUID: %w", err)
		}
		return openapi_types.UUID(id), nil
	}

	includeBuiltin := true
	limit := 500
	resp, err := c.GetDatasetKinds(accessToken, GetDatasetKindsParams{
		IncludeBuiltin: &includeBuiltin,
		Limit:          limit,
	})
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("failed to list table kinds: %w", err)
	}

	resolved, err := ResolveDatasetKind(resp.Results, input)
	if err != nil {
		return openapi_types.UUID{}, err
	}

	id, err := uuid.Parse(resolved.ID)
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("invalid UUID: %w", err)
	}

	return openapi_types.UUID(id), nil
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
