package review_policies

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

// Client handles HTTP requests for review policy operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new review policies client (standard pattern).
func NewClient(baseURL string, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
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

// GetOrgRulePolicy returns the effective org-level rule review policy.
// Returns (nil, nil) on 404 — no policy exists at any level.
func (c *Client) GetOrgRulePolicy(accessToken string) (*EffectiveRulePolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetOrgRuleReviewPolicyWithResponse(
		context.Background(), c.organizationID, &api.GetOrgRuleReviewPolicyParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// PutOrgRulePolicy creates or replaces the org-level rule review policy.
func (c *Client) PutOrgRulePolicy(accessToken string, body api.ReviewPolicyRequestSchema) (*RulePolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CreateOrUpdateOrgRuleReviewPolicyWithResponse(
		context.Background(), c.organizationID, &api.CreateOrUpdateOrgRuleReviewPolicyParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "apply rule review policy failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "apply rule review policy failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetDatasetRulePolicy returns the effective rule review policy for a dataset.
// Returns (nil, nil) on 404.
func (c *Client) GetDatasetRulePolicy(accessToken string, datasetID int) (*EffectiveRulePolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetDatasetRuleReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.GetDatasetRuleReviewPolicyParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// PutDatasetRulePolicy creates or replaces a dataset-level rule review policy override.
func (c *Client) PutDatasetRulePolicy(accessToken string, datasetID int, body api.ReviewPolicyRequestSchema) (*RulePolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CreateOrUpdateDatasetRuleReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.CreateOrUpdateDatasetRuleReviewPolicyParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "apply rule review policy failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "apply rule review policy failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetOrgDataPolicy returns the org-level data review policy.
// Returns (nil, nil) on 404.
func (c *Client) GetOrgDataPolicy(accessToken string) (*DataPolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetOrgDataReviewPolicyWithResponse(
		context.Background(), c.organizationID, &api.GetOrgDataReviewPolicyParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// PutOrgDataPolicy creates or replaces the org-level data review policy.
func (c *Client) PutOrgDataPolicy(accessToken string, body api.DataReviewPolicySchema) (*DataPolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PutOrgDataReviewPolicyWithResponse(
		context.Background(), c.organizationID, &api.PutOrgDataReviewPolicyParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "apply data review policy failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "apply data review policy failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetDatasetDataPolicy returns the data review policy for a dataset.
// Returns (nil, nil) on 404.
func (c *Client) GetDatasetDataPolicy(accessToken string, datasetID int) (*DataPolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetDatasetDataReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.GetDatasetDataReviewPolicyParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// PutDatasetDataPolicy creates or replaces a dataset-level data review policy override.
func (c *Client) PutDatasetDataPolicy(accessToken string, datasetID int, body api.DataReviewPolicySchema) (*DataPolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PutDatasetDataReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.PutDatasetDataReviewPolicyParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "apply data review policy failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "apply data review policy failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// DeleteDatasetRulePolicy removes a dataset-level rule review policy override.
// The table reverts to the org policy / system default.
func (c *Client) DeleteDatasetRulePolicy(accessToken string, datasetID int) error {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteDatasetRuleReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.DeleteDatasetRuleReviewPolicyParams{})
	if err != nil {
		return apiClient.HandleError(err, "delete rule review policy failed")
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "delete rule review policy failed")
	}
	return nil
}

// DeleteDatasetDataPolicy removes a dataset-level data review policy override.
func (c *Client) DeleteDatasetDataPolicy(accessToken string, datasetID int) error {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteDatasetDataReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.DeleteDatasetDataReviewPolicyParams{})
	if err != nil {
		return apiClient.HandleError(err, "delete data review policy failed")
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "delete data review policy failed")
	}
	return nil
}

// GetOrgChangePolicy returns the organization's change review policy.
// Returns (nil, nil) on 404 — the organization has none.
func (c *Client) GetOrgChangePolicy(accessToken string) (*ChangePolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetOrgChangeReviewPolicyWithResponse(
		context.Background(), c.organizationID, &api.GetOrgChangeReviewPolicyParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// PutOrgChangePolicy creates or replaces the organization's change review policy.
func (c *Client) PutOrgChangePolicy(accessToken string, body api.ChangeReviewPolicyPutSchema) (*ChangePolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PutOrgChangeReviewPolicyWithResponse(
		context.Background(), c.organizationID, &api.PutOrgChangeReviewPolicyParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "apply change review policy failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "apply change review policy failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetChangePolicyTemplate returns the server's change review policy template:
// require_distinct_approver and operations.
func (c *Client) GetChangePolicyTemplate(accessToken string) (map[string]any, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetChangeReviewPolicyTemplateWithResponse(
		context.Background(), c.organizationID, &api.GetChangeReviewPolicyTemplateParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return *resp.JSON200, nil
}

// GetDatasetChangePolicy returns a table's change review override.
// Returns (nil, nil) on 404 — the table has none.
func (c *Client) GetDatasetChangePolicy(accessToken string, datasetID int) (*ChangePolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetDatasetChangeReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.GetDatasetChangeReviewPolicyParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// PutDatasetChangePolicy creates or replaces a table's change review override.
func (c *Client) PutDatasetChangePolicy(accessToken string, datasetID int, body api.ChangeReviewPolicyPutSchema) (*ChangePolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PutDatasetChangeReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.PutDatasetChangeReviewPolicyParams{}, body)
	if err != nil {
		return nil, apiClient.HandleError(err, "apply change review policy failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "apply change review policy failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetEffectiveChangePolicy returns the change review policy in force for a table.
func (c *Client) GetEffectiveChangePolicy(accessToken string, datasetID int) (*EffectiveChangePolicy, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetEffectiveChangeReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.GetEffectiveChangeReviewPolicyParams{})
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

// DeleteDatasetChangePolicy removes a table's change review override and
// reports whether there was one.
func (c *Client) DeleteDatasetChangePolicy(accessToken string, datasetID int) (bool, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return false, err
	}
	resp, err := apiClient.API.DeleteDatasetChangeReviewPolicyWithResponse(
		context.Background(), c.organizationID, datasetID, &api.DeleteDatasetChangeReviewPolicyParams{})
	if err != nil {
		return false, apiClient.HandleError(err, "delete change review policy failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return false, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "delete change review policy failed")
	}
	if resp.JSON200 == nil {
		return false, fmt.Errorf("unexpected empty response")
	}
	return (*resp.JSON200)["deleted"], nil
}
