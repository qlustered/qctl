package dry_runs

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

// Client handles HTTP requests for dry-run job operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new dry-runs client
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: openapi_types.UUID(orgUUID),
		verbosity:      verbosity,
		timeout:        30 * time.Second,
	}
}

// newAPIClient creates a configured API client
func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	return client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
}

// LaunchDryRunJob creates a new dry-run job
func (c *Client) LaunchDryRunJob(accessToken string, req LaunchRequest) (*LaunchResponse, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.LaunchDryRunJobWithResponse(
		context.Background(),
		c.organizationID,
		&api.LaunchDryRunJobParams{},
		api.LaunchDryRunJobJSONRequestBody(req),
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to launch dry-run job")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to launch dry-run job")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetDryRunJob retrieves a single dry-run job by ID
func (c *Client) GetDryRunJob(accessToken string, jobID int) (*DryRunJobFull, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDryRunJobWithResponse(
		context.Background(),
		c.organizationID,
		jobID,
		&api.GetDryRunJobParams{},
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to get dry-run job")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to get dry-run job")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// ListDryRunJobs retrieves dry-run jobs for a dataset
func (c *Client) ListDryRunJobs(accessToken string, datasetID int, params *api.GetDryRunJobsParams) (*DryRunJobsList, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	if params == nil {
		params = &api.GetDryRunJobsParams{}
	}

	resp, err := apiClient.API.GetDryRunJobsWithResponse(
		context.Background(),
		c.organizationID,
		datasetID,
		params,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to list dry-run jobs")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to list dry-run jobs")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetDryRunJobPreview retrieves the full preview for a dry-run job
func (c *Client) GetDryRunJobPreview(accessToken string, jobID int) (*DryRunJobPreview, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDryRunJobPreviewWithResponse(
		context.Background(),
		c.organizationID,
		jobID,
		&api.GetDryRunJobPreviewParams{},
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to get dry-run job preview")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to get dry-run job preview")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetDryRunJobPreviewCompact retrieves the compact preview for a dry-run job
func (c *Client) GetDryRunJobPreviewCompact(accessToken string, jobID int) (*CompactPreview, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDryRunJobPreviewCompactWithResponse(
		context.Background(),
		c.organizationID,
		jobID,
		&api.GetDryRunJobPreviewCompactParams{},
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to get compact preview")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to get compact preview")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}
