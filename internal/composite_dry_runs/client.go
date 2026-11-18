// Package composite_dry_runs is the domain client for composite dry runs:
// GET /orgs/{org}/composite-dry-runs, its detail route, its per-row examples,
// and the launch route PUT /orgs/{org}/composite-spec-revisions/{rev}/dry-runs.
package composite_dry_runs //nolint:revive // Public package name matches the established resource domain.

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

// Client handles HTTP requests for composite dry-run operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new composite dry runs client.
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

// Type aliases for generated types.
type (
	JobTiny      = api.CompositeDryRunJobTinySchema    //nolint:revive // Public alias for the generated list row.
	JobsPage     = api.CompositeDryRunJobsListSchema   //nolint:revive // Public alias for the generated list page.
	JobDetail    = api.CompositeDryRunJobDetail        //nolint:revive // Public alias for the generated detail.
	ExamplesPage = api.CompositeDryRunExamplesResponse //nolint:revive // Public alias for the generated examples page.
	OrderBy      = api.CompositeDryRunJobOrderBy       //nolint:revive // Public alias for the generated sort enum.
	LaunchResult = api.LaunchCompositeDryRunResult     //nolint:revive // Public alias for the generated launch answer.
)

// ListParams holds parameters for listing composite dry runs.
//
// States is repeatable server-side (`state=running&state=error`).
type ListParams struct {
	States              []string
	DownstreamDatasetID *int
	GroupID             *openapi_types.UUID
	RevisionID          *openapi_types.UUID
	OrderBy             string
	Page                int
	Limit               int
	Reverse             bool
}

// List retrieves a page of composite dry runs.
func (c *Client) List(accessToken string, params ListParams) (*JobsPage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	apiParams := &api.ListCompositeDryRunsParams{
		DownstreamDatasetID: params.DownstreamDatasetID,
		GroupID:             params.GroupID,
		RevisionID:          params.RevisionID,
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
		orderBy := api.CompositeDryRunJobOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		// The backend defaults this endpoint to reverse=true (descending):
		// list_composite_dry_runs declares `reverse: bool = Query(True)`. So
		// --reverse means "reverse the DEFAULT order" and maps to sending
		// reverse=false, the idiom documented in internal/rule_versions/client.go.
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.ListCompositeDryRunsWithResponse(context.Background(), c.organizationID, apiParams)
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

// Detail is a composite dry run with its response body and precision-preserving fields.
type Detail struct {
	Job *JobDetail

	// StructuredSummary is the wire object, nil when the server sent null.
	StructuredSummary interface{}
	ErrorContext      map[string]interface{}

	// Body is the response body as received.
	Body []byte
}

// detailOverlay is what Get decodes from the body beside the generated type.
type detailOverlay struct {
	StructuredSummary interface{}            `json:"structured_summary"`
	ErrorContext      map[string]interface{} `json:"error_context"`
}

// Get retrieves a single composite dry run by ID.
//
// The route answers 404 for an id that does not exist and for one that
// belongs to another organization, in the same words
// (get_composite_dry_run, capi/capi/views/composite_dry_run_view.py).
func (c *Client) Get(accessToken string, jobID int) (*Detail, error) {
	return c.GetContext(context.Background(), accessToken, jobID)
}

// GetContext is Get with ctx controlling the request.
func (c *Client) GetContext(ctx context.Context, accessToken string, jobID int) (*Detail, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	resp, err := apiClient.API.GetCompositeDryRunWithResponse(
		ctx, c.organizationID, jobID, &api.GetCompositeDryRunParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	var overlay detailOverlay
	if err := jsonnum.Unmarshal(resp.Body, &overlay); err != nil {
		return nil, fmt.Errorf("failed to decode composite dry run response: %w", err)
	}
	return &Detail{
		Job:               resp.JSON200,
		StructuredSummary: overlay.StructuredSummary,
		ErrorContext:      overlay.ErrorContext,
		Body:              resp.Body,
	}, nil
}

// ExamplesParams holds parameters for one page of a dry run's examples.
type ExamplesParams struct {
	Bucket      string
	SourceAlias string
	Page        int
	Limit       int
}

// Examples retrieves one page of a dry run's per-spine-row evidence. It
// returns the generated decoding and the response body as received.
func (c *Client) Examples(accessToken string, jobID int, params ExamplesParams) (*ExamplesPage, []byte, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}

	apiParams := &api.GetCompositeDryRunExamplesParams{}
	if params.Bucket != "" {
		apiParams.Bucket = &params.Bucket
	}
	if params.SourceAlias != "" {
		apiParams.SourceAlias = &params.SourceAlias
	}
	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}

	resp, err := apiClient.API.GetCompositeDryRunExamplesWithResponse(
		context.Background(), c.organizationID, jobID, apiParams)
	if err != nil {
		return nil, nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, resp.Body, nil
}

// LaunchParams holds the body of one composite dry-run launch. A zero
// MaxSpineRows and a nil Seed are left for the server to default.
type LaunchParams struct {
	RevisionID        openapi_types.UUID
	MaxSpineRows      int
	Seed              *int
	PinnedSpineRowIDs []int
}

// Launch starts a composite dry run of one spec revision. It returns the
// generated decoding and the response body as received. A 429 is an
// *apierror.ThrottleError carrying the response's Retry-After.
func (c *Client) Launch(accessToken string, params LaunchParams) (*LaunchResult, []byte, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}

	body := api.LaunchCompositeDryRunRequest{
		CompositeSpecRevisionID: params.RevisionID,
		Seed:                    params.Seed,
	}
	if params.MaxSpineRows > 0 {
		body.MaxSpineRows = &params.MaxSpineRows
	}
	if len(params.PinnedSpineRowIDs) > 0 {
		body.PinnedSpineRowIds = &params.PinnedSpineRowIDs
	}

	const contextMsg = "failed to launch composite dry run"
	resp, err := apiClient.API.LaunchCompositeDryRunWithResponse(
		context.Background(), c.organizationID, params.RevisionID, &api.LaunchCompositeDryRunParams{}, body)
	if err != nil {
		return nil, nil, apiClient.HandleError(err, contextMsg)
	}
	if resp.StatusCode() != http.StatusOK {
		var header http.Header
		if resp.HTTPResponse != nil {
			header = resp.HTTPResponse.Header
		}
		return nil, nil, apierror.HandleHTTPErrorWithHeader(resp.StatusCode(), resp.Body, header, contextMsg)
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, resp.Body, nil
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
