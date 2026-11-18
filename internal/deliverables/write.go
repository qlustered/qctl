package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// DeliverableSlugMax is the length atlas cuts a name's slug to before any -N
// suffix.
const DeliverableSlugMax = 59

const (
	createDeliverableFailed = "create deliverable failed"
	patchDeliverableFailed  = "patch deliverable failed"
	createRevisionFailed    = "create deliverable revision failed"
)

// RevisionCreated is a created revision's tree, its anchor report, and the original body.
type RevisionCreated struct {
	*api.DeliverableRevisionCreateResponse
	Body json.RawMessage `json:"-"`
}

// BySlug returns the deliverable whose slug is exactly slug, in the trash or
// not, or nil when there is none.
func (c *Client) BySlug(token, slug string) (*ListItem, error) {
	params := ListParams{Limit: resolveLimit, OrderBy: "created_at", IncludeArchived: true}
	if utf8.RuneCountInString(slug) >= 3 {
		params.Search = slug
	}
	items, err := c.listAll(token, params)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Slug == slug {
			return &items[i], nil
		}
	}
	return nil, nil
}

// Create POSTs body as raw JSON to /deliverables.
func (c *Client) Create(token string, body map[string]any) (*Deliverable, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode deliverable request: %w", err)
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CreateDeliverableWithBodyWithResponse(context.Background(), c.orgID,
		&api.CreateDeliverableParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, createDeliverableFailed, "create_deliverable")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, createDeliverableFailed, "create_deliverable")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty create deliverable response")
	}
	return resp.JSON200, nil
}

// Patch sends body as raw JSON to PATCH /deliverables/{id}.
func (c *Client) Patch(token string, id uuid.UUID, body map[string]any) (*Deliverable, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode deliverable request: %w", err)
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PatchDeliverableWithBodyWithResponse(context.Background(), c.orgID, id,
		&api.PatchDeliverableParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, patchDeliverableFailed, "patch_deliverable")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, patchDeliverableFailed, "patch_deliverable")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty patch deliverable response")
	}
	return resp.JSON200, nil
}

// CreateRevision POSTs body as raw JSON to /deliverables/{id}/revisions. A 409
// SKELETON_LOSSY is a *TemplateLossyError (templateLossyError).
func (c *Client) CreateRevision(token string, deliverableID uuid.UUID, body map[string]any) (*RevisionCreated, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode deliverable revision request: %w", err)
	}
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.CreateDeliverableRevisionWithBodyWithResponse(context.Background(), c.orgID, deliverableID,
		&api.CreateDeliverableRevisionParams{}, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, templateLossyError(wrapAPIError(apiClient, err, createRevisionFailed, "create_deliverable_revision"))
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, templateLossyError(handleError(resp.StatusCode(), resp.Body, createRevisionFailed, "create_deliverable_revision"))
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty create deliverable revision response")
	}
	tree, err := decodeBinding[api.DeliverableRevisionCreateResponse](resp.Body)
	if err != nil {
		return nil, err
	}
	return &RevisionCreated{DeliverableRevisionCreateResponse: tree, Body: resp.Body}, nil
}
