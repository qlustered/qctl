package warnings

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// ResolveResult is the backend's resolve response: a frontend redirect and,
// when a keyword action staged its rule change instead of applying it, the
// staged suggestion's id. It carries no status and no job id.
type ResolveResult = api.WarningResolveResponseSchema

// Resolve resolves a warning with one of the actions it offers.
//
// The body carries the chosen action plus any action-specific fields; v1 of
// `qctl warning resolve` sets Action only, because those are the only actions
// --action can drive. Which fields pair with which action is
// x-warning-action-fields on WarningsResolveSchema in the OpenAPI spec,
// generated into api.WarningActionFields.
func (c *Client) Resolve(accessToken string, warningID int, body api.WarningsResolveSchema) (*ResolveResult, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.ResolveWarningWithResponse(
		context.Background(), c.organizationID, warningID, &api.ResolveWarningParams{}, body)
	if err != nil {
		// Not apiClient.HandleError: an error body the generated parser
		// rejects arrives here with the body only reachable through
		// LastCapturedError(). See internal/warnings/errors.go.
		return nil, wrapAPIError(apiClient, err, "resolve warning failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "resolve warning failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}
