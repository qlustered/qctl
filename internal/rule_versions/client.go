package rule_versions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// resolutionListLimit caps the catalog listing used to resolve an identifier.
// An org with more than this many rule revisions can hold a short ID the
// unfiltered fallback will not see; the cache, seeded by every `get rules` and
// `get rule-revisions`, is what covers that case.
const resolutionListLimit = 1000

// minCachedPrefixLen is the shortest input the short-ID cache may answer,
// counted in hex digits with dashes stripped. It is the width of the SHORT-ID
// column (see ShortID), and it is a safety floor rather than a tuning knob: the
// cache stores UUIDs without names, so it cannot tell "2024" the name fragment
// from "2024" the start of some unrelated revision's UUID, and answering from
// it would take precedence over the name match the user meant. A shorter input
// therefore skips the cache and is resolved against the unfiltered catalog,
// where names are available -- and still never reaches the backend as a
// search_query.
const minCachedPrefixLen = 8

// Client handles HTTP requests for rule version operations
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new rule versions client
func NewClient(baseURL string, organizationID string, verbosity int) (*Client, error) {
	orgID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}
	return &Client{
		baseURL:        baseURL,
		organizationID: orgID,
		verbosity:      verbosity,
		timeout:        60 * time.Second,
	}, nil
}

// RuleVersionSubmitRequest represents the request to submit rule version file content
// This is a compatibility wrapper around api.RuleRevisionSubmitRequestSchema
type RuleVersionSubmitRequest struct {
	Force    *bool  `json:"force,omitempty"`
	FileText string `json:"file_text"`
	Verbose  *bool  `json:"verbose,omitempty"` // ask the backend for the full code diff on 422 version conflicts
}

// RuleVersionSubmitResponse represents the response from rule version submission
// This is a compatibility wrapper around api.RuleRevisionSubmitResponseSchema.
// SubmitRuleVersion leaves no list nil.
type RuleVersionSubmitResponse struct {
	Message    string     `json:"message"`     // Success message
	Added      [][]string `json:"added"`       // List of [rule_name, version] tuples
	NotChanged [][]string `json:"not_changed"` // List of [rule_name, version] tuples that were unchanged
}

// VersionConflictError is returned when a rule submission or check conflicts
// with a locked revision. Diff carries detail.diff from the wire:
// the full code diff when the request sent verbose=true, otherwise the
// compact diff (which the display message in Err already embeds).
type VersionConflictError struct {
	Diff string
	Err  error
}

func (e *VersionConflictError) Error() string { return e.Err.Error() }

// Unwrap keeps internal/errors exit-code resolution working.
func (e *VersionConflictError) Unwrap() error { return e.Err }

// handleSubmitError wraps a 422 with a non-empty conflict diff so the submit
// command can render it and the bump-release hint.
func handleSubmitError(statusCode int, body []byte, contextMsg string) error {
	err := apierror.HandleHTTPErrorFromBytes(statusCode, body, contextMsg)
	if statusCode != http.StatusUnprocessableEntity {
		return err
	}
	var envelope struct {
		Detail struct {
			ErrorCode string `json:"error_code"`
			Diff      string `json:"diff"`
		} `json:"detail"`
	}
	if jsonErr := json.Unmarshal(body, &envelope); jsonErr != nil ||
		envelope.Detail.ErrorCode != "ERR_RULE_VERSION_CONFLICT" || envelope.Detail.Diff == "" {
		return err
	}
	return &VersionConflictError{Diff: envelope.Detail.Diff, Err: err}
}

