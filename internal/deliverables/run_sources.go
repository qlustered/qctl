package deliverables

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// SourceRef names one of a run's sources by exactly one of: the sheet name of
// its sheet binding (Sheet), the sheet and A1 reference of its cell binding
// (CellSheet, CellRef), or its id (ID).
type SourceRef struct {
	Sheet     string
	CellSheet string
	CellRef   string
	ID        int
}

// ParseSourceCell splits Sheet!A1 at its last "!" and normalizes the A1
// reference as NormalizeCellRef does; anything else is bad usage.
func ParseSourceCell(value string) (sheet, ref string, err error) {
	return splitCellKey(value)
}

// RunSource is one of a run's sources and the name of the binding it reads
// for: `sheet "Name"` or `cell Sheet!A1`.
type RunSource struct {
	api.RunSourceResponse
	Name string
}

// FindRunSource returns the source of run that ref names. revision is the
// run's binding tree, which resolves sheet and cell names to binding ids.
func FindRunSource(run *Run, revision *RevisionDetail, ref SourceRef) (*RunSource, error) {
	sources := copied(run.Sources)
	names := newBindingNames(revision)
	if ref.ID != 0 {
		for _, source := range sources {
			if source.ID == ref.ID {
				return &RunSource{RunSourceResponse: source, Name: names.of(source)}, nil
			}
		}
		ids := make([]string, 0, len(sources))
		for _, source := range sources {
			ids = append(ids, strconv.Itoa(source.ID))
		}
		return nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("deliverable run %d has no source %d (its sources: %s)", run.ID, ref.ID, listOrNone(ids))))
	}
	release := runReleaseText(revision, run.RevisionID)
	var subject string
	var bindingID uuid.UUID
	var err error
	if ref.Sheet != "" {
		subject = "sheet " + readinessSheetName(ref.Sheet)
		bindingID, err = sheetBindingID(revision, ref.Sheet, release)
	} else {
		subject = "cell " + cellText(ref.CellSheet) + "!" + ref.CellRef
		bindingID, err = cellBindingID(revision, ref.CellSheet, ref.CellRef, release)
	}
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		binding := source.SheetBindingID
		if ref.Sheet == "" {
			binding = source.CellBindingID
		}
		if binding != nil && *binding == bindingID {
			return &RunSource{RunSourceResponse: source, Name: subject}, nil
		}
	}
	return nil, fmt.Errorf("deliverable run %d has no source for %s of release %s", run.ID, subject, release)
}

func sheetBindingID(revision *RevisionDetail, sheet, release string) (uuid.UUID, error) {
	var names []string
	for _, binding := range revisionSheets(revision) {
		if binding.SheetName == sheet {
			return binding.ID, nil
		}
		names = append(names, readinessSheetName(binding.SheetName))
	}
	return uuid.Nil, qerrors.NewBadUsagef("release %s has no sheet %s (its sheets: %s)", release, readinessSheetName(sheet), listOrNone(names))
}

func cellBindingID(revision *RevisionDetail, sheet, ref, release string) (uuid.UUID, error) {
	var totals []string
	for _, binding := range revisionCells(revision) {
		if binding.SheetName != sheet || !sameCellRef(binding.CellRef, ref) {
			if ownTableTotal(binding) {
				totals = append(totals, cellText(binding.SheetName)+"!"+cellText(binding.CellRef))
			}
			continue
		}
		subject := "cell " + cellText(sheet) + "!" + ref
		switch {
		case binding.SourceType != api.DeliverableCellSourceTypeAggregate:
			return uuid.Nil, qerrors.NewBadUsagef("%s is a %s cell and reads no table; only a total (source_type aggregate) has a source", subject, cellText(string(binding.SourceType)))
		case binding.SameRowsAsSheetBindingID != nil:
			mirrored := sheetNameOf(revision, *binding.SameRowsAsSheetBindingID)
			return uuid.Nil, qerrors.NewBadUsagef("%s totals the rows of sheet %s and reads no table of its own; override that sheet instead: --sheet %s", subject, readinessSheetName(mirrored), strconv.Quote(mirrored))
		}
		return binding.ID, nil
	}
	return uuid.Nil, qerrors.NewBadUsagef("release %s has no cell %s!%s (totals that read their own table: %s)", release, cellText(sheet), ref, listOrNone(totals))
}

