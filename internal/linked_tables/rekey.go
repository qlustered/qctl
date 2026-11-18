package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// RekeyRequest builds the re-key body from a linked table manifest and source ids.
func (m *LinkedTableManifest) RekeyRequest(sourceIDs map[string]int) (api.CompositeSpecGroupRekeyRequest, error) {
	var request api.CompositeSpecGroupRekeyRequest
	if m.Spec.Release == nil || *m.Spec.Release == "" {
		return request, qerrors.NewBadUsage("spec.release is required for re-keying")
	}
	if m.Spec.DryRun != nil {
		return request, qerrors.NewBadUsage("spec.dry_run is not used by re-key; launch a dry run after re-keying")
	}
	apply, err := m.ToAPIRequest()
	if err != nil {
		return request, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to build re-key request")
	}
	request.NaturalKeyColumns = apply.Spec.Spine.NaturalKeyColumns
	request.NaturalKeyNormalization = apply.Spec.Spine.NaturalKeyNormalization
	request.Revision = api.CompositeSpecRevisionSpec{
		Release: apply.Spec.Release, Description: apply.Spec.Description,
		Output: apply.Spec.Output, Sources: make(map[string]api.CompositeSourceSpec, len(apply.Spec.Sources)),
	}
	for alias, source := range apply.Spec.Sources {
		id, ok := sourceIDs[source.Dataset]
		if !ok {
			return request, qerrors.NewBadUsagef("spec.sources.%s.dataset %q was not resolved", alias, source.Dataset)
		}
		// The apply and re-key source schemas differ only in dataset address:
		// apply takes a name, while re-key takes an id. JSON carries the same
		// join union in both generated types.
		encoded, err := json.Marshal(source)
		if err != nil {
			return request, fmt.Errorf("failed to encode source %q: %w", alias, err)
		}
		var converted api.CompositeSourceSpec
		if err := json.Unmarshal(encoded, &converted); err != nil {
			return request, fmt.Errorf("failed to convert source %q: %w", alias, err)
		}
		converted.DatasetID = id
		request.Revision.Sources[alias] = converted
	}
	return request, nil
}

// Rekey replaces a linked table's spec group and returns the draft revision.
func (c *Client) Rekey(accessToken string, groupID uuid.UUID, request api.CompositeSpecGroupRekeyRequest) (*api.CompositeSpecGroupRekeyResponse, []byte, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}
	contextMsg := fmt.Sprintf("failed to re-key composite spec group %s", groupID)
	resp, err := apiClient.API.RekeyCompositeSpecGroupViewWithResponse(
		context.Background(), c.organizationID, groupID, &api.RekeyCompositeSpecGroupViewParams{}, request)
	if err != nil {
		return nil, nil, apiClient.HandleError(err, contextMsg)
	}
	if resp.StatusCode() != http.StatusOK {
		httpErr := apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, contextMsg)
		return nil, nil, withIssues(resp.StatusCode(), resp.Body, httpErr)
	}
	if resp.JSON200 == nil {
		return nil, nil, qerrors.NewAPIIncompatible("re-key answer has no response body")
	}
	return resp.JSON200, resp.Body, nil
}