// SubmitRuleVersion submits a rule version file for import using the generated client
func (c *Client) SubmitRuleVersion(accessToken string, req RuleVersionSubmitRequest) (*RuleVersionSubmitResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Convert to generated request type
	apiReq := api.SubmitRuleRevisionJSONRequestBody{
		FileText: req.FileText,
		Force:    req.Force,
		Verbose:  req.Verbose,
	}

	resp, err := apiClient.API.SubmitRuleRevisionWithResponse(context.Background(), c.organizationID, &api.SubmitRuleRevisionParams{}, apiReq)
	if err != nil {
		// The generated HTTPValidationError parser expects `detail` to be a
		// list; ERR_RULE_VERSION_CONFLICT (and other structured errors) send
		// an object, so JSON unmarshal fails before resp is populated and we
		// land here instead of the status-code branch below. Recover the raw
		// body captured by the transport so handleSubmitError still sees it.
		if code, body, ok := apiClient.LastCapturedError(); ok {
			return nil, handleSubmitError(code, body, "rule import failed")
		}
		return nil, apiClient.HandleError(err, "rule import failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, handleSubmitError(resp.StatusCode(), resp.Body, "rule import failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	// Convert response to compatibility type
	result := &RuleVersionSubmitResponse{
		Message:    resp.JSON200.Message,
		Added:      [][]string{},
		NotChanged: [][]string{},
	}

	// Convert Added from [][]interface{} to [][]string
	for _, added := range resp.JSON200.Added {
		var strTuple []string
		for _, item := range added {
			if s, ok := item.(string); ok {
				strTuple = append(strTuple, s)
			} else {
				strTuple = append(strTuple, fmt.Sprintf("%v", item))
			}
		}
		result.Added = append(result.Added, strTuple)
	}

	// Convert NotChanged from [][]interface{} to [][]string
	if resp.JSON200.NotChanged != nil {
		for _, nc := range *resp.JSON200.NotChanged {
			var strTuple []string
			for _, item := range nc {
				if s, ok := item.(string); ok {
					strTuple = append(strTuple, s)
				} else {
					strTuple = append(strTuple, fmt.Sprintf("%v", item))
				}
			}
			result.NotChanged = append(result.NotChanged, strTuple)
		}
	}

	return result, nil
}

// CheckRuleVersion asks the server what submitting req does, without
// submitting it. A 422 comes back as SubmitRuleVersion returns it; a 404 or
// 405 comes back as an exit-5 error.
func (c *Client) CheckRuleVersion(accessToken string, req RuleVersionSubmitRequest) (*api.RuleRevisionCheckResponseSchema, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	apiReq := api.CheckRuleRevisionJSONRequestBody{
		FileText: req.FileText,
		Force:    req.Force,
		Verbose:  req.Verbose,
	}

	resp, err := apiClient.API.CheckRuleRevisionWithResponse(context.Background(), c.organizationID, &api.CheckRuleRevisionParams{}, apiReq)
	if err != nil {
		// Same object-shaped 422 detail as SubmitRuleVersion.
		if code, body, ok := apiClient.LastCapturedError(); ok {
			return nil, handleCheckError(code, body)
		}
		return nil, apiClient.HandleError(err, "rule check failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, handleCheckError(resp.StatusCode(), resp.Body)
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// handleCheckError maps a check-route error response: 404 and 405 to an
// exit-5 error, 422 to the rejection handleSubmitError builds, and anything
// else to a failed check.
func handleCheckError(statusCode int, body []byte) error {
	switch statusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return qerrors.NewAPIIncompatible(fmt.Sprintf(
			"this server does not support 'qctl submit rules --check' "+
				"(POST rule-revisions/check answered %d %s); nothing was submitted",
			statusCode, http.StatusText(statusCode)))
	case http.StatusUnprocessableEntity:
		return handleSubmitError(statusCode, body, "submit would reject this file")
	}
	return handleSubmitError(statusCode, body, "rule check failed")
}

// GetRuleRevisionsParams holds parameters for listing rule revisions
type GetRuleRevisionsParams struct {
	SearchQuery         *string
	ProblemDomainSlug   *[]string
	GeneralOnly         *bool
	OnlyDefault         *bool
	StateFilter         *string
	HasUpgradeAvailable *bool
	OrderBy             string
	Page                int
	Limit               int
	Reverse             bool
}

// GetRuleRevisions retrieves the list of rule revisions
func (c *Client) GetRuleRevisions(accessToken string, params GetRuleRevisionsParams) (*RuleRevisionList, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	apiParams := &api.GetRuleRevisionListParams{
		SearchQuery:       params.SearchQuery,
		OnlyDefault:       params.OnlyDefault,
		ProblemDomainSlug: params.ProblemDomainSlug,
		GeneralOnly:       params.GeneralOnly,
	}

	if params.HasUpgradeAvailable != nil {
		apiParams.HasUpgradeAvailable = params.HasUpgradeAvailable
	}
	if params.StateFilter != nil {
		state := api.RuleState(*params.StateFilter)
		apiParams.StateFilter = &state
	}
	if params.Page > 0 {
		apiParams.Page = &params.Page
	}
	if params.Limit > 0 {
		apiParams.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		orderBy := api.RuleRevisionOrderBy(params.OrderBy)
		apiParams.OrderBy = &orderBy
	}
	if params.Reverse {
		// The backend defaults this endpoint to reverse=true (descending), so
		// Reverse here means "reverse the default order": it maps to sending
		// reverse=false (ascending). Same idiom as the alerts and datasets clients.
		notReversed := false
		apiParams.Reverse = &notReversed
	}

	resp, err := apiClient.API.GetRuleRevisionListWithResponse(context.Background(), c.organizationID, apiParams)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetDatasetRuleRevisionMatchesParams filters revisions matching a table.
type GetDatasetRuleRevisionMatchesParams struct {
	Match             api.RuleMatchBucket
	Page              int
	Limit             int
	SearchQuery       *string
	ProblemDomainSlug *[]string
}

// GetDatasetRuleRevisionMatches lists default revisions matching a table.
func (c *Client) GetDatasetRuleRevisionMatches(accessToken string, datasetID int, query GetDatasetRuleRevisionMatchesParams) (*api.RuleRevisionMatchListSchema, error) {
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: accessToken,
		Timeout: c.timeout, Verbosity: c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	params := &api.GetDatasetRuleRevisionMatchesParams{
		Match: &query.Match, Page: &query.Page, Limit: &query.Limit,
		SearchQuery: query.SearchQuery, ProblemDomainSlug: query.ProblemDomainSlug,
	}
	resp, err := apiClient.API.GetDatasetRuleRevisionMatchesWithResponse(context.Background(), c.organizationID, datasetID, params)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetRuleRevisionDatasetMatches lists tables matching a rule revision.
func (c *Client) GetRuleRevisionDatasetMatches(accessToken, revisionID string, page, limit int) (*api.DatasetMatchListSchema, error) {
	id, err := uuid.Parse(revisionID)
	if err != nil {
		return nil, qerrors.NewBadUsagef("invalid rule revision ID %q", revisionID)
	}
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: accessToken,
		Timeout: c.timeout, Verbosity: c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	params := &api.GetRuleRevisionDatasetMatchesParams{Page: &page, Limit: &limit}
	resp, err := apiClient.API.GetRuleRevisionDatasetMatchesWithResponse(context.Background(), c.organizationID, id, params)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// GetAllRuleRevisions retrieves every page matching params.
func (c *Client) GetAllRuleRevisions(accessToken string, params GetRuleRevisionsParams) ([]RuleRevisionTiny, error) {
	if params.Limit <= 0 {
		params.Limit = resolutionListLimit
	}

	// Atlas reports TotalRows but leaves Next empty for ordered pages. The
	// default relevance order has an ID tiebreaker; before c702b9f1f,
	// plain-column OFFSET orders did not. Newer builds append ID to them too.
	// Leave OrderBy empty for a stable walk across both versions.
	var revisions []RuleRevisionTiny
	for page := 1; ; page++ {
		params.Page = page
		resp, err := c.GetRuleRevisions(accessToken, params)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch rule revisions page %d: %w", page, err)
		}
		revisions = append(revisions, resp.Results...)

		if resp.TotalRows != nil {
			if len(revisions) >= *resp.TotalRows || len(resp.Results) == 0 {
				return revisions, nil
			}
			continue
		}
		if resp.Next == nil || len(resp.Results) == 0 {
			return revisions, nil
		}
	}
}

// GetRuleRevisionAllReleases retrieves all releases for a rule revision family
func (c *Client) GetRuleRevisionAllReleases(accessToken string, ruleRevisionID string) (*RuleRevisionsFamily, error) {
	revisionUUID, err := uuid.Parse(ruleRevisionID)
	if err != nil {
		return nil, fmt.Errorf("invalid rule revision ID: %w", err)
	}

	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetRuleRevisionAllReleasesWithResponse(
		context.Background(), c.organizationID, revisionUUID, &api.GetRuleRevisionAllReleasesParams{},
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// GetRuleRevisionDetails retrieves the full detail (including code) for a rule
// revision. Numbers in its interface{} fields decode as jsonnum.Unmarshal
// decodes them.
func (c *Client) GetRuleRevisionDetails(accessToken string, ruleRevisionID string, datasetID ...int) (*RuleRevisionFull, error) {
	params := &api.GetOneRuleRevisionDetailsParams{}
	if len(datasetID) > 0 {
		params.DatasetID = &datasetID[0]
	}
	return c.getRuleRevisionDetails(accessToken, ruleRevisionID, params)
}

// GetRuleRevisionBindingPreview fetches a rule revision with the server's
// per-field binding preview for one table. upgradedFrom, when non-nil, names
// the table rule whose bindings the preview inherits.
func (c *Client) GetRuleRevisionBindingPreview(accessToken string, ruleRevisionID string, datasetID int, upgradedFrom *uuid.UUID) (*RuleRevisionFull, error) {
	return c.getRuleRevisionDetails(accessToken, ruleRevisionID, &api.GetOneRuleRevisionDetailsParams{
		DatasetID:    &datasetID,
		UpgradedFrom: upgradedFrom,
	})
}

func (c *Client) getRuleRevisionDetails(accessToken string, ruleRevisionID string, params *api.GetOneRuleRevisionDetailsParams) (*RuleRevisionFull, error) {
	revisionUUID, err := uuid.Parse(ruleRevisionID)
	if err != nil {
		return nil, fmt.Errorf("invalid rule revision ID: %w", err)
	}

	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetOneRuleRevisionDetailsWithResponse(
		context.Background(), c.organizationID, revisionUUID, params,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	var detail RuleRevisionFull
	if err := jsonnum.Unmarshal(resp.Body, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode rule revision response: %w", err)
	}
	return &detail, nil
}

// RuleVersionUnsubmitResponse represents the response from rule version
// unsubmit. UnsubmitRuleVersion leaves no list nil.
type RuleVersionUnsubmitResponse struct {
	Message  string                 `json:"message"`
	Deleted  []UnsubmitResultItem   `json:"deleted"`
	NotFound []UnsubmitResultItem   `json:"not_found"`
	Skipped  []UnsubmitSkippedGroup `json:"skipped"`
}

// PatchRuleRevision patches a rule revision's mutable fields
func (c *Client) PatchRuleRevision(accessToken string, ruleRevisionID string, req api.PatchRuleRevisionJSONRequestBody) (*api.PatchRuleRevisionResponseSchema, error) {
	revisionUUID, err := uuid.Parse(ruleRevisionID)
	if err != nil {
		return nil, fmt.Errorf("invalid rule revision ID: %w", err)
	}

	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.PatchRuleRevisionWithResponse(
		context.Background(), c.organizationID, revisionUUID, &api.PatchRuleRevisionParams{}, req,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "patch failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "patch failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	return resp.JSON200, nil
}

// DeleteRuleRevision deletes a rule revision by ID
func (c *Client) DeleteRuleRevision(accessToken string, ruleRevisionID string) error {
	revisionUUID, err := uuid.Parse(ruleRevisionID)
	if err != nil {
		return fmt.Errorf("invalid rule revision ID: %w", err)
	}

	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.DeleteRuleRevisionWithResponse(
		context.Background(), c.organizationID, revisionUUID, &api.DeleteRuleRevisionParams{},
	)
	if err != nil {
		return apiClient.HandleError(err, "delete failed")
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "delete failed")
	}

	return nil
}

// ResolveRuleID resolves a name, short ID, or full UUID to a rule revision UUID string.
// It requires a release filter when the rule has multiple releases (strict mode).
func (c *Client) ResolveRuleID(accessToken, input, release string) (string, error) {
	resolved, err := c.ResolveRuleFull(accessToken, input, release)
	if err != nil {
		return "", err
	}
	return resolved.ID, nil
}

// ResolveRuleIDAny resolves a name, short ID, or full UUID to any matching rule revision UUID.
// Unlike ResolveRuleID, it does NOT error when multiple releases exist — it picks the first match.
func (c *Client) ResolveRuleIDAny(accessToken, input string) (string, error) {
	resolved, err := c.resolve(accessToken, input, "", func(rules []RuleRevisionTiny, in, _ string) (*ResolvedRule, error) {
		return ResolveRuleAny(rules, in)
	})
	if err != nil {
		return "", err
	}
	return resolved.ID, nil
}

// ResolveRuleFull resolves a name, short ID, or full UUID to a full ResolvedRule struct.
// It requires a release filter when the rule has multiple releases (strict mode).
func (c *Client) ResolveRuleFull(accessToken, input, release string) (*ResolvedRule, error) {
	return c.resolve(accessToken, input, release, ResolveRule)
}

// resolve is the shared resolution flow; sel supplies the strict (ResolveRule)
// or lenient (ResolveRuleAny) choice among candidates.
//
// The branch on IsUUIDPrefix is load-bearing, not an optimization. The backend
// cannot search by id: the rule-revision list searches name, slug, release,
// the descriptions and the column arrays, and nothing else
// (get_rule_revision_list's search_in_fields,
// ../atlas/capi/capi/views/rule_view.py), while any query longer than two
// characters still emits a real WHERE clause (get_search_filters,
// ../atlas/capi/capi/views/pagination.py). A prefix-shaped input is therefore
// searched only to establish whether it is an exact name or slug. If not, it is
// expanded locally -- from the cache first, then from a bounded unfiltered
// catalog listing.
func (c *Client) resolve(
	accessToken, input, release string,
	sel func([]RuleRevisionTiny, string, string) (*ResolvedRule, error),
) (*ResolvedRule, error) {
	// 1. Full UUID — no cache, no request.
	if resolved, err := sel(nil, input, release); err == nil {
		return resolved, nil
	}

	// 2. A hex-only name must win over an identically spelled UUID prefix. The
	// search endpoint cannot match ids, so only an exact name/slug result is
	// authoritative here; fuzzy results must not preempt prefix expansion.
	if IsUUIDPrefix(input) {
		revisions, err := c.GetAllRuleRevisions(accessToken, GetRuleRevisionsParams{
			SearchQuery: &input,
			Limit:       resolutionListLimit,
			OnlyDefault: BoolPtr(false),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list rules for resolution: %w", err)
		}
		exactMatches, exactInput := findExactInputMatches(revisions, input)
		if len(exactMatches) > 0 {
			// Do not cache fuzzy search hits here: the search result is not an
			// exhaustive view of UUID prefixes, so caching one such hit could
			// make an actually ambiguous prefix look unique.
			c.rememberRevisions(exactMatches)
			return sel(exactMatches, exactInput, release)
		}

		// 3. Short ID / UUID prefix — expand locally after the exact-name miss.
		if hexLen(input) >= minCachedPrefixLen {
			if full, ok, err := aliasstore.ExpandShortID(c.baseURL, c.organizationID.String(), aliasstore.ResourceRuleRevision, input); err == nil && ok {
				return &ResolvedRule{ID: full}, nil
			}
		}
		resp, err := c.GetRuleRevisions(accessToken, GetRuleRevisionsParams{
			Limit:       resolutionListLimit,
			OnlyDefault: BoolPtr(false),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list rules for resolution: %w", err)
		}
		c.rememberRevisions(resp.Results)
		resolved, selErr := sel(resp.Results, input, release)
		if selErr != nil && resp.TotalRows != nil && len(resp.Results) < *resp.TotalRows {
			rest := fmt.Sprintf(
				"the catalog holds %d rule revisions but resolution only scanned the first %d (resolutionListLimit); "+
					"run 'qctl get rules' or 'qctl get rule-revisions' to cache the rest locally, then retry",
				*resp.TotalRows, len(resp.Results),
			)
			return nil, fmt.Errorf("%w\n\n%s", selErr, rest)
		}
		return resolved, selErr
	}

	// 4. Non-hex name or slug — the backend can search for these.
	revisions, err := c.GetAllRuleRevisions(accessToken, GetRuleRevisionsParams{
		SearchQuery: &input,
		Limit:       resolutionListLimit,
		OnlyDefault: BoolPtr(false),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list rules for resolution: %w", err)
	}
	c.rememberRevisions(revisions)
	return sel(revisions, input, release)
}

// rememberRevisions seeds the short-ID cache with everything a listing just
// returned. Best-effort by design: a cache write failure costs one extra
// listing next time and must never fail the user's command.
func (c *Client) rememberRevisions(revisions []RuleRevisionTiny) {
	if len(revisions) == 0 {
		return
	}
	ids := make([]string, 0, len(revisions))
	for _, r := range revisions {
		ids = append(ids, r.ID.String())
	}
	RememberRevisionShortIDs(c.baseURL, c.organizationID.String(), ids)
}

// RememberRevisionShortIDs records SHORT-ID -> full UUID for rule revisions the
// user has just been shown, so a later `qctl get rule def8757c` or
// `spec.rule_revision_id: def8757c` expands without listing the catalog.
// Best-effort: errors are dropped, the resolver falls back to listing.
func RememberRevisionShortIDs(serverURL, orgID string, ids []string) {
	_ = aliasstore.RememberShortIDs(serverURL, orgID, aliasstore.ResourceRuleRevision, ids)
}

// UnsubmitRuleVersion unsubmits rule versions matching the provided source file
func (c *Client) UnsubmitRuleVersion(accessToken string, fileText string) (*RuleVersionUnsubmitResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	apiReq := api.UnsubmitRuleRevisionJSONRequestBody{
		FileText: fileText,
	}

	resp, err := apiClient.API.UnsubmitRuleRevisionWithResponse(
		context.Background(), c.organizationID, &api.UnsubmitRuleRevisionParams{}, apiReq,
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "unsubmit failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "unsubmit failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	result := &RuleVersionUnsubmitResponse{
		Message:  resp.JSON200.Message,
		Deleted:  []UnsubmitResultItem{},
		NotFound: []UnsubmitResultItem{},
		Skipped:  []UnsubmitSkippedGroup{},
	}

	if resp.JSON200.Deleted != nil {
		result.Deleted = *resp.JSON200.Deleted
	}

	if resp.JSON200.NotFound != nil {
		result.NotFound = *resp.JSON200.NotFound
	}

	if resp.JSON200.Skipped != nil {
		result.Skipped = *resp.JSON200.Skipped
	}

	return result, nil
}
