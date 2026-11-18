// Package rule_row_ignores is the domain client for a table rule's row-level
// ignores: the record that stops ONE table rule flagging ONE quarantined row of
// ONE cloud source.
//
// Every route here is scoped by data_source_model_id and there is no
// table-wide form of any of them. An ignore is keyed on the triple (cloud
// source, row, rule), and atlas's partial unique index allows one ACTIVE ignore
// per triple (dataset_rule_row_ignore_model.py).
//
// The one route this package deliberately does NOT expose on its own is a
// delete by bare ignore id. atlas's un_ignore_by_id looks the record up by id
// alone, with no dataset, organization or data-source clause (qctl-tas), so an
// id that came from anywhere but a scoped listing can discard some other
// table's ignore. UnIgnore therefore takes the record a List call returned,
// never an id.
package rule_row_ignores //nolint:revive // Matches the backend resource name.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// Ignore is one active row-level ignore, aliased because the generated schema
// is already the qctl-side shape.
type Ignore = api.RuleRowIgnoreResponseSchema

// APIError is re-exported so command files never import internal/comments
// directly; see internal/datasets/errors.go for the same three lines.
type APIError = comments.APIError

// Client handles HTTP requests for rule-row-ignore operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new rule-row-ignores client (standard pattern).
func NewClient(baseURL string, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
		// Both writes run reprocess_bad_rows_for_dataset_rule inside the
		// request, which selects and flags every bad row carrying the rule
		// across the whole table before it answers.
		timeout: 60 * time.Second,
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

// ListFilter names what a List call asks for. The route answers 400 with
// neither RuleID nor RowID, so at least one is required.
//
// Both together are honoured HERE, not by the server: the view checks row_id
// first and never reads dataset_rule_id when row_id is present
// (rule_row_ignore_view.py list_rule_row_ignores), so sending both would
// silently return that row's ignores for EVERY rule. List sends row_id alone
// and narrows to the rule itself.
type ListFilter struct {
	CloudSourceID int
	RuleID        *openapi_types.UUID
	RowID         *int
}

// List returns the ACTIVE ignores matching the filter, in the server's order,
// which is unspecified: the controller's select carries no ORDER BY. The route
// is not paginated and answers a bare array, so this is one request whatever
// the size.
func (c *Client) List(accessToken string, datasetID int, f ListFilter) ([]Ignore, error) {
	if f.RuleID == nil && f.RowID == nil {
		return nil, fmt.Errorf("internal error: rule-row-ignore list needs a rule or a row id")
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListRuleRowIgnoresParams{DataSourceModelID: f.CloudSourceID}
	if f.RowID != nil {
		params.RowID = f.RowID
	} else {
		params.DatasetRuleID = f.RuleID
	}
	resp, err := apiClient.API.ListRuleRowIgnoresWithResponse(
		context.Background(), c.organizationID, datasetID, params)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, "list rule ignores failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, "list rule ignores failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	out := *resp.JSON200
	if f.RowID != nil && f.RuleID != nil {
		narrowed := out[:0:0]
		for _, ig := range out {
			if ig.DatasetRuleID == *f.RuleID {
				narrowed = append(narrowed, ig)
			}
		}
		out = narrowed
	}
	return out, nil
}

// CreateRequest is one ignore-rule call: one rule, one cloud source, a list of
// quarantined row ids.
type CreateRequest struct {
	CloudSourceID int
	RuleID        openapi_types.UUID
	RowIDs        []int
	// Reason nil sends JSON null, which is the schema's default.
	Reason *string
}

// Create ignores the rule on every named row and answers one record per row id
// in the body, in body order.
//
// It is idempotent per (row, rule): a pair that is already ignored comes back
// as the EXISTING record, carrying its original reason and decided_by_id, and
// the Reason sent here is dropped for that row (create_ignore's early return).
// The route validates the row ids first — every one must be a live bad row of
// this cloud source, else a 404 naming the unknown ones — and only then the
// rule, so a cloud source the table does not have surfaces as the row-ids 404.
func (c *Client) Create(accessToken string, datasetID int, req CreateRequest) ([]Ignore, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	// A nil slice marshals to "row_ids":null, a pydantic type error, where an
	// empty list earns the min_length message. Same guard as SetCell's ids.
	rowIDs := req.RowIDs
	if rowIDs == nil {
		rowIDs = []int{}
	}
	body := api.RuleRowIgnoreCreateSchema{
		DataSourceModelID: req.CloudSourceID,
		DatasetRuleID:     req.RuleID,
		RowIds:            rowIDs,
		Reason:            req.Reason,
	}
	resp, err := apiClient.API.CreateRuleRowIgnoresWithResponse(
		context.Background(), c.organizationID, datasetID, &api.CreateRuleRowIgnoresParams{}, body)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, "ignore rule failed")
	}
	if resp.StatusCode() != http.StatusCreated {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, "ignore rule failed")
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return *resp.JSON201, nil
}

// UnIgnore discards one active ignore. It takes the RECORD a List call
// returned rather than an id, so the only ids that reach the unscoped DELETE
// are ones a scoped listing produced (see the package doc and qctl-tas); the
// data source sent is the record's own.
//
// The route answers 404 when the ignore is no longer active, so the HTTP
// surface is not idempotent even though the controller is. The caller decides
// what a raced 404 means.
func (c *Client) UnIgnore(accessToken string, datasetID int, ig Ignore) error {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.UnIgnoreRuleRowWithResponse(
		context.Background(), c.organizationID, datasetID, ig.ID,
		&api.UnIgnoreRuleRowParams{DataSourceModelID: ig.DataSourceModelID})
	if err != nil {
		return comments.WrapAPIError(apiClient, err, "unignore rule failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return comments.HandleError(resp.StatusCode(), resp.Body, "unignore rule failed")
	}
	return nil
}
