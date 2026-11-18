package deliverables

import (
	"strconv"

	"github.com/qlustered/qctl/internal/api"
)

// RunColumns are the default run listing columns.
const RunColumns = "id,sequence,state,revision_mode,params,rendered_at,updated_at"

// RunDisplay is one run's table row.
type RunDisplay struct {
	ID                 string `json:"id"`
	DeliverableID      string `json:"deliverable_id"`
	Deliverable        string `json:"deliverable"`
	Sequence           string `json:"sequence"`
	State              string `json:"state"`
	RevisionMode       string `json:"revision_mode"`
	Params             string `json:"params"`
	Attempt            string `json:"attempt"`
	RenderedAt         string `json:"rendered_at"`
	RenderError        string `json:"render_error"`
	StaleReason        string `json:"stale_reason"`
	RevisionID         string `json:"revision_id"`
	RenderedRevisionID string `json:"rendered_revision_id"`
	SignedOffAt        string `json:"signed_off_at"`
	DeliveredAt        string `json:"delivered_at"`
	CancelledAt        string `json:"cancelled_at"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// OrganizationRunRows returns one table row per organization run.
func OrganizationRunRows(runs []api.OrganizationDeliverableRunListItem) []RunDisplay {
	base := make([]Run, len(runs))
	for i, run := range runs {
		base[i] = Run{
			ID: run.ID, DeliverableID: run.DeliverableID, Sequence: run.Sequence,
			State: run.State, RevisionMode: run.RevisionMode, RunParams: run.RunParams, RunParamsTyped: run.RunParamsTyped,
			AttemptID: run.AttemptID, RenderedAt: run.RenderedAt, RenderError: run.RenderError, StaleReason: run.StaleReason,
			RevisionID: run.RevisionID, RenderedRevisionID: run.RenderedRevisionID,
			SignedOffAt: run.SignedOffAt, DeliveredAt: run.DeliveredAt, CancelledAt: run.CancelledAt,
			CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
		}
	}
	rows := RunRows(base)
	for i, run := range runs {
		rows[i].Deliverable = cellText(run.DeliverableName)
	}
	return rows
}

// RunRows returns one table row per run.
func RunRows(runs []Run) []RunDisplay {
	rows := make([]RunDisplay, len(runs))
	for i, run := range runs {
		renderError := "-"
		if run.RenderError != nil {
			renderError = dash(cellText(*run.RenderError))
		}
		rows[i] = RunDisplay{
			ID: strconv.Itoa(run.ID), DeliverableID: run.DeliverableID.String(), Sequence: strconv.Itoa(run.Sequence),
			State: string(run.State), RevisionMode: string(run.RevisionMode),
			Params: cellText(RunParamsText(run.RunParamsTyped, run.RunParams)), Attempt: strconv.Itoa(run.AttemptID),
			RenderedAt: relative(run.RenderedAt), RenderError: renderError, StaleReason: dash(pointerText(run.StaleReason)),
			RevisionID: run.RevisionID.String(), RenderedRevisionID: pointerText(run.RenderedRevisionID),
			SignedOffAt: relative(run.SignedOffAt), DeliveredAt: relative(run.DeliveredAt),
			CancelledAt: relative(run.CancelledAt), CreatedAt: relative(run.CreatedAt), UpdatedAt: relative(run.UpdatedAt),
		}
	}
	return rows
}
