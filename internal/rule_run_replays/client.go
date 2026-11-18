// Package rule_run_replays is the domain client for rule-run replays: one
// launch route and one read route.
//
// A replay re-executes a historical rule run's manifest over the rows that run
// left evidence for, so the verdicts it produces can be compared with the ones
// it recorded. The work rides an ordinary dry-run job as its carrier, which is
// why a launch answers with a dry_run_job_id as well as a replay_id, and why
// the replay's own state is read off that carrier
// (get_rule_run_replay_for_organization, atlas
// capi/capi/controllers/rule_run_replay_controller.py).
package rule_run_replays //nolint:revive // Package name matches the resource domain, as every other domain client does.

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

// Client handles HTTP requests for rule-run replay operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new rule-run replays client.
func NewClient(baseURL, organizationID string, verbosity int) (*Client, error) {
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

// Type aliases for the generated schemas.
type (
	// Replay is one replay attempt: the launch's bookkeeping, the carrier's
	// state, and the report once the replay finished.
	Replay = api.RuleRunReplaySchema
	// LaunchResponse is what a launch answers with, before the replay has run.
	LaunchResponse = api.RuleRunReplayLaunchResponseSchema
)

func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	return client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
}

// LaunchReplay starts a replay of one historical rule run, pinning at most
// maxRows of the rows that run left evidence for.
//
// maxRows is always sent. The backend's own default is 1000 (atlas
// capi/capi/schemas/rule_run_schema.py RuleRunReplayLaunchSchema, `max_rows:
// int = Field(default=1000, gt=0)`), and qctl mirrors it rather than omitting
// the field, because the cap is load-bearing for reproducibility: the run's
// first that many rows in row-id order are pinned, so the same cap on the same
// run pins the same rows. A launch that named no cap would be reproducible only
// for as long as the server's default did not move.
//
// The bound is enforced by the caller, not here: oapi-codegen dropped the
// `gt=0` and generated MaxRows as a plain *int, so a non-positive value would
// otherwise travel to the backend and come back as a pydantic 422.
func (c *Client) LaunchReplay(accessToken string, ruleRunID, maxRows int) (*LaunchResponse, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.LaunchRuleRunReplayJobWithResponse(
		context.Background(),
		c.organizationID,
		ruleRunID,
		&api.LaunchRuleRunReplayJobParams{},
		api.RuleRunReplayLaunchSchema{MaxRows: &maxRows},
	)
	if err != nil {
		// The generated parser returns (nil, err) whenever a typed error
		// branch fails to unmarshal, which is what a pydantic 422 does here:
		// the spec declares HTTPValidationError{Detail *[]ValidationError} and
		// the backend sends detail:{msg,...}. HandleError replays the body the
		// transport captured, headers included, so nothing is lost.
		return nil, apiClient.HandleError(err, "failed to launch rule-run replay")
	}

	if resp.StatusCode() != http.StatusOK {
		// Forward the response headers rather than calling
		// HandleHTTPErrorFromBytes, which passes nil: this route's 429 carries
		// its backoff in Retry-After (atlas capi/capi/views/rule_run_view.py:
		// 190-205), and without the header *apierror.ThrottleError.RetryAfter
		// silently reads 0. Mirrors dry_runs.LaunchDryRunJob.
		var header http.Header
		if resp.HTTPResponse != nil {
			header = resp.HTTPResponse.Header
		}
		return nil, apierror.HandleHTTPErrorWithHeader(
			resp.StatusCode(), resp.Body, header, "failed to launch rule-run replay")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetReplay reads one replay by id.
func (c *Client) GetReplay(accessToken string, replayID int) (*Replay, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetRuleRunReplayWithResponse(
		context.Background(),
		c.organizationID,
		replayID,
		&api.GetRuleRunReplayParams{},
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to get rule-run replay")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(
			resp.StatusCode(), resp.Body, "failed to get rule-run replay")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
