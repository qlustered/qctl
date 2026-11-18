package stored_items

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/filedl"
	"github.com/qlustered/qctl/internal/fileul"
)

// Client handles HTTP requests for stored items (files) operations
type Client struct {
	thirdParty     bool
	httpClient     *http.Client
	baseURL        string
	organizationID openapi_types.UUID
	timeout        time.Duration
	verbosity      int
}

// transferTimeout is this package's own deadline: it moves whole files, so
// the 30 seconds an API call gets is not defensible here. It is a floor, not
// a ceiling -- an explicit --request-timeout still wins, in either direction
// (see client.ResolveTimeout).
const transferTimeout = 300 * time.Second

// NewClient creates a new stored items client.
//
// httpClient stays a plain *http.Client rather than a client.Doer for two
// reasons: filedl.ToFile takes one, and --retries has no business on a bulk
// transfer -- an upload is a PUT or POST, which the retry never repeats
// anyway, and a download streams straight to disk. Only the deadline is
// resolved.
func NewClient(baseURL string, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgID,
		httpClient: &http.Client{
			Timeout: client.ResolveTimeout(transferTimeout),
		},
		verbosity: verbosity,
		timeout:   transferTimeout,
	}, nil
}

// Type aliases for generated types - use these directly
type (
	StoredItemTiny                   = api.StoredItemTinySchema
	StoredItemFull                   = api.StoredItemFullSchema
	ThirdPartyStoredItem             = api.ThirdPartyStoredItemSchema
	StoredItemsPage                  = api.StoredItemsListSchema
	StoredItemURLResponse            = api.StoredItemURLSchema
	StoredItemPutRequest             = api.StoredItemPutRequestSchema
	StoredItemDeleteOrRecoverRequest = api.StoredItemDeleteOrRecoverRequestSchema
	FileTypes                        = api.FileTypes
	HTTPMethod                       = api.HTTPMethod
)

// StoredItemsListResponse is an alias for StoredItemsPage (kept for backward compatibility)
type StoredItemsListResponse = StoredItemsPage

// GetStoredItemsParams contains parameters for listing stored items
type GetStoredItemsParams struct {
	States            []string // slice header: 24 bytes
	DatasetID         *int
	DataSourceModelID *int
	SearchQuery       *string
	// IsTrashed selects deleted (true) or active (false) files. The server
	// applies it as an equality filter, so the two sets never come back
	// together; nil leaves the parameter off the request entirely and the
	// server's own default (active files) applies.
	IsTrashed *bool
	OrderBy   string
	Page      int
	Limit     int
	Reverse   bool
}

