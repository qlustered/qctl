// Package composite_run_jobs is the domain client for the composite recompute
// history: GET /orgs/{org}/composite-run-jobs and its detail route, the only
// two requests it sends.
//
// A composite run job is the control-plane record of ONE composite recompute,
// the job that rebuilds a composite (linked) table.
package composite_run_jobs //nolint:revive // Public package name matches the established resource domain.

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

// Client handles HTTP requests for composite run job operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new composite run jobs client.
func NewClient(baseURL string, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgID,
		verbosity:      verbosity,
	}, nil
}

// Type aliases for generated types - use these directly.
type (
	CompositeRunJobTiny  = api.CompositeRunJobTinySchema  //nolint:revive // Established public compatibility alias.
	CompositeRunJobFull  = api.CompositeRunJobSchemaFull  //nolint:revive // Established public compatibility alias.
	CompositeRunJobsPage = api.CompositeRunJobsListSchema //nolint:revive // Established public compatibility alias.
	OrderBy              = api.CompositeRunJobOrderBy     //nolint:revive // Established public compatibility alias.
	State                = api.IngestionJobState          //nolint:revive // Established public compatibility alias.
)

// GetCompositeRunJobsParams holds parameters for listing composite run jobs.
//
// States is repeatable server-side (`state=running&state=error`); the command
// validates each value before it gets here, so an unknown state never reaches
// the wire.
type GetCompositeRunJobsParams struct {
	States           []string
	ChangedDatasetID *int
	GroupID          *openapi_types.UUID
	Reason           *string
	OrderBy          string
	Page             int
	Limit            int
	Reverse          bool
}

// GetCompositeRunJobs retrieves a page of composite run jobs.
func (c *Client) GetCompositeRunJobs(accessToken string, params GetCompositeRunJobsParams) (*CompositeRunJobsPage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	apiParams := &api.GetCompositeRunJobsParams{
		ChangedDatasetID: params.ChangedDatasetID,
		GroupID:          params.GroupID,
		Reason:           params.Reason,
	}

	if len(params.States) > 0 {
		states := make([]api.IngestionJobState, 0, len(params.States))
		for _, s := range params.States {
			states = append(states, api.IngestionJobState(s))
		}
		apiParams.State = &states
	}
	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.CompositeRunJobOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		// The backend defaults this endpoint to reverse=true (descending):
		// composite_run_job_view.py's get_composite_run_jobs declares `reverse:
		// bool = FastApiQuery(True, ...)`. So --reverse means "reverse the
		// DEFAULT order" and maps to sending reverse=false (ascending), the
		// same idiom documented in internal/rule_versions/client.go.
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetCompositeRunJobsWithResponse(context.Background(), c.organizationID, apiParams)
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

// GetCompositeRunJob retrieves a single composite run job by ID.
//
// The route 404s on an id that does not exist and on one whose anchor resolves
// to another organization (verify_composite_run_job_organization,
// capi/capi/controllers/composite_run_job_controller.py), and 403s unless the
// caller can read every dataset the response names
// (check_composite_run_job_datasets_for_user).
// Both come back through apierror, so the exit codes are 3 and 2 respectively.
func (c *Client) GetCompositeRunJob(accessToken string, jobID int) (*CompositeRunJobFull, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	resp, err := apiClient.API.GetOneCompositeRunJobWithResponse(
		context.Background(), c.organizationID, jobID, &api.GetOneCompositeRunJobParams{})
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

func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	return apiClient, nil
}
