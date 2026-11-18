package history

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// historyEnv wires GET /history and captures the query string. The captured
// query is the point of this file: a filter that silently fails to reach the
// wire widens an audit listing to the whole organization without saying so.
type historyEnv struct {
	mock  *testutil.MockAPIServer
	calls int
	query url.Values
	code  int
	body  string
}

func newHistoryEnv(t *testing.T) *historyEnv {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	env := &historyEnv{mock: mock, code: http.StatusOK}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/history", func(w http.ResponseWriter, r *http.Request) {
		env.calls++
		env.query = r.URL.Query()
		if env.code != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(env.code)
			_, _ = w.Write([]byte(env.body))
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})
	return env
}

func (e *historyEnv) get(t *testing.T, params GetHistoryParams) (*Page, error) {
	t.Helper()
	return NewClient(e.mock.URL(), testOrgID, 0).GetHistory("test-token", params)
}

func TestGetHistorySendsEveryFilter(t *testing.T) {
	env := newHistoryEnv(t)

	datasetID, modelID := 7, 42
	model := api.ModelsWithHistoryDatasetRule
	field, value, search := "thresholds", "5", "orders"
	undo := api.HistoryUndoStatusUndone
	who := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	after := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)

	if _, err := env.get(t, GetHistoryParams{
		DatasetID:     &datasetID,
		Model:         &model,
		ModelID:       &modelID,
		FieldName:     &field,
		Value:         &value,
		SearchQuery:   &search,
		WhodunitID:    &who,
		CreatedAfter:  &after,
		CreatedBefore: &before,
		UndoStatus:    &undo,
		Operation:     []Operation{api.SimpleHistoryActionAdded, api.SimpleHistoryActionRemoved},
		OrderBy:       "created_at",
		Page:          2,
		Limit:         50,
	}); err != nil {
		t.Fatalf("GetHistory: %v", err)
	}

	want := map[string]string{
		"dataset_id": "7", "model": "DatasetRule", "model_id": "42",
		"field_name": "thresholds", "value": "5", "search_query": "orders",
		"whodunit_id": "11111111-2222-3333-4444-555555555555",
		"order_by":    "created_at", "page": "2", "limit": "50",
		"undo_status": "undone",
	}
	for key, expected := range want {
		if got := env.query.Get(key); got != expected {
			t.Errorf("query %s = %q, want %q", key, got, expected)
		}
	}
	if got := env.query["operation"]; len(got) != 2 || got[0] != "added" || got[1] != "removed" {
		t.Errorf("operation = %v, want [added removed]", got)
	}
	// The wire wants RFC 3339; anything else is a filter the server ignores.
	if got := env.query.Get("created_after"); got != "2026-08-30T00:00:00Z" {
		t.Errorf("created_after = %q, want 2026-08-30T00:00:00Z", got)
	}
	if got := env.query.Get("created_before"); got != "2026-08-31T00:00:00Z" {
		t.Errorf("created_before = %q, want 2026-08-31T00:00:00Z", got)
	}
}

// The endpoint declares reverse=True (atlas history_view.py get_history_items),
// so the listing is newest-first already and Reverse means "send reverse=false".
// Its ABSENCE must send nothing at all, or every default listing would flip.
func TestGetHistoryReverseInvertsTheServerDefault(t *testing.T) {
	env := newHistoryEnv(t)
	if _, err := env.get(t, GetHistoryParams{Reverse: true}); err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if got := env.query.Get("reverse"); got != "false" {
		t.Errorf("--reverse sent reverse=%q, want %q", got, "false")
	}

	env2 := newHistoryEnv(t)
	if _, err := env2.get(t, GetHistoryParams{}); err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if _, ok := env2.query["reverse"]; ok {
		t.Errorf("no --reverse sent reverse=%q, want the parameter omitted", env2.query.Get("reverse"))
	}
}

func TestGetHistoryOmitsUnsetFilters(t *testing.T) {
	env := newHistoryEnv(t)
	if _, err := env.get(t, GetHistoryParams{Page: 1, Limit: 100}); err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	for _, key := range []string{"dataset_id", "model", "model_id", "field_name", "value",
		"search_query", "whodunit_id", "created_after", "created_before", "operation", "undo_status", "order_by"} {
		if _, ok := env.query[key]; ok {
			t.Errorf("%s was sent as %q; an unset filter must be omitted", key, env.query.Get(key))
		}
	}
}

// A 403 is the shape this endpoint answers with for --model Destination as a
// non-ops role, and for any model as an external user. It must exit 4 with the
// server's own message, not collapse to a generic 1.
func TestGetHistoryForbiddenKeepsItsExitCode(t *testing.T) {
	env := newHistoryEnv(t)
	env.code = http.StatusForbidden
	env.body = `{"detail":{"msg":"User is not authorized to access this history"}}`

	_, err := env.get(t, GetHistoryParams{})
	if err == nil {
		t.Fatal("expected an error for 403")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d (ExitUnauthorized)", got, qerrors.ExitUnauthorized)
	}
}
