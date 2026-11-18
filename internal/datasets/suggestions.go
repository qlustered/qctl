package datasets

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// ConceptSuggestions returns suggestions for unassigned columns of a table.
func (c *Client) ConceptSuggestions(token string, id int) (*api.DatasetColumnConceptSuggestionsSchema, error) {
	// Model startup can outlast the default request deadline. The global
	// --request-timeout still takes precedence over this per-request value.
	apiClient, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: client.ResolveTimeout(90 * time.Second), Verbosity: c.verbosity})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	resp, err := apiClient.API.GetDatasetColumnConceptSuggestionsWithResponse(context.Background(), c.organizationID, id, &api.GetDatasetColumnConceptSuggestionsParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "get standard field suggestions")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get standard field suggestions")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty standard field suggestions response")
	}
	return resp.JSON200, nil
}

// TableKindSuggestions returns table kinds suggested by assigned concepts.
func (c *Client) TableKindSuggestions(token string, id int) (*api.DatasetProblemDomainSuggestionsSchema, error) {
	apiClient, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: client.ResolveTimeout(c.timeout), Verbosity: c.verbosity})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	resp, err := apiClient.API.GetDatasetProblemDomainSuggestionsWithResponse(context.Background(), c.organizationID, id, &api.GetDatasetProblemDomainSuggestionsParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "get table kind suggestions")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get table kind suggestions")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty table kind suggestions response")
	}
	return resp.JSON200, nil
}
