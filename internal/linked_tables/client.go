package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Client handles HTTP requests for linked table (composite dataset) operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new linked tables client.
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

// ApplyResponse mirrors atlas CompositeDatasetApplyResponse, the apply
// route's 200 answer when validate_only is false.
type ApplyResponse struct {
	Result                  string    `json:"result"` // created, revision_added or unchanged
	DatasetID               int       `json:"dataset_id"`
	SettingsModelID         int       `json:"settings_model_id"`
	DataSourceModelID       int       `json:"data_source_model_id"`
	CompositeSpecGroupID    uuid.UUID `json:"composite_spec_group_id"`
	CompositeSpecRevisionID uuid.UUID `json:"composite_spec_revision_id"`
	RevisionState           string    `json:"revision_state"`
	Release                 string    `json:"release"`
	SpecSHA256              string    `json:"spec_sha256"`
	DesiredState            string    `json:"desired_state"` // enabled or draft
	Converged               bool      `json:"converged"`
	CompositeDryRunJobID    *int      `json:"composite_dry_run_job_id"`
	IngestionJobID          *int      `json:"ingestion_job_id"`
	Warnings                []Issue   `json:"warnings"`
}

// Apply sends the manifest to the idempotent backend apply endpoint. It
// returns the decoded answer, nil when the body does not decode or carries no
// result, and the raw response body.
func (c *Client) Apply(accessToken string, m *LinkedTableManifest) (*ApplyResponse, []byte, error) {
	body, err := c.post(accessToken, m, false, "apply linked table failed")
	if err != nil {
		return nil, nil, err
	}
	var resp ApplyResponse
	if json.Unmarshal(body, &resp) != nil || resp.Result == "" {
		return nil, body, nil
	}
	return &resp, body, nil
}

// ValidateResult is the apply route's answer to validate_only=true.
type ValidateResult struct {
	Valid    bool
	Errors   []Issue
	Warnings []Issue
}

// Validate sends the manifest with validate_only=true, which persists nothing.
// It returns the decoded verdict and the raw response body.
func (c *Client) Validate(accessToken string, m *LinkedTableManifest) (*ValidateResult, []byte, error) {
	body, err := c.post(accessToken, m, true, "validate linked table failed")
	if err != nil {
		return nil, nil, err
	}
	var wire struct {
		Valid    *bool   `json:"valid"`
		Errors   []Issue `json:"errors"`
		Warnings []Issue `json:"warnings"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || wire.Valid == nil {
		return nil, body, qerrors.NewAPIIncompatible(
			"the validate_only answer has no 'valid' field, so qctl cannot tell whether the manifest passed")
	}
	return &ValidateResult{Valid: *wire.Valid, Errors: wire.Errors, Warnings: wire.Warnings}, body, nil
}

// Err is nil for a valid manifest and an exit-2 error listing the server's
// errors otherwise.
func (r *ValidateResult) Err(name string) error {
	if r.Valid {
		return nil
	}
	if len(r.Errors) == 0 {
		return qerrors.NewBadUsagef("linked-table/%s failed server validation (the server listed no errors)", name)
	}
	return qerrors.NewBadUsagef("linked-table/%s failed server validation:\n%s", name, formatIssueList(r.Errors))
}

// post sends the manifest to POST /composite-datasets/apply and returns the
// 2xx response body.
func (c *Client) post(accessToken string, m *LinkedTableManifest, validateOnly bool, contextMsg string) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("manifest is required")
	}

	body, err := m.ToAPIRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	resp, err := apiClient.API.ApplyCompositeDatasetViewWithResponse(
		context.Background(),
		c.organizationID,
		&api.ApplyCompositeDatasetViewParams{ValidateOnly: &validateOnly},
		body,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, contextMsg)
	}

	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		// Forward the response headers. Converging a manifest usually launches
		// a composite dry run (_converge, atlas
		// capi/capi/controllers/composite_dataset_apply_controller.py; a
		// revision that is already enabled, or already has a current
		// successful dry run, converges without one), so this route answers
		// 429 when the exploratory cap is full, and
		// HandleHTTPErrorFromBytes would pass nil -- discarding the server's
		// Retry-After and leaving *apierror.ThrottleError to fall back to the
		// body's detail.context.retry_after_seconds, or to 0 when the body
		// carries none. Note which shape hits which path: the object-shaped
		// detail atlas sends parses fine and lands HERE, while a
		// non-conforming body fails in the generated parser and reaches the
		// caller through HandleError above. Both have to keep the header.
		var header http.Header
		if resp.HTTPResponse != nil {
			header = resp.HTTPResponse.Header
		}
		httpErr := apierror.HandleHTTPErrorWithHeader(resp.StatusCode(), resp.Body, header, contextMsg)
		return nil, withIssues(resp.StatusCode(), resp.Body, httpErr)
	}
	return resp.Body, nil
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
