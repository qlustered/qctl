package warnings

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

const msgsListPath = "/api/orgs/" + testOrgID + "/datasets/42/warnings-msgs-list/"

func TestMsgsList_ReturnsMessagesByID(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotIDs []int
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, r *http.Request) {
		var body api.WarningsMsgsListRequestSchema
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotIDs = body.Ids
		testutil.RespondJSON(w, http.StatusOK, []api.WarningsMsgsListResponseSchema{
			{ID: 55, Msg: "Anomalous value"},
			{ID: 56, Msg: "Looks like a duplicate"},
		})
	})

	msgs, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, []int{55, 56})
	if err != nil {
		t.Fatalf("MsgsList() error = %v", err)
	}
	if len(gotIDs) != 2 || gotIDs[0] != 55 || gotIDs[1] != 56 {
		t.Errorf("request ids = %v, want [55 56]", gotIDs)
	}
	if msgs[55].Msg != "Anomalous value" || msgs[56].Msg != "Looks like a duplicate" {
		t.Errorf("messages = %v", msgs)
	}
}

// An id the handler has no warning for under this table — or, for an external
// user, one outside their associated data sources — is simply absent from the
// response. That must not be an error, and must not become an empty message
// either: the caller has to be able to tell.
func TestMsgsList_UnansweredIDIsAbsentNotEmpty(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, []api.WarningsMsgsListResponseSchema{{ID: 55, Msg: "Anomalous value"}})
	})

	msgs, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, []int{55, 9999})
	if err != nil {
		t.Fatalf("MsgsList() error = %v", err)
	}
	if _, ok := msgs[9999]; ok {
		t.Errorf("an unanswered id must not be in the map: %v", msgs)
	}
}

// Nothing to ask about costs nothing: the request schema would be an empty
// list, and the round trip would buy an empty map either way.
func TestMsgsList_EmptyIDsMakesNoRequest(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	calls := 0
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, []api.WarningsMsgsListResponseSchema{})
	})

	msgs, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, nil)
	if err != nil {
		t.Fatalf("MsgsList() error = %v", err)
	}
	if len(msgs) != 0 || calls != 0 {
		t.Errorf("msgs = %v, calls = %d, want empty and 0", msgs, calls)
	}
}

// The route needs warning:list, which get_data does not, so a 403 is the likely
// failure and it has to reach the caller as one.
func TestMsgsList_PropagatesAnError(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
			"detail": map[string]interface{}{"msg": "warning:list required"},
		})
	})

	if _, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, []int{1}); err == nil {
		t.Fatal("expected an error for a 403")
	}
}

// More ids than one lookup can answer are split, and every chunk's results land
// in the one map.
func TestMsgsList_ChunksLongIDSets(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var sizes []int
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, r *http.Request) {
		var body api.WarningsMsgsListRequestSchema
		_ = json.NewDecoder(r.Body).Decode(&body)
		sizes = append(sizes, len(body.Ids))
		out := make([]api.WarningsMsgsListResponseSchema, 0, len(body.Ids))
		for _, id := range body.Ids {
			out = append(out, api.WarningsMsgsListResponseSchema{ID: id, Msg: "msg"})
		}
		testutil.RespondJSON(w, http.StatusOK, out)
	})

	ids := make([]int, msgsListMaxIDs+5)
	for i := range ids {
		ids[i] = i + 1
	}
	msgs, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, ids)
	if err != nil {
		t.Fatalf("MsgsList() error = %v", err)
	}
	if len(sizes) != 2 || sizes[0] != msgsListMaxIDs || sizes[1] != 5 {
		t.Errorf("chunk sizes = %v, want [%d 5]", sizes, msgsListMaxIDs)
	}
	if len(msgs) != len(ids) {
		t.Errorf("messages = %d, want %d", len(msgs), len(ids))
	}
}

// Each item carries the flag records against that warning, detached ones
// included; MsgsList keeps them, decoded, on the item.
func TestMsgsList_KeepsUserSubmittedIssues(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id": 55, "msg": "Anomalous value", "user_submitted_issues": [
				{"id": "6f1c2a7e-0000-4000-8000-000000000001", "row_id": 8943, "field_names": ["email"],
				 "submitted_by": {"id": "6f1c2a7e-0000-4000-8000-0000000000aa", "email": "ann@x.com", "first_name": "Ann", "last_name": "Lee"},
				 "thread_id": null, "created_at": "2026-10-01T10:00:00Z", "retracted_at": null,
				 "detached_at": "2026-10-04T10:00:00Z"}
			]},
			{"id": 56, "msg": "Looks like a duplicate", "user_submitted_issues": []}
		]`))
	})

	msgs, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, []int{55, 56})
	if err != nil {
		t.Fatalf("MsgsList() error = %v", err)
	}
	flags := msgs[55].UserSubmittedIssues
	if flags == nil || len(*flags) != 1 {
		t.Fatalf("warning 55 flags = %+v, want 1 record", flags)
	}
	if f := (*flags)[0]; f.RowID != 8943 || f.SubmittedBy.LastName != "Lee" || f.DetachedAt == nil {
		t.Errorf("flag = %+v, want a detached record on row 8943", f)
	}
	if got := msgs[56].UserSubmittedIssues; got == nil || len(*got) != 0 {
		t.Errorf("warning 56 flags = %+v, want an empty list", got)
	}
}
