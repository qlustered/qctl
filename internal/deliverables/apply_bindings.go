package deliverables

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

// replan is what a response changes about the binding writes after it.
type replan struct {
	// tree, when non-nil, is the created release's tree, against which every
	// binding write is planned again.
	tree *api.DeliverableRevisionResponse
	// Otherwise the column writes of sheet are planned again against columns,
	// the column bindings of sheet binding id.
	sheet   string
	id      uuid.UUID
	columns []api.ColumnBindingResponse
}

// replanned returns requests with the requests after request i planned again
// as next says. bindingRequests' refusals are returned as they are.
func (p *Plan) replanned(requests []Request, i int, next *replan) ([]Request, error) {
	done, rest := requests[:i+1], slices.Clone(requests[i+1:])
	if next.tree != nil {
		with, err := p.bindingRequests(next.tree)
		if err != nil {
			return nil, err
		}
		return slices.Concat(done, slices.DeleteFunc(rest, isBindingWrite), with), nil
	}
	columns := func(r Request) bool { return r.Sheet == next.sheet && r.Column != 0 }
	return slices.Concat(done, p.columnRequests(next.sheet, next.id, next.columns), slices.DeleteFunc(rest, columns)), nil
}

func isBindingWrite(r Request) bool {
	return r.step > stepRevision
}

// sendBinding sends and counts a binding write, returning any column replan.
func (c *Client) sendBinding(token string, p *Plan, request Request, r *ApplyResult) (*replan, error) {
	if err := p.refuseUnlistedRemoval(request); err != nil {
		return nil, err
	}
	var next *replan
	var column *api.ColumnBindingResponse
	var err error
	switch request.step {
	case stepCellDelete:
		err = c.DeleteCellBinding(token, p.resultRevision(r).ID, request.CellBindingID)
	case stepCellPatch, stepCellCreate:
		body := maps.Clone(request.Body)
		if request.mirrorSheet != "" {
			id, ok := r.sheetIDs[request.mirrorSheet]
			if !ok {
				return nil, fmt.Errorf("mirror sheet %q has no binding on the target revision", cellText(request.mirrorSheet))
			}
			body[wireSameRowsAs] = id
		}
		if request.step == stepCellPatch {
			_, err = c.PatchCellBinding(token, p.resultRevision(r).ID, request.CellBindingID, body)
		} else {
			_, err = c.CreateCellBinding(token, p.resultRevision(r).ID, body)
		}
	case stepColumnDelete:
		err = c.DeleteColumnBinding(token, request.SheetBindingID, request.ColumnBindingID)
	case stepColumnPatch:
		column, err = c.PatchColumnBinding(token, request.SheetBindingID, request.ColumnBindingID, request.Body)
	case stepColumnCreate:
		column, err = c.CreateColumnBinding(token, request.SheetBindingID, request.Body)
	default:
		next, err = c.sendSheet(token, p.resultRevision(r).ID, request, r)
	}
	if err != nil {
		return nil, p.bindingWriteError(request, err)
	}
	if column != nil {
		for _, warning := range copied(column.Warnings) {
			r.ColumnWarnings = append(r.ColumnWarnings, ColumnBindingWarning{Sheet: request.Sheet, Column: request.Column, Issue: warning})
		}
	}
	r.count(request.step)
	return next, nil
}

// sendSheet sends the sheet binding write request to revision revisionID.
func (c *Client) sendSheet(token string, revisionID uuid.UUID, request Request, r *ApplyResult) (*replan, error) {
	switch request.step {
	case stepSheetCreate:
		created, err := c.CreateSheetBinding(token, revisionID, request.Body)
		if err != nil {
			return nil, err
		}
		r.sheetIDs[request.Sheet] = created.ID
		return &replan{sheet: request.Sheet, id: created.ID, columns: copied(created.ColumnBindings)}, nil
	case stepSheetPatch:
		patched, err := c.PatchSheetBinding(token, revisionID, request.SheetBindingID, request.Body)
		if err != nil || patched.AnchorReport == nil || len(patched.AnchorReport.ColumnBindings) == 0 {
			return nil, err
		}
		r.AnchorReports = append(r.AnchorReports, patched.AnchorReport)
		return &replan{sheet: request.Sheet, id: patched.ID, columns: copied(patched.ColumnBindings)}, nil
	}
	return nil, c.DeleteSheetBinding(token, revisionID, request.SheetBindingID)
}

