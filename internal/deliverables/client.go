// Package deliverables manages deliverables and their revisions and reads their runs.
package deliverables

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

type Deliverable = api.DeliverableResponse
type ListItem = api.DeliverableListItem
type Page = api.DeliverableListSchema
type Revision = api.DeliverableRevisionListItem
type Run = api.DeliverableRunResponse

// Client manages deliverables in one organization.
type Client struct {
	baseURL   string
	orgID     uuid.UUID
	verbosity int
}

// NewClient creates a deliverables client.
func NewClient(baseURL, orgID string, verbosity int) *Client {
	id, _ := uuid.Parse(orgID)
	return &Client{baseURL: baseURL, orgID: id, verbosity: verbosity}
}

func (c *Client) newAPIClient(token string) (*client.Client, error) {
	return c.newAPIClientWithin(token, 0)
}

// newAPIClientWithin is newAPIClient with own as the deadline that
// client.ResolveTimeout resolves.
func (c *Client) newAPIClientWithin(token string, own time.Duration) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: token,
		Timeout: client.ResolveTimeout(own), Verbosity: c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	return apiClient, nil
}

// ListParams selects one page of deliverables. A zero value sends nothing.
type ListParams struct {
	Page    int
	Limit   int
	OrderBy string
	// Reverse flips the server's default descending order.
	Reverse         bool
	IncludeArchived bool
	OnlyArchived    bool
	Search          string
	// MemberDatasetID is the table the deliverables must read.
	MemberDatasetID int
}

func (p ListParams) apiParams() *api.ListDeliverablesParams {
	out := &api.ListDeliverablesParams{}
	if p.Page > 0 {
		out.Page = &p.Page
	}
	if p.Limit > 0 {
		out.Limit = &p.Limit
	}
	if p.OrderBy != "" {
		order := api.DeliverableOrderBy(p.OrderBy)
		out.OrderBy = &order
	}
	if p.Reverse {
		notReversed := false
		out.Reverse = &notReversed
	}
	if p.IncludeArchived {
		out.IncludeArchived = &p.IncludeArchived
	}
	if p.OnlyArchived {
		out.OnlyArchived = &p.OnlyArchived
	}
	if p.Search != "" {
		out.SearchQuery = &p.Search
	}
	if p.MemberDatasetID != 0 {
		out.MemberDatasetID = &p.MemberDatasetID
	}
	return out
}

// List reads one page of deliverables. Numbers in each latest run's
// parameters decode as jsonnum.Unmarshal decodes them.
func (c *Client) List(token string, params ListParams) (*Page, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ListDeliverablesWithResponse(context.Background(), c.orgID, params.apiParams())
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list deliverables failed", "list_deliverables")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list deliverables failed", "list_deliverables")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable list response")
	}
	var page Page
	if err := jsonnum.Unmarshal(resp.Body, &page); err != nil {
		return nil, fmt.Errorf("decode deliverable list: %w", err)
	}
	return &page, nil
}

// Get reads one deliverable by UUID, in the trash or not.
func (c *Client) Get(token string, id uuid.UUID) (*Deliverable, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetDeliverableWithResponse(context.Background(), c.orgID, id, &api.GetDeliverableParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get deliverable failed", "get_deliverable")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get deliverable failed", "get_deliverable")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable response")
	}
	return resp.JSON200, nil
}

// Detail is a deliverable with its revisions and its latest run.
type Detail struct {
	Deliverable *Deliverable `json:"deliverable"`
	Revisions   []Revision   `json:"revisions"`
	LatestRun   *Run         `json:"latest_run"`
}

// Detail reads a deliverable, its revisions and its latest run.
func (c *Client) Detail(token string, id uuid.UUID) (*Detail, error) {
	item, err := c.Get(token, id)
	if err != nil {
		return nil, err
	}
	revisions, err := c.ListRevisions(token, id)
	if err != nil {
		return nil, err
	}
	run, err := c.LatestRun(token, id)
	if err != nil {
		return nil, err
	}
	return &Detail{Deliverable: item, Revisions: revisions, LatestRun: run}, nil
}

// ListRevisions reads every revision of a deliverable.
func (c *Client) ListRevisions(token string, id uuid.UUID) ([]Revision, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ListDeliverableRevisionsWithResponse(context.Background(), c.orgID, id, &api.ListDeliverableRevisionsParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list deliverable revisions failed", "list_deliverable_revisions")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list deliverable revisions failed", "list_deliverable_revisions")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable revision list response")
	}
	return *resp.JSON200, nil
}

// LatestRun reads a deliverable's run with the highest id, or nil when it has
// none. Numbers in its interface{} fields decode as jsonnum.Unmarshal decodes
// them.
func (c *Client) LatestRun(token string, id uuid.UUID) (*Run, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	one := 1
	page, err := c.listRuns(apiClient, id, &api.ListDeliverableRunsParams{Limit: &one})
	if err != nil {
		return nil, err
	}
	if page.Results == nil || len(*page.Results) == 0 {
		return nil, nil
	}
	return &(*page.Results)[0], nil
}

// listRuns reads one page of a deliverable's runs. Numbers in the runs'
// interface{} fields decode as jsonnum.Unmarshal decodes them.
func (c *Client) listRuns(apiClient *client.Client, id uuid.UUID, params *api.ListDeliverableRunsParams) (*api.DeliverableRunListSchema, error) {
	resp, err := apiClient.API.ListDeliverableRunsWithResponse(context.Background(), c.orgID, id, params)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list deliverable runs failed", "list_deliverable_runs")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list deliverable runs failed", "list_deliverable_runs")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable run list response")
	}
	var page api.DeliverableRunListSchema
	if err := jsonnum.Unmarshal(resp.Body, &page); err != nil {
		return nil, fmt.Errorf("decode deliverable run list: %w", err)
	}
	return &page, nil
}
