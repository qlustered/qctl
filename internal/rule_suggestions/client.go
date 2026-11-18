package rule_suggestions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/row_filters"
)

// Type aliases to the generated schemas.
type (
	Suggestion     = api.DatasetRuleSuggestionSchema
	SuggestionTiny = api.DatasetRuleSuggestionTinySchema
	SuggestionList = api.DatasetRuleSuggestionListSchema
)

// Client handles HTTP requests for rule suggestion operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new rule suggestions client. The organization UUID is
// validated at construction time (dataset_rules pattern).
func NewClient(baseURL string, organizationID string, verbosity int) (*Client, error) {
	orgUUID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}, nil
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

// wrapAPIError rebuilds a structured *APIError from the transport's captured
// error body when the generated parser rejected it (e.g. the backend's
// object-shaped 422 detail), falling back to generic handling otherwise.
func wrapAPIError(apiClient *client.Client, err error, contextMsg string) error {
	if code, body, ok := apiClient.LastCapturedError(); ok {
		return handleError(code, body, contextMsg)
	}
	return apiClient.HandleError(err, contextMsg)
}

func parseSuggestionID(id string) (openapi_types.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return openapi_types.UUID{}, qerrors.NewBadUsagef("invalid suggestion id %q: expected a full UUID", id)
	}
	return u, nil
}

// ListOptions filters the suggestion list.
type ListOptions struct {
	// States is any of pending|applied|dismissed|failed; empty = no state
	// filter. `failed` is reachable only by a revalidation candidate the
	// backend resolved as failed (see rule_suggestions.PhaseFailed for the
	// four routes) — an ordinary staged edit never reaches it.
	States []string
	Source string // periodic|profiling|user; "" = all
	Page   int
	Limit  int
}

// List fetches a table's rule suggestions.
func (c *Client) List(accessToken string, datasetID int, opts ListOptions) (*SuggestionList, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListSuggestionsParams{}
	if len(opts.States) > 0 {
		states := make([]api.SuggestionState, len(opts.States))
		for i, s := range opts.States {
			states[i] = api.SuggestionState(s)
		}
		params.State = &states
	}
	if opts.Source != "" {
		src := api.SuggestionSource(opts.Source)
		params.Source = &src
	}
	if opts.Page > 0 {
		params.Page = &opts.Page
	}
	if opts.Limit > 0 {
		params.Limit = &opts.Limit
	}
	resp, err := apiClient.API.ListSuggestionsWithResponse(context.Background(), c.organizationID, datasetID, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list rule suggestions failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list rule suggestions failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// Accept applies the staged change to the table rule.
func (c *Client) Accept(accessToken string, datasetID int, suggestionID string) (*Suggestion, error) {
	sugUUID, err := parseSuggestionID(suggestionID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.AcceptSuggestion(
		context.Background(), c.organizationID, datasetID, sugUUID, &api.AcceptSuggestionParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "accept suggestion failed")
	}
	return suggestionResponse(resp, "accept suggestion failed")
}

// Dismiss discards the staged change. dismissFamily maps to the backend's
// dismiss_family: never re-suggest any revision of this rule family for
// this table.
func (c *Client) Dismiss(accessToken string, datasetID int, suggestionID string, dismissFamily bool) (*Suggestion, error) {
	sugUUID, err := parseSuggestionID(suggestionID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.DismissSuggestionJSONRequestBody{}
	if dismissFamily {
		body.DismissFamily = &dismissFamily
	}
	resp, err := apiClient.API.DismissSuggestion(
		context.Background(), c.organizationID, datasetID, sugUUID, &api.DismissSuggestionParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "dismiss suggestion failed")
	}
	return suggestionResponse(resp, "dismiss suggestion failed")
}

// Get fetches one suggestion.
func (c *Client) Get(accessToken string, datasetID int, suggestionID string) (*Suggestion, error) {
	sugUUID, err := parseSuggestionID(suggestionID)
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetSuggestion(
		context.Background(), c.organizationID, datasetID, sugUUID, &api.GetSuggestionParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get rule suggestion failed")
	}
	return suggestionResponse(resp, "get rule suggestion failed")
}

// suggestionResponse reads and closes resp's body. A 200 is decoded with
// decodeSuggestion; any other status goes to handleError with contextMsg.
func suggestionResponse(resp *http.Response, contextMsg string) (*Suggestion, error) {
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read rule suggestion response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, handleError(resp.StatusCode, body, contextMsg)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, fmt.Errorf("unexpected empty response")
	}
	suggestion, err := decodeSuggestion(body)
	if err != nil {
		return nil, fmt.Errorf("decode rule suggestion: %w", err)
	}
	return suggestion, nil
}

// decodeSuggestion mirrors the generated response decode with UseNumber.
// The generated schema remains authoritative for every typed field, while
// numbers nested under the interface{} params field avoid a float64 round trip.
// Decoding the raw response directly matters: repairing params after the
// generated decoder runs is too late for an integer outside float64's range,
// because that first decode rejects the otherwise-valid JSON response.
func decodeSuggestion(body []byte) (*Suggestion, error) {
	var suggestion Suggestion
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&suggestion); err != nil {
		return nil, err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, err
	}
	return &suggestion, nil
}

