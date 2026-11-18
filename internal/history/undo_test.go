package history

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// undoEnv wires all four POST routes and records the decoded body of each.
// The bodies are the point: page_type plus exactly one id is what selects the
// history chain, and an id sent on the wrong field silently walks a different
// object's chain rather than failing.
type undoEnv struct {
	mock      *testutil.MockAPIServer
	bodies    map[string]map[string]interface{}
	code      map[string]int
	raw       map[string]string
	resultMsg *string
}

func newUndoEnv(t *testing.T) *undoEnv {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	env := &undoEnv{
		mock:   mock,
		bodies: map[string]map[string]interface{}{},
		code:   map[string]int{},
		raw:    map[string]string{},
	}
	for _, route := range []string{"undo-step", "redo-step", "undo", "redo"} {
		route := route
		mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/"+route, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			env.bodies[route] = body

			if code := env.code[route]; code != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				_, _ = w.Write([]byte(env.raw[route]))
				return
			}
			if route == "undo-step" || route == "redo-step" {
				testutil.RespondJSON(w, http.StatusOK, api.UndoRedoStepResponseSchema{
					HistoryItemsIds: []int{12, 13},
					Msg:             "**max** changed from **10** to **20**.",
				})
				return
			}
			refresh := true
			testutil.RespondJSON(w, http.StatusOK, api.UndoRedoResponseSchema{ShouldRefresh: &refresh, Msg: env.resultMsg})
		})
	}
	return env
}

func (e *undoEnv) client() *Client { return NewClient(e.mock.URL(), testOrgID, 0) }

func TestStepSendsPageTypeAndExactlyOneID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		target   Target
		pageType string
		idField  string
		idValue  float64
	}{
		{"table", TableTarget(7, "table orders"), "DataView", "dataset_id", 7},
		{"cloud-source", CloudSourceTarget(4), "DataSourceModelEdit", "data_source_model_id", 4},
		{"destination", DestinationTarget(9), "DestinationEdit", "destination_id", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newUndoEnv(t)
			if _, err := env.client().Step("tok", Undo, tc.target); err != nil {
				t.Fatalf("Step: %v", err)
			}
			body := env.bodies["undo-step"]
			if body["page_type"] != tc.pageType {
				t.Errorf("page_type = %v, want %q", body["page_type"], tc.pageType)
			}
			if body[tc.idField] != tc.idValue {
				t.Errorf("%s = %v, want %v", tc.idField, body[tc.idField], tc.idValue)
			}
			// The other two id fields must be null: a populated one walks a
			// different object's chain, and the backend would not complain.
			for _, other := range []string{"dataset_id", "data_source_model_id", "destination_id",
				"backup_settings_id", "stored_item_id"} {
				if other == tc.idField {
					continue
				}
				if v, ok := body[other]; ok && v != nil {
					t.Errorf("%s = %v, want null", other, v)
				}
			}
		})
	}
}

