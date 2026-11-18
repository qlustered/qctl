// Package value_mapping is the domain client for the value-mapping
// recommender — the backend service that proposes replacement values for a
// value in a field.
//
// It is its own package rather than a method on internal/datasets because the
// route is ORG-scoped, not dataset-scoped: POST
// /orgs/{org}/value-mapping/recommendation takes the table as a body field, so
// it does not belong on a client whose every other method carries a dataset id
// in the path.
package value_mapping //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, rule_upgrades).

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// Client handles HTTP requests for value-mapping operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new value mapping client
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
}

// Recommendation is one proposed replacement value.
//
// Strong is a pointer in the wire schema but every constructor in the
// recommender sets it (atlas capi/capi/controllers/value_mapping_recommender.py),
// so a nil in practice means the field was absent, not "unknown strength".
type Recommendation = api.MaskableValueRecommendationSchema

// RecommendRequest is one recommendation lookup.
//
// There is no settings-model field, deliberately. The route resolves the
// settings model from dataset_id server-side when settings_model_id is absent
// (get_value_mapping_recommendations, atlas
// capi/capi/views/value_mapping_view.py), and a table is the noun qctl commands
// take; a settings-model id is an internal handle no qctl surface exposes.
//
// The three optional ids are pointers because nil is the backend default: the
// pydantic fields all default to None and the view branches on `is not None`,
// so a nil reaching the wire as JSON null is indistinguishable from an omitted
// key (ValueRecommendationRequestSchema,
// capi/capi/schemas/value_mapping_schema.py).
type RecommendRequest struct {
	DataSourceModelID *int
	AlertItemID       *int
	// WarningItemID is `warning_item_id_` on the wire, trailing underscore and
	// all. If both issue ids are sent the WARNING wins in the recommender.
	WarningItemID *int
	FieldName     string
	Value         string
	DatasetID     int
}

// Recommend returns the replacement values the recommender proposes for a
// value in a field, best first.
//
// An empty list is a normal answer, not a failure: the recommender returns []
// for array and JSON fields and whenever nothing scores. Its own docstring
// calls the ranking brute force and warns that the queried value itself barely
// ranks, so treat the list as suggestions, never as a resolution.
//
// Results are served from a 30-second TTL cache keyed on
// (settings model, source, field, value, issue), so two calls a second apart
// can return the same list after a mapping change.
func (c *Client) Recommend(accessToken string, req RecommendRequest) ([]Recommendation, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	datasetID := req.DatasetID
	body := api.ValueRecommendationRequestSchema{
		DatasetID:         &datasetID,
		FieldName:         req.FieldName,
		Value:             req.Value,
		DataSourceModelID: req.DataSourceModelID,
		AlertItemID:       req.AlertItemID,
		WarningItemID:     req.WarningItemID,
	}
	resp, err := apiClient.API.GetValueMappingRecommendationsWithResponse(
		context.Background(), c.organizationID,
		&api.GetValueMappingRecommendationsParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get value recommendations failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get value recommendations failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("get value recommendations failed: unexpected empty response")
	}
	return *resp.JSON200, nil
}
