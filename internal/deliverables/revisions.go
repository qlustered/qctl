package deliverables

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

// RevisionDetail is a revision's binding tree and its original response body.
type RevisionDetail struct {
	*api.DeliverableRevisionResponse
	Body json.RawMessage `json:"-"`
}

// GetRevision reads a revision's binding tree.
func (c *Client) GetRevision(token string, id uuid.UUID) (*RevisionDetail, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetDeliverableRevisionWithResponse(context.Background(), c.orgID, id, &api.GetDeliverableRevisionParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get deliverable revision failed", "get_deliverable_revision")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get deliverable revision failed", "get_deliverable_revision")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable revision response")
	}
	tree, err := decodeBinding[api.DeliverableRevisionResponse](resp.Body)
	if err != nil {
		return nil, err
	}
	return &RevisionDetail{DeliverableRevisionResponse: tree, Body: resp.Body}, nil
}

// TemplateRows is a template sheet's rows and its original response body.
type TemplateRows struct {
	*api.SkeletonSheetRowsResponse
	Body json.RawMessage `json:"-"`
}

// SheetRows reads the first limit rows of a revision's template sheet.
func (c *Client) SheetRows(token string, id uuid.UUID, sheet string, limit int) (*TemplateRows, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetSkeletonSheetRowsWithResponse(context.Background(), c.orgID, id, sheet, &api.GetSkeletonSheetRowsParams{Limit: &limit})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "read template rows failed", "get_skeleton_sheet_rows")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "read template rows failed", "get_skeleton_sheet_rows")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty template rows response")
	}
	return &TemplateRows{SkeletonSheetRowsResponse: resp.JSON200, Body: resp.Body}, nil
}

// ValidateRevisionSelector rejects conflicting selectors and malformed revision UUIDs.
func ValidateRevisionSelector(release, revision string) error {
	if release != "" && revision != "" {
		return qerrors.NewBadUsage("--release and --revision are mutually exclusive")
	}
	if revision != "" {
		if _, ok := revisionUUID(revision); !ok {
			return qerrors.NewBadUsage("--revision must be a full UUID")
		}
	}
	return nil
}

func revisionUUID(ref string) (uuid.UUID, bool) {
	if len(ref) != 36 {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(ref)
	return id, err == nil
}

// ResolveRevision selects a revision belonging to deliverableID.
func (c *Client) ResolveRevision(token string, deliverableID uuid.UUID, release, revision string) (uuid.UUID, error) {
	if err := ValidateRevisionSelector(release, revision); err != nil {
		return uuid.Nil, err
	}
	items, err := c.ListRevisions(token, deliverableID)
	if err != nil {
		return uuid.Nil, err
	}
	if release != "" || revision != "" {
		item, err := matchRevision(items, deliverableID, release, revision)
		return item.ID, err
	}
	for _, item := range items {
		if item.State == api.DeliverableRevisionStateEnabled {
			return item.ID, nil
		}
	}
	if len(items) == 1 {
		return items[0].ID, nil
	}
	if len(items) == 0 {
		return uuid.Nil, noRevisions(deliverableID)
	}
	return uuid.Nil, qerrors.NewBadUsage("no revision is enabled; choose --release or --revision from 'qctl get deliverable-revisions'")
}

// matchRevision selects the item whose release is release, else whose ID is
// the full UUID in release or revision.
func matchRevision(items []Revision, deliverableID uuid.UUID, release, revision string) (Revision, error) {
	if release != "" {
		for _, item := range items {
			if item.Release == release {
				return item, nil
			}
		}
		revision = release
	}
	id, valid := revisionUUID(revision)
	for _, item := range items {
		if valid && item.ID == id {
			return item, nil
		}
	}
	return Revision{}, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf(
		"no revision matches %q for deliverable %s; use 'qctl get deliverable-revisions %s'", revision, deliverableID, deliverableID)))
}

func noRevisions(deliverableID uuid.UUID) error {
	return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("deliverable %s has no revisions", deliverableID)))
}
