package history

import (
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
)

// Direction is which way along one object's change chain a command walks.
// Its string form is also the word the outcome line and the error context use.
type Direction string

const (
	Undo Direction = "undo"
	Redo Direction = "redo"
)

// Type aliases for the generated wire types - use these directly.
type (
	StepResult = api.UndoRedoStepResponseSchema
	ActResult  = api.UndoRedoResponseSchema
)

// Target names the ONE object whose chain is being walked.
//
// page_type is REQUIRED by both request schemas and has no default:
// AsyncHistoryController.get_filter_query_and_model_query
// (atlas cettings/cettings/controllers/history_controller.py) is a chain of
// elif branches over it, and there is no branch without a page_type -- so there
// is no org-wide undo to build. Each page type also demands its own companion
// id, or _raise_err_if_empty answers 422 (history_controller.py -- the async
// and sync copies).
//
// That chain closes with APIError(400, 'Page type "..." is not supported.'),
// and this comment used to say an absent or unrecognised value lands there. It
// does not, and nothing qctl can send does. page_type is a required field typed
// to the PageTypesWithHistory enum on UndoRedoStepPostRequestSchema
// (capi/capi/schemas/history_schema.py), so FastAPI rejects both an absent
// and an out-of-vocabulary value with 422 before the controller is
// constructed -- and all five enum members
// (common/common/types.py) have a branch. The else is dead code behind
// these four routes. Do not write a test claiming to exercise it.
//
// page_type never appears in the CLI: it is a fact about which screen the
// browser is on (cosmosview src/.../Menu.tsx maps routes to it), and the
// CLI's equivalent of "which screen" is which subcommand was typed. The three
// constructors below are the whole mapping.
//
// Label is the human name of the object ("table orders"), carried here rather
// than in the command layer so the outcome line has one definition for all six
// commands.
type Target struct {
	PageType          api.PageTypesWithHistory
	DatasetID         *int
	DataSourceModelID *int
	DestinationID     *int
	Label             string
}

// TableTarget selects a table's history under DataView.
func TableTarget(datasetID int, label string) Target {
	return Target{PageType: api.DataView, DatasetID: &datasetID, Label: label}
}

// CloudSourceTarget walks one cloud source's chain.
func CloudSourceTarget(id int) Target {
	return Target{
		PageType:          api.DataSourceModelEdit,
		DataSourceModelID: &id,
		Label:             fmt.Sprintf("cloud-source %d", id),
	}
}

// DestinationTarget walks one destination's chain.
func DestinationTarget(id int) Target {
	return Target{
		PageType:      api.DestinationEdit,
		DestinationID: &id,
		Label:         fmt.Sprintf("destination %d", id),
	}
}

func (t Target) stepBody() api.UndoRedoStepPostRequestSchema {
	return api.UndoRedoStepPostRequestSchema{
		PageType:          t.PageType,
		DatasetID:         t.DatasetID,
		DataSourceModelID: t.DataSourceModelID,
		DestinationID:     t.DestinationID,
	}
}

// actBody carries the previewed ids. BackupSettingsID and StoredItemID stay
// nil on both bodies: backup settings are not a qctl resource, and
// stored_item_id is accepted by both schemas, assigned onto the controller,
// and read by no filter in either the sync or the async implementation - so a
// "file undo" would be indistinguishable from `qctl undo table`.
func (t Target) actBody(ids []int) api.UndoRedoPostRequestSchema {
	return api.UndoRedoPostRequestSchema{
		PageType:          t.PageType,
		DatasetID:         t.DatasetID,
		DataSourceModelID: t.DataSourceModelID,
		DestinationID:     t.DestinationID,
		HistoryItemsIds:   &ids,
	}
}

