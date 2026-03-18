package stored_items

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// Client handles HTTP requests for stored items (files) operations
type Client struct {
	httpClient     *http.Client
	baseURL        string
	organizationID openapi_types.UUID
	timeout        time.Duration
	verbosity      int
}

// NewClient creates a new stored items client
func NewClient(baseURL string, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgID,
		httpClient: &http.Client{
			Timeout: 300 * time.Second, // Longer timeout for file uploads/downloads
		},
		verbosity: verbosity,
		timeout:   300 * time.Second,
	}, nil
}

// Type aliases for generated types - use these directly
type (
	StoredItemTiny                   = api.StoredItemTinySchema
	StoredItemFull                   = api.StoredItemFullSchema
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
	OrderBy           string
	Page              int
	Limit             int
	Reverse           bool
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

// GetAllStoredItems retrieves all stored items with auto-pagination
func (c *Client) GetAllStoredItems(accessToken string, params GetStoredItemsParams, maxResults int) ([]StoredItemTiny, error) {
	var allResults []StoredItemTiny
	page := 1
	chunkSize := params.Limit
	if chunkSize == 0 {
		chunkSize = 100
	}

	// Default to stable sort by id
	if params.OrderBy == "" {
		params.OrderBy = "id"
	}

	for {
		remainingSlots := maxResults - len(allResults)
		if maxResults > 0 && remainingSlots < chunkSize {
			chunkSize = remainingSlots
			if chunkSize <= 0 {
				break
			}
		}

		params.Page = page
		params.Limit = chunkSize

		resp, err := c.GetStoredItems(accessToken, params)
		if err != nil {
			if len(allResults) > 0 {
				return allResults, fmt.Errorf("partial results (got %d): %w", len(allResults), err)
			}
			return nil, err
		}

		allResults = append(allResults, resp.Results...)

		if resp.Next == nil || len(resp.Results) == 0 {
			break
		}

		if maxResults > 0 && len(allResults) >= maxResults {
			break
		}

		page++
	}

	return allResults, nil
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

// PublishStoredItemForIngestion publishes a stored item for ingestion using the generated client
func (c *Client) PublishStoredItemForIngestion(accessToken string, storedItemID int) error {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.PublishStoredItemForIngestionWithResponse(
		context.Background(),
		c.organizationID,
		storedItemID,
		&api.PublishStoredItemForIngestionParams{},
	)
	if err != nil {
		return apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusCreated {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	return nil
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

// UploadFile uploads a file to the pre-signed URL
// Note: This method uploads to an external URL, not the API, so it doesn't use the generated client
func (c *Client) UploadFile(urlResp *StoredItemURLResponse, filePath string) error {
	// Determine HTTP method (default to PUT if not specified)
	method := "PUT"
	if urlResp.HTTPMethod != nil && *urlResp.HTTPMethod != "" {
		method = string(*urlResp.HTTPMethod)
	}

	// Get fields map (may be nil)
	var fields map[string]string
	if urlResp.Fields != nil {
		fields = *urlResp.Fields
	}

	// Build the request based on method
	var req *http.Request
	var err error

	if method == "POST" && len(fields) > 0 {
		req, err = c.buildMultipartUploadRequest(urlResp.URL, filePath, fields)
	} else {
		req, err = c.buildPutUploadRequest(method, urlResp.URL, filePath, fields)
	}
	if err != nil {
		return err
	}

	// Execute the upload
	return c.executeUpload(req, method)
}

// buildMultipartUploadRequest creates a POST request with multipart form data (for S3/GCS POST uploads).
func (c *Client) buildMultipartUploadRequest(url, filePath string, fields map[string]string) (*http.Request, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add fields first
	for key, val := range fields {
		if err := writer.WriteField(key, val); err != nil {
			return nil, fmt.Errorf("failed to write field %s: %w", key, err)
		}
	}

	// Add file
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("failed to copy file content: %w", err)
	}

	writer.Close()

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return req, nil
}

// buildPutUploadRequest creates a PUT request with file body (standard S3 PUT upload).
func (c *Client) buildPutUploadRequest(method, url, filePath string, fields map[string]string) (*http.Request, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	req, err := http.NewRequest(method, url, file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Get file stats for content length
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	req.ContentLength = stat.Size()

	// Set content type if available
	if contentType, ok := fields["Content-Type"]; ok {
		req.Header.Set("Content-Type", contentType)
	}

	return req, nil
}

// executeUpload sends the upload request and handles the response.
func (c *Client) executeUpload(req *http.Request, method string) error {
	if c.verbosity > 0 {
		fmt.Fprintf(os.Stderr, "→ %s %s (file upload)\n", method, req.URL)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	if c.verbosity > 0 {
		fmt.Fprintf(os.Stderr, "← %d %s\n", resp.StatusCode, resp.Status)
	}

	// Accept 200, 201, 204 as success for uploads
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("upload failed with status %d (could not read response body)", resp.StatusCode)
		}
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// DownloadFile downloads a file from the pre-signed URL
// Note: This method downloads from an external URL, not the API, so it doesn't use the generated client
func (c *Client) DownloadFile(urlResp *StoredItemURLResponse, outputPath string, force bool) error {
	// Check if file already exists
	if _, err := os.Stat(outputPath); err == nil && !force {
		return fmt.Errorf("file already exists: %s (use --force to overwrite)", outputPath)
	}

	// Determine HTTP method (default to GET)
	method := "GET"
	if urlResp.HTTPMethod != nil && *urlResp.HTTPMethod != "" {
		method = string(*urlResp.HTTPMethod)
	}

	req, err := http.NewRequest(method, urlResp.URL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if c.verbosity > 0 {
		fmt.Fprintf(os.Stderr, "→ %s %s (file download)\n", method, urlResp.URL)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if c.verbosity > 0 {
		fmt.Fprintf(os.Stderr, "← %d %s\n", resp.StatusCode, resp.Status)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("download failed with status %d (could not read response body)", resp.StatusCode)
		}
		return fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Create output file
	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer out.Close()

	// Copy response body to file
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// GenerateStorageKey generates a key for storing the file in backup storage
// This follows the pattern: org_id/dataset_id/data_source_model_id/timestamp_filename
func GenerateStorageKey(orgID string, datasetID, dataSourceModelID int, fileName string) string {
	timestamp := time.Now().UTC().Format("20060102-150405")
	return fmt.Sprintf("%s/%d/%d/%s_%s", orgID, datasetID, dataSourceModelID, timestamp, fileName)
}
