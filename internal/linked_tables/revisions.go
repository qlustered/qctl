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

// Revision is one composite spec revision of a linked table.
type Revision struct {
	ID      uuid.UUID
	GroupID uuid.UUID
	Release string
	State   string
}

// Name is the revision's release, or its UUID when it has none.
func (r *Revision) Name() string {
	if r.Release != "" {
		return r.Release
	}
	return r.ID.String()
}

// LatestRevision returns the newest spec revision of the linked table
// datasetID, whatever its state.
func (c *Client) LatestRevision(accessToken string, datasetID int) (*Revision, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	contextMsg := fmt.Sprintf("failed to find a spec revision for table %d (only a linked table has one)", datasetID)
	resp, err := apiClient.API.GetLatestCompositeSpecRevisionWithResponse(
		context.Background(), c.organizationID, datasetID, &api.GetLatestCompositeSpecRevisionParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, contextMsg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, contextMsg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	latest := resp.JSON200
	return &Revision{
		ID:      latest.CompositeSpecRevisionID,
		GroupID: latest.CompositeSpecGroupID,
		Release: latest.Release,
		State:   latest.State,
	}, nil
}

// GetRevision returns the spec revision revisionID.
func (c *Client) GetRevision(accessToken string, revisionID uuid.UUID) (*Revision, error) {
	resp, err := c.readRevision(accessToken, revisionID)
	if err != nil {
		return nil, err
	}
	detail := resp.JSON200
	rev := &Revision{
		ID:      detail.CompositeSpecRevisionID,
		GroupID: detail.CompositeSpecGroupID,
		State:   detail.State,
	}
	if detail.Revision.Release != nil {
		rev.Release = *detail.Revision.Release
	}
	return rev, nil
}

// RevisionSpec is what a composite spec revision holds besides its identity
// and state: its release, its description and the sources and output of its
// spec.
type RevisionSpec struct {
	Release     *string                   `json:"release"`
	Description *string                   `json:"description"`
	Sources     map[string]RevisionSource `json:"sources"`
	Output      map[string]OutputMapping  `json:"output"`
}

// RevisionGroup is the composite spec group a revision belongs to.
type RevisionGroup struct {
	Name                    string            `json:"name"`
	Slug                    string            `json:"slug"`
	SpineDatasetID          int               `json:"spine_dataset_id"`
	NaturalKeyColumns       []string          `json:"natural_key_columns"`
	NaturalKeyNormalization map[string]string `json:"natural_key_normalization"`
	IdentityHashVersion     int               `json:"identity_hash_version"`
}

// RevisionDetail is a composite spec revision with its spec and its group.
type RevisionDetail struct {
	ID       uuid.UUID      `json:"composite_spec_revision_id"`
	GroupID  uuid.UUID      `json:"composite_spec_group_id"`
	State    string         `json:"state"`
	Revision *RevisionSpec  `json:"revision"`
	Group    *RevisionGroup `json:"group"`
}

// RevisionSource is one source of a RevisionSpec. Each JoinFields value is a
// shorthand source-field name (a JSON string) or an expanded clause (an
// object), as the server sent it.
type RevisionSource struct {
	DatasetID  int                        `json:"dataset_id"`
	Arity      *string                    `json:"arity"`
	JoinFields map[string]json.RawMessage `json:"join_fields"`
}

// GetRevisionSpec returns the spec of the revision revisionID.
func (c *Client) GetRevisionSpec(accessToken string, revisionID uuid.UUID) (*RevisionSpec, error) {
	resp, err := c.readRevision(accessToken, revisionID)
	if err != nil {
		return nil, err
	}
	var body struct {
		Revision *RevisionSpec `json:"revision"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil || body.Revision == nil {
		return nil, qerrors.NewAPIIncompatible(fmt.Sprintf("spec revision %s came back without a readable 'revision' block", revisionID))
	}
	return body.Revision, nil
}

// GetRevisionDetail returns the revision revisionID with its spec and group.
func (c *Client) GetRevisionDetail(accessToken string, revisionID uuid.UUID) (*RevisionDetail, error) {
	resp, err := c.readRevision(accessToken, revisionID)
	if err != nil {
		return nil, err
	}
	var detail RevisionDetail
	if err := json.Unmarshal(resp.Body, &detail); err != nil || detail.Revision == nil || detail.Group == nil {
		return nil, qerrors.NewAPIIncompatible(fmt.Sprintf("spec revision %s came back without readable 'revision' and 'group' blocks", revisionID))
	}
	return &detail, nil
}

// readRevision sends GET /composite-spec-revisions/{revisionID} and returns
// its 200 response.
func (c *Client) readRevision(accessToken string, revisionID uuid.UUID) (*api.ReadCompositeSpecRevisionDetailResponse, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}

	contextMsg := fmt.Sprintf("failed to read spec revision %s", revisionID)
	resp, err := apiClient.API.ReadCompositeSpecRevisionDetailWithResponse(
		context.Background(), c.organizationID, revisionID, &api.ReadCompositeSpecRevisionDetailParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, contextMsg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, contextMsg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp, nil
}

// ResolveRevision returns the spec revision of the linked table datasetID
// that a command acts on: the latest one when revisionID is nil, otherwise
// revisionID. A revisionID that belongs to another linked table is bad usage.
func (c *Client) ResolveRevision(accessToken string, datasetID int, revisionID *uuid.UUID) (*Revision, error) {
	latest, err := c.LatestRevision(accessToken, datasetID)
	if err != nil {
		return nil, err
	}
	if revisionID == nil || *revisionID == latest.ID {
		return latest, nil
	}
	rev, err := c.GetRevision(accessToken, *revisionID)
	if err != nil {
		return nil, err
	}
	if rev.GroupID != latest.GroupID {
		return nil, qerrors.NewBadUsagef(
			"revision %s is not a revision of table %d: it belongs to composite spec group %s, the table to %s",
			rev.ID, datasetID, rev.GroupID, latest.GroupID)
	}
	return rev, nil
}

// EnableRevision enables the spec revision revisionID. It returns the
// generated decoding and the response body as received.
func (c *Client) EnableRevision(accessToken string, revisionID uuid.UUID) (*api.CompositeSpecRevisionEnableResponse, []byte, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}

	contextMsg := fmt.Sprintf("failed to enable spec revision %s", revisionID)
	resp, err := apiClient.API.EnableCompositeSpecRevisionWithResponse(
		context.Background(), c.organizationID, revisionID, &api.EnableCompositeSpecRevisionParams{})
	if err != nil {
		return nil, nil, apiClient.HandleError(err, contextMsg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, contextMsg)
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, resp.Body, nil
}

// RetryBuild sends POST /orgs/{org}/composite-spec-groups/{groupID}/retry-build.
// It returns the generated decoding and the response body as received.
func (c *Client) RetryBuild(accessToken string, groupID uuid.UUID) (*api.CompositeRetryBuildResponse, []byte, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, nil, err
	}

	contextMsg := fmt.Sprintf("failed to queue a rebuild of composite spec group %s", groupID)
	resp, err := apiClient.API.RetryCompositeBuildWithResponse(
		context.Background(), c.organizationID, groupID, &api.RetryCompositeBuildParams{})
	if err != nil {
		return nil, nil, apiClient.HandleError(err, contextMsg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, contextMsg)
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, resp.Body, nil
}
