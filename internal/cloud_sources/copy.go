package cloud_sources

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// CopyTemplate retrieves the template for copying sourceID into targetDatasetID.
func (c *Client) CopyTemplate(accessToken string, targetDatasetID, sourceID int) (*api.DataSourceModelCopyTemplateSchema, []byte, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetADataSourceCopyForDatasetWithResponse(
		context.Background(), c.organizationID, targetDatasetID, sourceID, &api.GetADataSourceCopyForDatasetParams{},
	)
	if err != nil {
		return nil, nil, apiClient.HandleError(err, "get cloud source copy template failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "get cloud source copy template failed")
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, resp.Body, nil
}

// Copy creates a source by copying sourceID with body as its request JSON.
func (c *Client) Copy(accessToken string, sourceID int, body []byte) (*api.DataSourceModelCopyResponseSchema, []byte, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.PostCopyDataSourceModelWithBodyWithResponse(
		context.Background(), c.organizationID, sourceID, &api.PostCopyDataSourceModelParams{},
		"application/json", bytes.NewReader(body),
	)
	if err != nil {
		return nil, nil, apiClient.HandleError(err, "copy cloud source failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "copy cloud source failed")
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, resp.Body, nil
}