// GetStoredItems retrieves a single page of stored items using the generated client
func (c *Client) GetStoredItems(accessToken string, params GetStoredItemsParams) (*StoredItemsPage, error) {
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
	apiParams := &api.GetStoredItemsParams{
		DatasetID:         params.DatasetID,
		DataSourceModelID: params.DataSourceModelID,
		SearchQuery:       params.SearchQuery,
		IsTrashed:         params.IsTrashed,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.StoredItemOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetStoredItemsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetAllStoredItems fetches items page by page, every request with one page size,
// and returns them with the server's total_rows, or nil when it sent none. It
// stops at maxResults items (0 = no cap), at an empty page, once it holds
// total_rows items, or, when the server sent no total, at a page with no next.
// A failed request after the first returns the items so far with the error.
func (c *Client) GetAllStoredItems(accessToken string, params GetStoredItemsParams, maxResults int) ([]StoredItemTiny, *int, error) {
	chunkSize := params.Limit
	if chunkSize <= 0 {
		chunkSize = 100
	}
	if maxResults > 0 && maxResults < chunkSize {
		chunkSize = maxResults
	}
	params.Limit = chunkSize

	// Default to stable sort
	if params.OrderBy == "" {
		params.OrderBy = "id"
	}

	var allResults []StoredItemTiny
	var total *int
	for page := 1; ; page++ {
		params.Page = page
		resp, err := c.GetStoredItems(accessToken, params)
		if err != nil {
			// If we have partial results, return them with the error
			if len(allResults) > 0 {
				return allResults, total, fmt.Errorf("partial results (got %d): %w", len(allResults), err)
			}
			return nil, nil, err
		}
		if resp.TotalRows != nil {
			total = resp.TotalRows
		}
		allResults = append(allResults, resp.Results...)

		switch {
		case maxResults > 0 && len(allResults) >= maxResults:
			return allResults[:maxResults], total, nil
		case len(resp.Results) == 0:
			return allResults, total, nil
		case total != nil:
			if len(allResults) >= *total {
				return allResults, total, nil
			}
		case resp.Next == nil:
			return allResults, total, nil
		}
	}
}

// GetStoredItem retrieves a single stored item by ID using the generated client
func (c *Client) GetStoredItem(accessToken string, storedItemID int) (*StoredItemFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetOneStoredItemWithResponse(context.Background(), c.organizationID, storedItemID, &api.GetOneStoredItemParams{})
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

// GetThirdPartyStoredItem retrieves one file of a table shared into the
// organization, including the ID of the table it belongs to.
func (c *Client) GetThirdPartyStoredItem(accessToken string, storedItemID int) (*ThirdPartyStoredItem, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	resp, err := apiClient.API.GetOneThirdPartyStoredItemWithResponse(context.Background(), c.organizationID, storedItemID, &api.GetOneThirdPartyStoredItemParams{})
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

// CreateStoredItemForUpload creates a stored item and returns a pre-signed upload URL using the generated client
func (c *Client) CreateStoredItemForUpload(accessToken string, req StoredItemPutRequest) (*StoredItemURLResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.CreateAStoredItemForUploadingTheFileWithResponse(
		context.Background(),
		c.organizationID,
		&api.CreateAStoredItemForUploadingTheFileParams{},
		req,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusCreated {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// PublishStoredItemForIngestion publishes a stored item for ingestion and
// returns the ID of the ingestion job the response names, or nil when the
// response names none. A non-nil replacesStoredItemID is sent as
// replaces_stored_item_id.
func (c *Client) PublishStoredItemForIngestion(accessToken string, storedItemID int, replacesStoredItemID *int) (*int, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.PublishStoredItemForIngestionWithResponse(
		context.Background(),
		c.organizationID,
		storedItemID,
		&api.PublishStoredItemForIngestionParams{ReplacesStoredItemID: replacesStoredItemID},
	)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusCreated {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, nil
	}
	return resp.JSON200.IngestionJobID, nil
}

// DeleteZombieStoredItem deletes a zombie stored item (cleanup after failed upload) using the generated client
func (c *Client) DeleteZombieStoredItem(accessToken string, storedItemID int) error {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.DeleteZombieStoredItemObjectWithResponse(
		context.Background(),
		c.organizationID,
		storedItemID,
		&api.DeleteZombieStoredItemObjectParams{},
	)
	if err != nil {
		return apiClient.HandleError(err, "zombie cleanup failed")
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		if c.verbosity > 0 {
			fmt.Fprintf(os.Stderr, "Warning: zombie cleanup failed: %d\n", resp.StatusCode())
		}
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "zombie cleanup failed")
	}

	return nil
}

// DeleteOrRecoverStoredItem marks a stored item as deleted/ignored or recovers it using the generated client
func (c *Client) DeleteOrRecoverStoredItem(accessToken string, req StoredItemDeleteOrRecoverRequest) error {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.DeleteOrRecoverStoredItemWithResponse(
		context.Background(),
		c.organizationID,
		&api.DeleteOrRecoverStoredItemParams{},
		req,
	)
	if err != nil {
		return apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	return nil
}

// GetDownloadURL retrieves a pre-signed URL for downloading the original file using the generated client
func (c *Client) GetDownloadURL(accessToken string, storedItemID int) (*StoredItemURLResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDownloadLinkForOriginalStoredItemWithResponse(
		context.Background(),
		c.organizationID,
		storedItemID,
		&api.GetDownloadLinkForOriginalStoredItemParams{},
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

// UploadFile uploads a file to the presigned URL.
func (c *Client) UploadFile(urlResp *StoredItemURLResponse, filePath string) error {
	target := fileul.Target{URL: urlResp.URL}
	if urlResp.HTTPMethod != nil {
		target.Method = string(*urlResp.HTTPMethod)
	}
	if urlResp.Fields != nil {
		target.Fields = *urlResp.Fields
	}
	return fileul.Upload(c.httpClient, target, filePath, c.verbosity)
}

// DownloadFile downloads a file from the pre-signed URL.
//
// Note: this downloads from an external URL, not the API, so it does not use
// the generated client. The transfer itself lives in internal/filedl, shared
// with `download data` and with the decrypted-backup path of `download file`.
func (c *Client) DownloadFile(urlResp *StoredItemURLResponse, outputPath string, force bool) error {
	method := ""
	if urlResp.HTTPMethod != nil {
		method = string(*urlResp.HTTPMethod)
	}
	return filedl.ToFile(c.httpClient, method, urlResp.URL, outputPath, force, c.verbosity)
}
