package deliverables

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// RunPage is one page of a deliverable's runs.
type RunPage = api.DeliverableRunListSchema

// OrganizationRunPage is one page of runs across an organization.
type OrganizationRunPage = api.OrganizationDeliverableRunListSchema

// RunStates are the run states in lifecycle order.
var RunStates = []api.DeliverableRunState{
	api.DeliverableRunStateOpen,
	api.DeliverableRunStateRendered,
	api.DeliverableRunStateStale,
	api.DeliverableRunStateSignedOff,
	api.DeliverableRunStateDelivered,
	api.DeliverableRunStateCancelled,
}

var runStateLabels = map[api.DeliverableRunState]string{
	api.DeliverableRunStateOpen:      "In progress",
	api.DeliverableRunStateRendered:  "Draft ready",
	api.DeliverableRunStateStale:     "Draft out of date",
	api.DeliverableRunStateSignedOff: "Signed off",
	api.DeliverableRunStateDelivered: "Delivered",
	api.DeliverableRunStateCancelled: "Cancelled",
}

// RunStateLabel returns the web app's word for state, or state itself when it
// has none.
func RunStateLabel(state api.DeliverableRunState) string {
	if label, ok := runStateLabels[state]; ok {
		return label
	}
	return string(state)
}

// RunListParams selects one page of runs. A zero value sends
// nothing.
type RunListParams struct {
	Page  int
	Limit int
	// Reverse flips the server's default newest-first order.
	Reverse bool
	State   string
}

func (p RunListParams) apiParams() *api.ListDeliverableRunsParams {
	out := &api.ListDeliverableRunsParams{}
	if p.Page > 0 {
		out.Page = &p.Page
	}
	if p.Limit > 0 {
		out.Limit = &p.Limit
	}
	if p.Reverse {
		notReversed := false
		out.Reverse = &notReversed
	}
	if p.State != "" {
		state := api.DeliverableRunState(p.State)
		out.State = &state
	}
	return out
}

// ListRuns reads one page of a deliverable's runs. Numbers in the runs'
// interface{} fields decode as jsonnum.Unmarshal decodes them.
func (c *Client) ListRuns(token string, deliverableID uuid.UUID, params RunListParams) (*RunPage, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	return c.listRuns(apiClient, deliverableID, params.apiParams())
}

// ListOrganizationRuns reads one page of the organization's runs.
func (c *Client) ListOrganizationRuns(token string, params RunListParams) (*OrganizationRunPage, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	p := params.apiParams()
	resp, err := apiClient.API.ListOrganizationDeliverableRunsWithResponse(context.Background(), c.orgID,
		&api.ListOrganizationDeliverableRunsParams{Page: p.Page, Limit: p.Limit, Reverse: p.Reverse, State: p.State})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "list organization deliverable runs failed", "list_organization_deliverable_runs")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list organization deliverable runs failed", "list_organization_deliverable_runs")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty organization deliverable run list response")
	}
	var page OrganizationRunPage
	if err := jsonnum.Unmarshal(resp.Body, &page); err != nil {
		return nil, fmt.Errorf("decode organization deliverable run list: %w", err)
	}
	return &page, nil
}

// GetRun reads one run. Numbers in its interface{} fields decode as
// jsonnum.Unmarshal decodes them.
func (c *Client) GetRun(token string, runID int) (*Run, error) {
	return c.getRun(context.Background(), token, runID)
}

func (c *Client) getRun(ctx context.Context, token string, runID int) (*Run, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ReadDeliverableRunWithResponse(ctx, c.orgID, runID, &api.ReadDeliverableRunParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get deliverable run failed", "read_deliverable_run")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get deliverable run failed", "read_deliverable_run")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty deliverable run response")
	}
	var run Run
	if err := jsonnum.Unmarshal(resp.Body, &run); err != nil {
		return nil, fmt.Errorf("decode deliverable run: %w", err)
	}
	return &run, nil
}

// allRunsPageSize is the limit of each listing AllRuns makes.
const allRunsPageSize = 500

// AllRuns lists every run of a deliverable, ascending by id.
func (c *Client) AllRuns(token string, deliverableID uuid.UUID) ([]Run, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	limit, orderBy, reverse := allRunsPageSize, "id", false
	var runs []Run
	for page := 1; ; page++ {
		params := &api.ListDeliverableRunsParams{Page: &page, Limit: &limit, OrderBy: &orderBy, Reverse: &reverse}
		list, err := c.listRuns(apiClient, deliverableID, params)
		if err != nil {
			return nil, err
		}
		var results []Run
		if list.Results != nil {
			results = *list.Results
		}
		runs = append(runs, results...)
		if len(results) < limit || (list.TotalRows != nil && len(runs) >= *list.TotalRows) {
			return runs, nil
		}
	}
}

var revisionModeTexts = map[api.DeliverableRunRevisionMode]string{
	api.NewPeriod:          "new period",
	api.ReissueAsDelivered: "re-issue as delivered",
	api.ReissueEnabled:     "re-issue on the enabled revision",
}

// RevisionModeText returns mode in words, or mode itself when it has none.
func RevisionModeText(mode api.DeliverableRunRevisionMode) string {
	if text, ok := revisionModeTexts[mode]; ok {
		return text
	}
	return string(mode)
}
