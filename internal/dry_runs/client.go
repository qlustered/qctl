package dry_runs //nolint:revive // Public package name matches the established resource domain.

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
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
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
		organizationID: orgUUID,
		verbosity:      verbosity,
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
		req,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to launch dry-run job")
	}

	if resp.StatusCode() != http.StatusOK {
		// Forward the response headers: HandleHTTPErrorFromBytes passes nil, so a
		// 429's Retry-After would silently read 0 in *apierror.ThrottleError and
		// every retry would fall through to the client's computed backoff. Mirrors
		// dataset_rules.InstantiateRule and linked_tables.Apply. This endpoint
		// enforces the exploratory dry-run cap -- 3 runs EXECUTING per user per
		// organization, queued work free (check_dry_run_rate_limit, atlas
		// cettings/cettings/controllers/dry_run_helpers.py, checked in
		// create_and_run_dry_run, capi/capi/controllers/dry_run_controller.py).
		var header http.Header
		if resp.HTTPResponse != nil {
			header = resp.HTTPResponse.Header
		}
		return nil, apierror.HandleHTTPErrorWithHeader(resp.StatusCode(), resp.Body, header, "failed to launch dry-run job")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetDryRunJob retrieves a single dry-run job by ID
func (c *Client) GetDryRunJob(accessToken string, jobID int) (*DryRunJobFull, error) {
	return c.GetDryRunJobContext(context.Background(), accessToken, jobID)
}

// GetDryRunJobContext is GetDryRunJob with ctx controlling the request.
func (c *Client) GetDryRunJobContext(ctx context.Context, accessToken string, jobID int) (*DryRunJobFull, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetDryRunJobWithResponse(
		ctx,
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

	return decodeBody[DryRunJobFull](resp.Body, "failed to decode dry-run job")
}

// ListDryRunJobs retrieves dry-run jobs for a dataset
func (c *Client) ListDryRunJobs(accessToken string, datasetID int, params *api.GetDryRunJobsParams) (*DryRunJobsList, error) {
	return c.ListDryRunJobsContext(context.Background(), accessToken, datasetID, params)
}

// ListDryRunJobsContext is ListDryRunJobs with ctx controlling the request.
func (c *Client) ListDryRunJobsContext(ctx context.Context, accessToken string, datasetID int, params *api.GetDryRunJobsParams) (*DryRunJobsList, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	if params == nil {
		params = &api.GetDryRunJobsParams{}
	}

	resp, err := apiClient.API.GetDryRunJobsWithResponse(
		ctx,
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

	return decodeBody[DryRunJobPreview](resp.Body, "failed to decode dry-run job preview")
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

	return decodeBody[CompactPreview](resp.Body, "failed to decode compact preview")
}

// decodeBody decodes body into a new T with jsonnum.Unmarshal, wrapping a
// failure in msg.
func decodeBody[T any](body []byte, msg string) (*T, error) {
	var v T
	if err := jsonnum.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", msg, err)
	}
	return &v, nil
}
