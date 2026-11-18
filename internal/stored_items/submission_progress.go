package stored_items

import (
	"context"
	"fmt"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
)

// SubmissionProgress is a file of the caller's granted sources with each step
// of its way to its table.
type SubmissionProgress = api.SubmissionFileProgressSchema

// GetSubmissionProgress reads a file's progress with
// GET /submissions/files/{id}. A non-2xx answer is an *APIError carrying the
// response's error_code.
func (c *Client) GetSubmissionProgress(accessToken string, storedItemID int) (*SubmissionProgress, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetSubmissionFileStateWithResponse(
		context.Background(), c.organizationID, storedItemID, &api.GetSubmissionFileStateParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// FileSubmission is the status form of a file's submission progress.
type FileSubmission struct {
	State         string               `yaml:"state" json:"state"`
	Outcome       *string              `yaml:"outcome" json:"outcome"`
	SeriesID      *string              `yaml:"series_id" json:"series_id"`
	SeriesName    string               `yaml:"series_name" json:"series_name"`
	PeriodKey     *string              `yaml:"period_key" json:"period_key"`
	PeriodStart   *string              `yaml:"period_start" json:"period_start"`
	PeriodEnd     *string              `yaml:"period_end" json:"period_end"`
	BlockingAlert *FileBlockingAlert   `yaml:"blocking_alert,omitempty" json:"blocking_alert,omitempty"`
	Steps         []FileSubmissionStep `yaml:"steps" json:"steps"`
}

// FileBlockingAlert names the alert holding a file.
type FileBlockingAlert struct {
	ID        int    `yaml:"id" json:"id"`
	IssueType string `yaml:"issue_type" json:"issue_type"`
}

// FileSubmissionStep is one step of a file's way to its table.
type FileSubmissionStep struct {
	Step           string            `yaml:"step" json:"step"`
	State          string            `yaml:"state" json:"state"`
	Actions        []string          `yaml:"actions,omitempty" json:"actions,omitempty"`
	AlertID        *int              `yaml:"alert_id,omitempty" json:"alert_id,omitempty"`
	ChangeProposal *FileProposalWait `yaml:"change_proposal,omitempty" json:"change_proposal,omitempty"`
}

// FileProposalWait is the change proposal a step waits on.
type FileProposalWait struct {
	ID            string   `yaml:"id" json:"id"`
	State         string   `yaml:"state" json:"state"`
	ApproverRoles []string `yaml:"approver_roles" json:"approver_roles"`
	AuthorIDs     []string `yaml:"author_ids" json:"author_ids"`
}

// NewFileSubmission is the status form of p; nil for a nil p.
func NewFileSubmission(p *SubmissionProgress) *FileSubmission {
	if p == nil {
		return nil
	}
	out := &FileSubmission{
		State:       string(p.State),
		SeriesName:  p.SeriesName,
		PeriodKey:   p.PeriodKey,
		PeriodStart: formatDate(p.PeriodStart),
		PeriodEnd:   formatDate(p.PeriodEnd),
		Steps:       make([]FileSubmissionStep, 0, len(p.Steps)),
	}
	if p.Outcome != nil {
		outcome := string(*p.Outcome)
		out.Outcome = &outcome
	}
	if p.SeriesID != nil {
		id := p.SeriesID.String()
		out.SeriesID = &id
	}
	if a := p.BlockingAlert; a != nil {
		out.BlockingAlert = &FileBlockingAlert{ID: a.ID, IssueType: string(a.IssueType)}
	}
	for _, s := range p.Steps {
		step := FileSubmissionStep{Step: string(s.Step), State: string(s.State), AlertID: s.AlertID}
		for _, action := range s.Actions {
			step.Actions = append(step.Actions, string(action))
		}
		if w := s.ChangeProposal; w != nil {
			wait := &FileProposalWait{
				ID:            w.ID.String(),
				State:         string(w.State),
				ApproverRoles: make([]string, 0, len(w.ApproverRoles)),
				AuthorIDs:     make([]string, 0, len(w.AuthorIds)),
			}
			for _, role := range w.ApproverRoles {
				wait.ApproverRoles = append(wait.ApproverRoles, string(role))
			}
			for _, id := range w.AuthorIds {
				wait.AuthorIDs = append(wait.AuthorIDs, id.String())
			}
			step.ChangeProposal = wait
		}
		out.Steps = append(out.Steps, step)
	}
	return out
}

func formatDate(d *openapi_types.Date) *string {
	if d == nil {
		return nil
	}
	s := d.Format(openapi_types.DateFormat)
	return &s
}