// BulkResult is the shape both bulk routes answer with. It arrives with HTTP
// 200 even when every item failed, so callers must inspect Errors rather than
// trusting the status code.
type BulkResult = api.BulkSuggestionResultSchema

// BulkSelector names the suggestions a bulk call acts on. IDs and the two
// filters are ANDed by the backend (_resolve_pending_suggestions), so a caller
// sets either IDs or filters, never both.
type BulkSelector struct {
	IDs    []string
	Source *api.SuggestionSource
	Action *api.SuggestionAction
}

// idsForRequest builds the `suggestion_ids` field. A nil is what the backend
// reads as "no id filter" (`if request.suggestion_ids is not None`), so an
// empty IDs slice must stay nil rather than becoming [] — which would compile
// to `IN ()` and match nothing.
func (s BulkSelector) idsForRequest() (*[]openapi_types.UUID, error) {
	if len(s.IDs) == 0 {
		return nil, nil
	}
	ids := make([]openapi_types.UUID, 0, len(s.IDs))
	for _, raw := range s.IDs {
		u, err := parseSuggestionID(raw)
		if err != nil {
			return nil, err
		}
		ids = append(ids, u)
	}
	return &ids, nil
}

// BulkAccept accepts every pending suggestion the selector matches.
//
// Note the asymmetry with Accept: an id that does not exist, or that is not
// pending, is filtered out by the backend's own query and appears in NEITHER
// Skipped nor Errors. Callers that took ids from a user must reconcile the
// result against what they sent; see internal/commands/suggestion/preflight.go.
func (c *Client) BulkAccept(accessToken string, datasetID int, sel BulkSelector) (*BulkResult, error) {
	ids, err := sel.idsForRequest()
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.BulkAcceptSuggestionsJSONRequestBody{
		SuggestionIds: ids,
		Source:        sel.Source,
		Action:        sel.Action,
	}
	resp, err := apiClient.API.BulkAcceptSuggestionsWithResponse(
		context.Background(), c.organizationID, datasetID, &api.BulkAcceptSuggestionsParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "bulk accept suggestions failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "bulk accept suggestions failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// BulkDismiss dismisses every pending suggestion the selector matches.
// dismissFamily maps to the backend's dismiss_family and applies to every
// suggestion in the batch.
func (c *Client) BulkDismiss(accessToken string, datasetID int, sel BulkSelector, dismissFamily bool) (*BulkResult, error) {
	ids, err := sel.idsForRequest()
	if err != nil {
		return nil, err
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.BulkDismissSuggestionsJSONRequestBody{
		SuggestionIds: ids,
		Source:        sel.Source,
		Action:        sel.Action,
	}
	if dismissFamily {
		body.DismissFamily = &dismissFamily
	}
	resp, err := apiClient.API.BulkDismissSuggestionsWithResponse(
		context.Background(), c.organizationID, datasetID, &api.BulkDismissSuggestionsParams{}, body)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "bulk dismiss suggestions failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "bulk dismiss suggestions failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// PatchFields are the editable fields of a pending suggestion. A nil pointer
// leaves its field alone. RowFilter's zero value leaves the row filter alone,
// row_filters.Remove() deletes it, and row_filters.Replace(f) stores f; the
// backend validates a replacement against the table's schema, answering 422
// for a filter that does not compile or a predicate on a custom rule, and 409
// when the suggestion has no rule release.
type PatchFields struct {
	Params            *map[string]interface{}
	Severity          *api.RuleSeverity
	RuleColumnMapping *map[string]string
	RowFilter         row_filters.Edit
}

// patchBody encodes f as a PatchSuggestionRequest. row_filter is in the body
// only when f.RowFilter is Named, and is null for a Remove.
func patchBody(f PatchFields) ([]byte, error) {
	req := api.PatchSuggestionRequest{
		Params:            f.Params,
		Severity:          f.Severity,
		RuleColumnMapping: f.RuleColumnMapping,
	}
	if !f.RowFilter.Named() {
		return json.Marshal(req)
	}
	// The outer row_filter hides the embedded one, whose omitempty drops nil.
	return json.Marshal(struct {
		api.PatchSuggestionRequest
		RowFilter *api.DescribedRowFilter `json:"row_filter"`
	}{req, f.RowFilter.Saved()})
}

// Patch updates a pending suggestion in place. The backend answers 409 for a
// revalidation candidate or a suggestion that is no longer pending.
//
// Params and RuleColumnMapping replace their stored values.
func (c *Client) Patch(accessToken string, datasetID int, suggestionID string, f PatchFields) (*Suggestion, error) {
	sugUUID, err := parseSuggestionID(suggestionID)
	if err != nil {
		return nil, err
	}
	body, err := patchBody(f)
	if err != nil {
		return nil, fmt.Errorf("edit suggestion failed: failed to encode request: %w", err)
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.PatchSuggestionWithBody(
		context.Background(), c.organizationID, datasetID, sugUUID, &api.PatchSuggestionParams{},
		"application/json", bytes.NewReader(body))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "edit suggestion failed")
	}
	return suggestionResponse(resp, "edit suggestion failed")
}