// api builds a configured client for one call. Scoped to the two methods
// below; GetHistory inlines the same construction.
func (c *Client) api(accessToken string) (*client.Client, error) {
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

// Step asks the backend what the next undo or redo on this object would do.
//
// It is not an opt-in preview. Its history_items_ids are the optimistic
// concurrency token the act call must echo, so this is the only way the
// command can work safely and it always runs.
//
// A 404 here means "nothing left to undo/redo", OR a target this organization
// cannot see: the step routes run _check_undo_redo_entity_access
// (history_view.py) first, and its
// verify_dataset_organization_async raises
// `APIError(404, f"Table id {dataset_id} not found in this organization.")`
// (capi/capi/controllers/organization_controller.py), with the cloud-source
// twin raising `APIError(404, f"Cloud source id {data_source_model_id} not
// found in this organization.")`. The cloud-source version is the async twin
// of the dataset check. Either way it exits 3. That is a benign state reported as an
// error, and the mapping is kept on purpose: inventing an exit-0 special case
// would make undo the one command whose exit code does not follow the contract.
func (c *Client) Step(accessToken string, dir Direction, target Target) (*StepResult, error) {
	apiClient, err := c.api(accessToken)
	if err != nil {
		return nil, err
	}
	contextMsg := string(dir) + " preview failed"
	body := target.stepBody()

	var (
		status  int
		raw     []byte
		parsed  *StepResult
		callErr error
	)
	if dir == Undo {
		resp, e := apiClient.API.GetUndoHistoryItemsStepWithResponse(
			context.Background(), c.organizationID, &api.GetUndoHistoryItemsStepParams{}, body)
		callErr = e
		if resp != nil {
			status, raw, parsed = resp.StatusCode(), resp.Body, resp.JSON200
		}
	} else {
		resp, e := apiClient.API.GetRedoHistoryItemsStepWithResponse(
			context.Background(), c.organizationID, &api.GetRedoHistoryItemsStepParams{}, body)
		callErr = e
		if resp != nil {
			status, raw, parsed = resp.StatusCode(), resp.Body, resp.JSON200
		}
	}

	if callErr != nil {
		return nil, wrapAPIError(apiClient, callErr, contextMsg)
	}
	if status != http.StatusOK {
		return nil, handleError(status, raw, contextMsg)
	}
	if parsed == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return parsed, nil
}

// Act performs the undo or redo, echoing the ids Step returned.
//
// ids is never allowed to be empty. The undo and redo views refuse a
// non-empty list that differs from the batch they select
// (_checked_history_items_ids, history_view.py) and act on that batch when
// the list is empty, so an empty list reverses whatever is current when the
// act call arrives -- the race the step-then-act handshake exists to close.
//
// Exhaustion -- "nothing left to undo" -- is a 404 on BOTH of the server's
// derivations, so it exits 3 either way. An earlier version of this comment
// claimed it could arrive as a 400 on the second one, and that was wrong.
//
// The two derivations are the view's (history_view.py, on the
// AsyncHistoryController) and the controller's own re-derivation inside undo()
// (history_controller.py). Both call get_current_history_items() with its
// default `raise_instead_of_return_none: bool = True`, and an empty seed select
// raises rather than returns:
//
//	raise APIError(404, "Nothing left to undo.", title="Info", severity=ErrorSeverity.info)
//
// in the async and sync copies. Note which copy runs where -- the two are
// independent classes, not a subclass pair: the view awaits
// AsyncHistoryController.undo, which builds a FRESH sync HistoryController and
// calls its undo, so the re-derivation in the controller is the SYNC copy.
//
// So `if not history_items:` (history_view.py) and
// `if not current_history_items:` are reached only on the other exit,
// `return None` -- the data-overwrite row vanishing between the seed select
// and the session.get. That, not exhaustion, is what the view's 404 and the
// controller's
// `APIError(400, "There is nothing to undo.")` actually cover.
//
// The genuine 400 in this neighbourhood is the concurrency refusal,
// which is the mechanism the ids handshake above exists for.
func (c *Client) Act(accessToken string, dir Direction, target Target, ids []int) (*ActResult, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("internal error: %s attempted with no history item ids; "+
			"the step call must run first and its ids must be passed through unchanged", dir)
	}
	apiClient, err := c.api(accessToken)
	if err != nil {
		return nil, err
	}
	contextMsg := string(dir) + " failed"
	body := target.actBody(ids)

	var (
		status  int
		raw     []byte
		parsed  *ActResult
		callErr error
	)
	if dir == Undo {
		resp, e := apiClient.API.UndoWithResponse(
			context.Background(), c.organizationID, &api.UndoParams{}, body)
		callErr = e
		if resp != nil {
			status, raw, parsed = resp.StatusCode(), resp.Body, resp.JSON200
		}
	} else {
		resp, e := apiClient.API.RedoWithResponse(
			context.Background(), c.organizationID, &api.RedoParams{}, body)
		callErr = e
		if resp != nil {
			status, raw, parsed = resp.StatusCode(), resp.Body, resp.JSON200
		}
	}

	if callErr != nil {
		return nil, wrapAPIError(apiClient, callErr, contextMsg)
	}
	if status != http.StatusOK {
		return nil, handleError(status, raw, contextMsg)
	}
	if parsed == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return parsed, nil
}