// refuseUnlistedRemoval checks a DELETE against the confirmed removals.
func (p *Plan) refuseUnlistedRemoval(request Request) error {
	labels := removalLabels([]Request{request})
	mayRemove := p.MayRemove
	if request.step == stepCellDelete {
		mayRemove = p.MayRemoveCells
	}
	if mayRemove || len(labels) == 0 || slices.Contains(p.Removals, labels[0]) {
		return nil
	}
	return fmt.Errorf("refusing to delete %s, which the plan did not list among the removals to confirm", labels[0])
}

// createdTree returns the binding tree of created.
func createdTree(created *api.DeliverableRevisionCreateResponse) *api.DeliverableRevisionResponse {
	return &api.DeliverableRevisionResponse{
		CellBindings: created.CellBindings, DeliverableID: created.DeliverableID, Description: created.Description,
		EnabledAt: created.EnabledAt, EnabledByID: created.EnabledByID, FidelityReport: created.FidelityReport,
		ID: created.ID, Release: created.Release, SheetBindings: created.SheetBindings, SkeletonID: created.SkeletonID,
		SkeletonLossyAcknowledged: created.SkeletonLossyAcknowledged, State: created.State,
		WorkbookInventory: created.WorkbookInventory,
	}
}

// finish reads the release once for r.Manifest and draft confirmation notes.
func (c *Client) finish(token string, p *Plan, r *ApplyResult) error {
	rev := p.resultRevision(r)
	r.Manifest = FromAPI(r.Deliverable, rev)
	if rev == nil {
		return nil
	}
	tree, err := c.GetRevision(token, rev.ID)
	if err != nil {
		return p.readBackFailed(err)
	}
	if (p.managesSheets || p.managesCells) && rev.State == api.DeliverableRevisionStateDraft {
		r.confirmations = p.draftConfirmations(tree.DeliverableRevisionResponse, nil)
	}
	m, err := c.exportManifest(token, r.Deliverable, rev, tree.DeliverableRevisionResponse)
	if err != nil {
		return p.readBackFailed(err)
	}
	r.Manifest = m
	return nil
}

func (p *Plan) readBackFailed(err error) error {
	return fmt.Errorf("deliverable/%s: every write was sent, but reading it back failed: %w", terminal.Sanitize(p.Slug), err)
}

// count records a binding write of step.
func (r *ApplyResult) count(step requestStep) {
	if r.writes == nil {
		r.writes = map[requestStep]int{}
	}
	r.writes[step]++
	r.BindingWrites++
}

// bindingKinds names each binding write step, in step order.
var bindingKinds = []struct {
	step      requestStep
	one, many string
}{
	{stepSheetDelete, "sheet deleted", "sheets deleted"},
	{stepSheetCreate, "sheet created", "sheets created"},
	{stepSheetPatch, "sheet updated", "sheets updated"},
	{stepColumnDelete, "column deleted", "columns deleted"},
	{stepColumnPatch, "column updated", "columns updated"},
	{stepColumnCreate, "column created", "columns created"},
	{stepCellDelete, "cell deleted", "cells deleted"},
	{stepCellPatch, "cell updated", "cells updated"},
	{stepCellCreate, "cell created", "cells created"},
}

func (r *ApplyResult) recordSheets(tree *api.DeliverableRevisionResponse) {
	r.sheetIDs = map[string]uuid.UUID{}
	for _, sheet := range copied(tree.SheetBindings) {
		r.sheetIDs[sheet.SheetName] = sheet.ID
	}
}

// bindingCounts returns r's binding writes counted by kind, such as
// "1 sheet created, 2 columns created", or "" for none.
func (r *ApplyResult) bindingCounts() string {
	var counts []string
	for _, kind := range bindingKinds {
		switch n := r.writes[kind.step]; n {
		case 0:
		case 1:
			counts = append(counts, "1 "+kind.one)
		default:
			counts = append(counts, fmt.Sprintf("%d %s", n, kind.many))
		}
	}
	return strings.Join(counts, ", ")
}
