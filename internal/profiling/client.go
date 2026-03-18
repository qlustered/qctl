package profiling

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

// Client handles HTTP requests for profiling job (training job in backend) operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new profiling jobs client
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
	ProfilingJobTiny  = api.TrainingJobTinySchema
	ProfilingJobFull  = api.TrainingJobFullSchema
	ProfilingJobsPage = api.TrainingJobsListSchema
	ProfilingJobState = api.TrainingJobState
	ProfilingJobStep  = api.TrainingJobStep
	ProfilingJobRun   = api.TrainingJobRunSchema
)

// ProfilingJobsListResponse is an alias for ProfilingJobsPage (kept for backward compatibility)
type ProfilingJobsListResponse = ProfilingJobsPage

// GetProfilingJobsParams holds parameters for listing profiling jobs
type GetProfilingJobsParams struct {
	DatasetID   *int
	SearchQuery *string
	OrderBy     string
	Page        int
	Limit       int
	Reverse     bool
}

// RunProfilingJobResponse is the response from run operations
type RunProfilingJobResponse struct {
	Msg string `json:"msg"`
}

// KillProfilingJobResponse is the response from kill operations
type KillProfilingJobResponse struct {
	Msg string `json:"msg"`
}

// GetProfilingJobs retrieves the list of profiling jobs using the generated client
func (c *Client) GetProfilingJobs(accessToken string, params GetProfilingJobsParams) (*ProfilingJobsPage, error) {
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
	apiParams := &api.GetTrainingJobsParams{
		DatasetID:   params.DatasetID,
		SearchQuery: params.SearchQuery,
	}

	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.TrainingJobOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetTrainingJobsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetProfilingJob retrieves a single profiling job by ID using the generated client
func (c *Client) GetProfilingJob(accessToken string, jobID int) (*ProfilingJobFull, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetOneTrainingJobWithResponse(context.Background(), c.organizationID, jobID, &api.GetOneTrainingJobParams{})
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

// RunProfilingJob runs a profiling job for a dataset using the generated client.
// Note: The backend API runs training jobs by dataset ID, not job ID.
func (c *Client) RunProfilingJob(accessToken string, datasetID int) (*RunProfilingJobResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.RunTrainingJobNowWithResponse(context.Background(), c.organizationID, datasetID, &api.RunTrainingJobNowParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return &RunProfilingJobResponse{
		Msg: resp.JSON200.Result,
	}, nil
}

// KillProfilingJob kills a profiling job by ID using the generated client.
// This is implemented by patching the job state to "killed".
func (c *Client) KillProfilingJob(accessToken string, jobID int) (*KillProfilingJobResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	reqBody := api.TrainingJobPatchRequestSchema{
		State: api.TrainingJobStateKilled,
	}

	resp, err := apiClient.API.PatchTrainingJobWithResponse(
		context.Background(),
		c.organizationID,
		jobID,
		&api.PatchTrainingJobParams{},
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

	return &KillProfilingJobResponse{
		Msg: fmt.Sprintf("Job %d state changed to killed", jobID),
	}, nil
}
