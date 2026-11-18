package concepts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/org"
)

type Tiny = api.ConceptTinySchema
type Full = api.ConceptSchemaFull
type Page = api.ConceptsListSchema

// Client reads concepts in one organization.
type Client struct {
	baseURL   string
	orgID     uuid.UUID
	verbosity int
	listLimit int
}

// NewClient creates a concept client.
func NewClient(baseURL, orgID string, verbosity int) *Client {
	id, _ := uuid.Parse(orgID)
	return &Client{baseURL: baseURL, orgID: id, verbosity: verbosity, listLimit: 500}
}

// ListParams selects a page of concepts.
type ListParams struct {
	SearchQuery     *string
	ProblemDomainID *uuid.UUID
	IncludeBuiltin  *bool
	OrderBy         string
	Page            int
	Limit           int
	Reverse         bool
}

// List reads a page of concepts.
func (c *Client) List(token string, params ListParams) (*Page, error) {
	apiClient, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: client.ResolveTimeout(0), Verbosity: c.verbosity})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	wire := &api.GetConceptsParams{SearchQuery: params.SearchQuery, ProblemDomainID: params.ProblemDomainID, IncludeBuiltin: params.IncludeBuiltin}
	if params.Page > 0 {
		wire.Page = &params.Page
	}
	if params.Limit > 0 {
		wire.Limit = &params.Limit
	}
	if params.OrderBy != "" {
		order := api.ConceptOrderBy(params.OrderBy)
		wire.OrderBy = &order
	}
	if params.Reverse {
		value := true
		wire.Reverse = &value
	}
	resp, err := apiClient.API.GetConceptsWithResponse(context.Background(), c.orgID, wire)
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to list standard fields")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to list standard fields")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty standard field list response")
	}
	return resp.JSON200, nil
}

// Get reads one concept by UUID.
func (c *Client) Get(token string, id uuid.UUID) (*Full, error) {
	apiClient, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: client.ResolveTimeout(0), Verbosity: c.verbosity})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	resp, err := apiClient.API.GetOneConceptWithResponse(context.Background(), c.orgID, id, &api.GetOneConceptParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "failed to get standard field")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to get standard field")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty standard field response")
	}
	return resp.JSON200, nil
}

// BySlug finds a standard field by its exact slug.
func (c *Client) BySlug(token, slug string) (*Full, error) {
	items, err := c.listAll(token, ListParams{Limit: c.listLimit, OrderBy: "slug", Page: 1})
	if err != nil {
		return nil, fmt.Errorf("list standard fields: %w", err)
	}
	for _, item := range items {
		if item.Slug == slug {
			return c.Get(token, item.ID)
		}
	}
	return nil, nil
}

// Apply sends a prepared create or patch request.
func (c *Client) Apply(token string, plan *Plan) (*Full, error) {
	body, err := plan.RequestJSON()
	if err != nil {
		return nil, err
	}
	apiClient, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: client.ResolveTimeout(0), Verbosity: c.verbosity})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	if plan.Create {
		resp, err := apiClient.API.CreateConceptWithBodyWithResponse(context.Background(), c.orgID, &api.CreateConceptParams{}, "application/json", bytes.NewReader(body))
		if err != nil {
			return nil, wrapAPIError(apiClient, err, "failed to create standard field")
		}
		if resp.StatusCode() != http.StatusCreated {
			return nil, comments.HandleError(resp.StatusCode(), resp.Body, "failed to create standard field")
		}
		if resp.JSON201 == nil {
			return nil, fmt.Errorf("unexpected empty create standard field response")
		}
		return resp.JSON201, nil
	}
	resp, err := apiClient.API.PatchConceptWithBodyWithResponse(context.Background(), c.orgID, plan.ID, &api.PatchConceptParams{}, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "failed to update standard field")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, "failed to update standard field")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty update standard field response")
	}
	return resp.JSON200, nil
}

// Delete removes one concept by UUID.
func (c *Client) Delete(token string, id uuid.UUID) error {
	apiClient, err := client.New(client.Config{BaseURL: c.baseURL, AccessToken: token, Timeout: client.ResolveTimeout(0), Verbosity: c.verbosity})
	if err != nil {
		return fmt.Errorf("create API client: %w", err)
	}
	resp, err := apiClient.API.DeleteConceptWithResponse(context.Background(), c.orgID, id, &api.DeleteConceptParams{})
	if err != nil {
		return wrapAPIError(apiClient, err, "failed to delete standard field")
	}
	if resp.StatusCode() != http.StatusNoContent {
		return comments.HandleError(resp.StatusCode(), resp.Body, "failed to delete standard field")
	}
	return nil
}

