package alerts

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

const msgsListPath = "/api/orgs/" + testOrgID + "/datasets/42/alerts-msgs-list/"

func TestMsgsList_ReturnsMessagesByID(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotIDs []int
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, r *http.Request) {
		var body api.AlertsMsgsListRequestSchema
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotIDs = body.Ids
		testutil.RespondJSON(w, http.StatusOK, []api.AlertsMsgsListResponseSchema{
			{ID: 8817, Msg: "Not a valid email"},
			{ID: 8818, Msg: "Unexpected value"},
		})
	})

	msgs, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, []int{8817, 8818})
	if err != nil {
		t.Fatalf("MsgsList() error = %v", err)
	}
	if len(gotIDs) != 2 || gotIDs[0] != 8817 || gotIDs[1] != 8818 {
		t.Errorf("request ids = %v, want [8817 8818]", gotIDs)
	}
	if msgs[8817].Msg != "Not a valid email" || msgs[8818].Msg != "Unexpected value" {
		t.Errorf("messages = %v", msgs)
	}
}

// An id the handler has no alert for under this table — or, for an external
// user, one outside their associated data sources — is simply absent from the
// response. That must not be an error, and must not become an empty message
// either: the caller has to be able to tell.
func TestMsgsList_UnansweredIDIsAbsentNotEmpty(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, []api.AlertsMsgsListResponseSchema{{ID: 8817, Msg: "Not a valid email"}})
	})

	msgs, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, []int{8817, 9999})
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
		testutil.RespondJSON(w, http.StatusOK, []api.AlertsMsgsListResponseSchema{})
	})

	msgs, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, nil)
	if err != nil {
		t.Fatalf("MsgsList() error = %v", err)
	}
	if len(msgs) != 0 || calls != 0 {
		t.Errorf("msgs = %v, calls = %d, want empty and 0", msgs, calls)
	}
}

// The route needs alert:list, which get_data does not, so a 403 is the likely
// failure and it has to reach the caller as one.
func TestMsgsList_PropagatesAnError(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
			"detail": map[string]interface{}{"msg": "alert:list required"},
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
		var body api.AlertsMsgsListRequestSchema
		_ = json.NewDecoder(r.Body).Decode(&body)
		sizes = append(sizes, len(body.Ids))
		out := make([]api.AlertsMsgsListResponseSchema, 0, len(body.Ids))
		for _, id := range body.Ids {
			out = append(out, api.AlertsMsgsListResponseSchema{ID: id, Msg: "msg"})
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

// Each item carries the flag records against that alert, retracted and
// detached ones included; MsgsList keeps them, decoded, on the item. An item
// without the key decodes with no list.
func TestMsgsList_KeepsUserSubmittedIssues(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", msgsListPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id": 8817, "msg": "Not a valid email", "user_submitted_issues": [
				{"id": "6f1c2a7e-0000-4000-8000-000000000001", "row_id": 8943, "field_names": ["email"],
				 "submitted_by": {"id": "6f1c2a7e-0000-4000-8000-0000000000aa", "email": "ann@x.com", "first_name": "Ann", "last_name": "Lee"},
				 "thread_id": null, "created_at": "2026-10-01T10:00:00Z", "retracted_at": null, "detached_at": null},
				{"id": "6f1c2a7e-0000-4000-8000-000000000002", "row_id": 8944, "field_names": [],
				 "submitted_by": {"id": "6f1c2a7e-0000-4000-8000-0000000000bb", "email": "bo@x.com", "first_name": "", "last_name": ""},
				 "thread_id": "6f1c2a7e-0000-4000-8000-0000000000cc", "created_at": "2026-10-02T10:00:00Z",
				 "retracted_at": "2026-10-03T10:00:00Z", "detached_at": null}
			]},
			{"id": 8818, "msg": "Unexpected value"}
		]`))
	})

	msgs, err := NewClient(mock.Server.URL, testOrgID, 0).MsgsList("test-token", 42, []int{8817, 8818})
	if err != nil {
		t.Fatalf("MsgsList() error = %v", err)
	}
	flags := msgs[8817].UserSubmittedIssues
	if flags == nil || len(*flags) != 2 {
		t.Fatalf("alert 8817 flags = %+v, want 2 records", flags)
	}
	first, second := (*flags)[0], (*flags)[1]
	if first.RowID != 8943 || first.SubmittedBy.FirstName != "Ann" || first.RetractedAt != nil || first.DetachedAt != nil {
		t.Errorf("first flag = %+v", first)
	}
	if second.RowID != 8944 || second.SubmittedBy.Email != "bo@x.com" || second.RetractedAt == nil {
		t.Errorf("second flag = %+v, want a retracted record", second)
	}
	if msgs[8818].Msg != "Unexpected value" || msgs[8818].UserSubmittedIssues != nil {
		t.Errorf("alert 8818 = %+v, want its message and no flags", msgs[8818])
	}
}