// ownTableTotal reports whether binding is a total that reads a table of its
// own, which gives it a run source.
func ownTableTotal(binding api.CellBindingResponse) bool {
	return binding.SourceType == api.DeliverableCellSourceTypeAggregate && binding.SameRowsAsSheetBindingID == nil
}

func sameCellRef(stored, ref string) bool {
	normalized, err := NormalizeCellRef(stored)
	if err != nil {
		return stored == ref
	}
	return normalized == ref
}

func sheetNameOf(revision *RevisionDetail, id uuid.UUID) string {
	for _, binding := range revisionSheets(revision) {
		if binding.ID == id {
			return binding.SheetName
		}
	}
	return id.String()
}

func revisionSheets(revision *RevisionDetail) []api.SheetBindingResponse {
	if revision == nil || revision.DeliverableRevisionResponse == nil {
		return nil
	}
	return copied(revision.SheetBindings)
}

func revisionCells(revision *RevisionDetail) []api.CellBindingResponse {
	if revision == nil || revision.DeliverableRevisionResponse == nil {
		return nil
	}
	return copied(revision.CellBindings)
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// CanOverrideSources reports whether a run in state can have its sources
// overridden: open, rendered or stale.
func CanOverrideSources(state api.DeliverableRunState) bool {
	return slices.Contains([]api.DeliverableRunState{
		api.DeliverableRunStateOpen, api.DeliverableRunStateRendered, api.DeliverableRunStateStale,
	}, state)
}

// OverrideResult is an override's answer: the run with its sources, and the
// original response body.
type OverrideResult struct {
	Run  *Run
	Body json.RawMessage
}

// OverrideSource points source sourceID of run runID at table tableID,
// recording reason. Numbers in the run's interface{} fields decode as
// jsonnum.Unmarshal decodes them.
func (c *Client) OverrideSource(token string, runID, sourceID, tableID int, reason string) (*OverrideResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	const failed = "override deliverable run source failed"
	resp, err := apiClient.API.OverrideDeliverableRunSourceWithResponse(context.Background(), c.orgID, runID, sourceID,
		&api.OverrideDeliverableRunSourceParams{}, api.RunSourceOverrideRequest{DatasetID: tableID, Reason: reason})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, failed, "override_deliverable_run_source")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, failed, "override_deliverable_run_source")
	}
	var run Run
	if err := jsonnum.Unmarshal(resp.Body, &run); err != nil {
		return nil, fmt.Errorf("decode deliverable run: %w", err)
	}
	return &OverrideResult{Run: &run, Body: resp.Body}, nil
}

// FormatSourceOverride is the line naming the table source now reads, the
// table it read before and the one resolution picked, then its reason. before
// is source as read before the override; after is the override's answer.
func FormatSourceOverride(before *RunSource, after *Run) string {
	now := before.RunSourceResponse
	if source := sourceByID(after, before.ID); source != nil {
		now = *source
	}
	text := fmt.Sprintf("deliverable-run/%d: %s now reads table %d (was %d; resolved %d)\n", after.ID, before.Name, now.DatasetID, before.DatasetID, now.ResolvedDatasetID)
	if now.OverrideReason != nil {
		text += "Reason: " + cellText(*now.OverrideReason) + "\n"
	}
	return text
}

// SourceOverrideNotices returns the warning the override's answer carries for
// source, and a line when the answer's run is stale saying it needs a
// re-render. beforeState is the run's state before the override.
func SourceOverrideNotices(source *RunSource, beforeState api.DeliverableRunState, after *Run) []string {
	var notices []string
	if now := sourceByID(after, source.ID); now != nil && now.Warning != nil && *now.Warning != "" {
		notices = append(notices, fmt.Sprintf("Warning (%s): %s", source.Name, cellText(*now.Warning)))
	}
	if after.State != api.DeliverableRunStateStale {
		return notices
	}
	reason := cellText(pointerText(after.StaleReason))
	render := fmt.Sprintf("re-render it to use the new table: qctl deliverable-run render %d --wait", after.ID)
	if beforeState == api.DeliverableRunStateRendered {
		return append(notices, fmt.Sprintf("Run %d went stale (%s): its draft file is out of date; %s", after.ID, reason, render))
	}
	return append(notices, fmt.Sprintf("Run %d is stale (%s); %s", after.ID, reason, render))
}

func sourceByID(run *Run, id int) *api.RunSourceResponse {
	for _, source := range copied(run.Sources) {
		if source.ID == id {
			return &source
		}
	}
	return nil
}
