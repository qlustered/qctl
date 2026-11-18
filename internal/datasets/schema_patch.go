package datasets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// PatchSchemaOverrides writes a schema override set through
// PATCH /orgs/{org}/datasets/{id}/migrations.
//
// body is a hand-built map rather than api.MigrationPatchRequest — see
// SchemaPatchBody's doc comment for the two independent reasons, both of which
// would corrupt a column the caller only meant to retype. Build it with
// SchemaPatchBody; this method only marshals and sends.
//
// Needs MIGRATION_EXECUTE, which only ops, admin and agent hold
// (atlas common/common/auth_types.py ROLE_PERMISSIONS): an editor who can
// change rules and table config cannot change a schema, and gets a 403.
func (c *Client) PatchSchemaOverrides(accessToken string, datasetID int, body map[string]any) (*api.MigrationPatchResponse, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to encode schema override request: %w", err)
	}
	resp, err := apiClient.API.PatchMigrationWithBodyWithResponse(
		context.Background(), c.organizationID, datasetID,
		&api.PatchMigrationParams{}, "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "schema update failed")
	}
	if resp.StatusCode() != http.StatusOK {
		// handleError keeps the *APIError envelope so the command layer can read
		// the error code and not just the status. The code no longer tells a
		// stale version token apart from the route's other conflicts: from atlas
		// #1020 both version-mismatch 409s carry ERR_MIGRATION_CONFLICT too, and
		// only a server older than that answers one with no code at all.
		return nil, handleError(resp.StatusCode(), resp.Body, "schema update failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("schema update failed: unexpected empty response")
	}
	return resp.JSON200, nil
}