// ErrIncompleteListing means the server did not return its complete counted catalog.
var ErrIncompleteListing = errors.New("incomplete standard field listing")

// ResolveID resolves a full UUID, exact slug or name, UUID prefix, or substring.
func (c *Client) ResolveID(token, input string) (uuid.UUID, error) {
	if input == "" {
		return uuid.Nil, qerrors.NewBadUsage("standard field reference cannot be empty")
	}
	if len(input) == 36 && org.IsUUIDLike(input) {
		id, err := uuid.Parse(input)
		if err != nil {
			return uuid.Nil, qerrors.NewBadUsagef("invalid standard field UUID %q: %v", input, err)
		}
		return id, nil
	}
	params := ListParams{Limit: c.listLimit, OrderBy: "slug", Page: 1}
	if !org.IsUUIDLike(input) && len([]rune(input)) >= 3 {
		params.SearchQuery = &input
	}
	items, err := c.listAll(token, params)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to list standard fields: %w", err)
	}
	return Resolve(items, input)
}

const listAttempts = 3

func (c *Client) listAll(token string, params ListParams) ([]Tiny, error) {
	resp, err := c.List(token, params)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		items := distinct(nil, resp.Results)
		if resp.TotalRows == nil {
			for page := 2; resp.Next != nil && len(resp.Results) > 0; page++ {
				params.Page = page
				resp, err = c.List(token, params)
				if err != nil {
					return nil, fmt.Errorf("page %d: %w", page, err)
				}
				items = distinct(items, resp.Results)
			}
			return items, nil
		}
		if len(resp.Results) < params.Limit && len(items) >= *resp.TotalRows {
			return items, nil
		}
		if attempt == listAttempts {
			return nil, fmt.Errorf("%w: counted %d standard fields, returned %d (%d distinct); retry or use a full UUID", ErrIncompleteListing, *resp.TotalRows, len(resp.Results), len(items))
		}
		params.Limit = max(*resp.TotalRows, len(resp.Results)) + 1
		resp, err = c.List(token, params)
		if err != nil {
			return nil, err
		}
		if resp.TotalRows == nil {
			return nil, fmt.Errorf("%w: server stopped reporting a total", ErrIncompleteListing)
		}
	}
}

func distinct(items, more []Tiny) []Tiny {
	seen := make(map[uuid.UUID]bool, len(items)+len(more))
	for _, item := range items {
		seen[item.ID] = true
	}
	for _, item := range more {
		if !seen[item.ID] {
			seen[item.ID] = true
			items = append(items, item)
		}
	}
	return items
}

// Resolve selects one standard field from a complete catalog.
func Resolve(items []Tiny, input string) (uuid.UUID, error) {
	if input == "" {
		return uuid.Nil, qerrors.NewBadUsage("standard field reference cannot be empty")
	}
	for _, match := range []func(Tiny) bool{
		func(item Tiny) bool { return item.Slug == input || item.Name == input },
		func(item Tiny) bool {
			return org.IsUUIDLike(input) && strings.HasPrefix(strings.ReplaceAll(item.ID.String(), "-", ""), strings.ToLower(strings.ReplaceAll(input, "-", "")))
		},
		func(item Tiny) bool {
			needle := strings.ToLower(input)
			return strings.Contains(strings.ToLower(item.Slug), needle) || strings.Contains(strings.ToLower(item.Name), needle)
		},
	} {
		var found []Tiny
		for _, item := range items {
			if match(item) {
				found = append(found, item)
			}
		}
		if len(found) == 1 {
			return found[0].ID, nil
		}
		if len(found) > 1 {
			var labels []string
			for _, item := range found {
				labels = append(labels, fmt.Sprintf("%s (%s)", item.Slug, item.ID.String()))
			}
			return uuid.Nil, qerrors.NewBadUsagef("multiple standard fields match %q: %s", input, strings.Join(labels, ", "))
		}
	}
	return uuid.Nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("no standard field matches %q; use 'qctl get standard-fields' to list available standard fields", input)))
}
