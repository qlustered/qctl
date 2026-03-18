package ingestion

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

// Client handles HTTP requests for ingestion job operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new ingestion jobs client
func NewClient(baseURL string, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgID,
		verbosity:      verbosity,
		timeout:        30 * time.Second,
	}, nil
}

// Type aliases for generated types - use these directly
type (
	IngestionJobTiny  = api.IngestionJobTinySchema
	IngestionJobFull  = api.IngestionJobSchemaFull
	IngestionJobsPage = api.IngestionJobsListSchema
	IngestionJobState = api.IngestionJobState
	IngestionJobRun   = api.IngestionJobRunSchema
	IngestionJobKill  = api.IngestionJobKillSchema
)

// IngestionJobsListResponse is an alias for IngestionJobsPage (kept for backward compatibility)
type IngestionJobsListResponse = IngestionJobsPage

// GetIngestionJobsParams holds parameters for listing ingestion jobs
type GetIngestionJobsParams struct {
	States       []string // slice header: 24 bytes
	DatasetID    *int
	DataSourceID *int
	StoredItemID *int
	IsDryRun     *bool
	CreatedBy    *string
	OrderBy      string
	Page         int
	Limit        int // Chunk size per request
	Reverse      bool
}

// RunIngestionJobRequest is the request body for running ingestion jobs
type RunIngestionJobRequest struct {
	IngestionJobIDs []int `json:"ingestion_job_ids"`
}

// RunIngestionJobResponse is the response from run operations
type RunIngestionJobResponse struct {
	Msg string `json:"msg"`
}

// KillIngestionJobRequest is the request body for killing ingestion jobs
type KillIngestionJobRequest struct {
	IngestionJobIDs []int `json:"ingestion_job_ids"`
}

// KillIngestionJobResponse is the response from kill operations
type KillIngestionJobResponse struct {
	Msg string `json:"msg"`
}

// GetIngestionJobs retrieves the list of ingestion jobs using the generated client
func (c *Client) GetIngestionJobs(accessToken string, params GetIngestionJobsParams) (*IngestionJobsPage, error) {
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
	// Note: StoredItemID and CreatedBy are not supported in generated client
	apiParams := &api.GetIngestionJobsParams{
		DatasetID:         params.DatasetID,
		DataSourceModelID: params.DataSourceID,
		IsDryRun:          params.IsDryRun,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.IngestionJobOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	// Convert states to generated type
	if len(params.States) > 0 {
		states := make([]api.IngestionJobState, len(params.States))
		for i, s := range params.States {
			states[i] = api.IngestionJobState(s)
		}
		apiParams.State = &states
	}

	resp, err := apiClient.API.GetIngestionJobsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetIngestionJob retrieves a single ingestion job by ID using the generated client
func (c *Client) GetIngestionJob(accessToken string, jobID int) (*IngestionJobFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetOneIngestionJobWithResponse(context.Background(), c.organizationID, jobID, &api.GetOneIngestionJobParams{})
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

// RunIngestionJob runs a single ingestion job by ID using the generated client
func (c *Client) RunIngestionJob(accessToken string, jobID int) (*RunIngestionJobResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.RunIngestionJobNowWithResponse(context.Background(), c.organizationID, jobID, &api.RunIngestionJobNowParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return &RunIngestionJobResponse{
		Msg: resp.JSON200.Result,
	}, nil
}

// RunMultipleIngestionJobs runs multiple ingestion jobs by IDs using the generated client
func (c *Client) RunMultipleIngestionJobs(accessToken string, jobIDs []int) (*RunIngestionJobResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	reqBody := api.IngestionJobsRunRequestSchema{
		IngestionJobIds: jobIDs,
	}

	resp, err := apiClient.API.RunMultipleIngestionJobsWithResponse(
		context.Background(),
		c.organizationID,
		&api.RunMultipleIngestionJobsParams{},
		reqBody,
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

	return &RunIngestionJobResponse{
		Msg: resp.JSON200.Result,
	}, nil
}

// KillIngestionJob kills a single ingestion job by ID using the generated client
func (c *Client) KillIngestionJob(accessToken string, jobID int) (*KillIngestionJobResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.KillIngestionJobWithResponse(context.Background(), c.organizationID, jobID, &api.KillIngestionJobParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return &KillIngestionJobResponse{
		Msg: resp.JSON200.Result,
	}, nil
}

// KillMultipleIngestionJobs kills multiple ingestion jobs by IDs using the generated client
func (c *Client) KillMultipleIngestionJobs(accessToken string, jobIDs []int) (*KillIngestionJobResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	reqBody := api.IngestionJobsKillRequestSchema{
		IngestionJobIds: jobIDs,
	}

	resp, err := apiClient.API.KillMultipleIngestionJobsWithResponse(
		context.Background(),
		c.organizationID,
		&api.KillMultipleIngestionJobsParams{},
		reqBody,
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

	return &KillIngestionJobResponse{
		Msg: resp.JSON200.Result,
	}, nil
}