func TestStepAndActUseTheDirectionsOwnRoute(t *testing.T) {
	env := newUndoEnv(t)
	c := env.client()

	if _, err := c.Step("tok", Redo, TableTarget(7, "table orders")); err != nil {
		t.Fatalf("Step redo: %v", err)
	}
	if _, err := c.Act("tok", Redo, TableTarget(7, "table orders"), []int{12}); err != nil {
		t.Fatalf("Act redo: %v", err)
	}
	got := env.mock.Requests()
	want := []string{
		"POST /api/orgs/" + testOrgID + "/redo-step",
		"POST /api/orgs/" + testOrgID + "/redo",
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// actBody is a separate function from stepBody, so the target fields are
// asserted again here over the SAME three targets
// TestStepSendsPageTypeAndExactlyOneID uses. This is the body that MUTATES
// data: a wrong id field on this one reverses a change on some other object,
// and the backend would not complain.
//
// It is table-driven because the single-target version was not enough. Setting
// `DataSourceModelID: nil` in Target.actBody (undo.go) -- dropping the
// cloud-source id from the mutating body -- left both ./internal/history and
// ./internal/commands/undoredo green: this test used TableTarget alone, and
// TestEachSubcommandSendsItsOwnPageTypeAndIdField reads env.bodies["*-step"],
// never the act body.
func TestActSendsTheIdsItWasGiven(t *testing.T) {
	for _, tc := range []struct {
		name     string
		target   Target
		pageType string
		idField  string
		idValue  float64
	}{
		{"table", TableTarget(7, "table orders"), "DataView", "dataset_id", 7},
		{"cloud-source", CloudSourceTarget(4), "DataSourceModelEdit", "data_source_model_id", 4},
		{"destination", DestinationTarget(9), "DestinationEdit", "destination_id", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newUndoEnv(t)
			if _, err := env.client().Act("tok", Undo, tc.target, []int{12, 13}); err != nil {
				t.Fatalf("Act: %v", err)
			}
			body := env.bodies["undo"]
			ids, _ := body["history_items_ids"].([]interface{})
			if len(ids) != 2 || ids[0] != float64(12) || ids[1] != float64(13) {
				t.Errorf("history_items_ids = %v, want [12 13]", ids)
			}
			if body["page_type"] != tc.pageType {
				t.Errorf("page_type = %v, want %q", body["page_type"], tc.pageType)
			}
			if body[tc.idField] != tc.idValue {
				t.Errorf("%s = %v, want %v", tc.idField, body[tc.idField], tc.idValue)
			}
			// The other id fields must be null, same loop as the step body's:
			// page_type selects which id the backend reads, so a MISSING one is
			// a 422 from _raise_err_if_empty and a spurious extra one is inert
			// -- but neither should ever be sent.
			for _, other := range []string{"dataset_id", "data_source_model_id", "destination_id",
				"backup_settings_id", "stored_item_id"} {
				if other == tc.idField {
					continue
				}
				if v, ok := body[other]; ok && v != nil {
					t.Errorf("%s = %v, want null", other, v)
				}
			}
		})
	}
}

// An empty list is not "no opinion": the backend guards its concurrency check
// with `if current_history_items_ids`, so an empty list disables it and undoes
// whatever is current NOW. Refuse locally rather than send it.
func TestActRefusesAnEmptyIdList(t *testing.T) {
	// Both empty forms, because the guard is len(ids) == 0: a regression to
	// `ids == nil` would still refuse the nil case and silently send the
	// empty-slice one.
	for _, tc := range []struct {
		name string
		ids  []int
	}{
		{"nil", nil},
		{"empty slice", []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newUndoEnv(t)
			_, err := env.client().Act("tok", Undo, TableTarget(7, "table orders"), tc.ids)
			if err == nil {
				t.Fatal("Act with no ids succeeded; it must refuse before the request")
			}
			if len(env.mock.Requests()) != 0 {
				t.Errorf("Act sent %v; it must not reach the wire", env.mock.Requests())
			}
			// Exit 1, not 2, on purpose: no flag or argument can reach this
			// branch, only a caller that skipped Step. Exit 2 would tell the
			// user their command line was wrong when the fault is qctl's own.
			if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitGenericError, err)
			}
		})
	}
}

func TestStepErrorsCarryTheContractExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		want int
	}{
		{"nothing left to undo", http.StatusNotFound,
			`{"detail":{"msg":"There is nothing left to undo.","title":"Info"}}`, qerrors.ExitNotFound},
		{"not permitted", http.StatusForbidden,
			`{"detail":{"msg":"You do not have permission to undo/redo this change."}}`, qerrors.ExitUnauthorized},
		// A synthetic 400 body, and deliberately NOT the controller's
		// APIError(400, 'Page type "..." is not supported.'), which this case
		// used to name and which no request qctl builds can reach: page_type is
		// a required enum-typed body field, so FastAPI answers 422 before that
		// branch runs, and all five enum members have a branch anyway. See
		// Target's doc comment in undo.go. No step-path 400 has been identified
		// in atlas, so the message here claims no particular server condition;
		// what is pinned is the generic 400 -> exit 2 mapping.
		{"a 400 with an unrecognised message", http.StatusBadRequest,
			`{"detail":{"msg":"Bad request.","title":"Invalid request"}}`, qerrors.ExitBadUsage},
		// 422 is the one status that reaches the wrapAPIError branch, and it is
		// the only reason errors.go exists. ParseGetUndoHistoryItemsStepResponse
		// in internal/api/client.gen.go -- grep the symbol, not a line number,
		// which the next `make generate-client` moves -- unmarshals a body on
		// its `rsp.StatusCode == 200` and `== 422` cases alone; every
		// other status returns (resp, nil) and takes the handleError branch
		// above. On 422 it unmarshals into HTTPValidationError, whose
		// Detail *[]ValidationError cannot hold the backend's object-shaped
		// detail, so it returns (nil, err) and resp.Body is unreachable --
		// LastCapturedError() is what rebuilds the exit code from there.
		{"validation error rebuilt from the captured body", http.StatusUnprocessableEntity,
			`{"detail":{"msg":"Field required","error_code":"missing"}}`, qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newUndoEnv(t)
			env.code["undo-step"] = tc.code
			env.raw["undo-step"] = tc.body

			_, err := env.client().Step("tok", Undo, TableTarget(7, "table orders"))
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Errorf("exit code = %d, want %d (err: %v)", got, tc.want, err)
			}
		})
	}
}
