package queue

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "00000000-0000-0000-0000-000000000000"

func intPtr(i int) *int    { return &i }
func boolPtr(b bool) *bool { return &b }
func strPtr(s string) *string {
	return &s
}

func sampleMessage() Message {
	key := "files/77/orders.csv"
	dequeued := time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC)
	return Message{
		ID:                42,
		QName:             "CUEUE_INGESTION",
		Key:               &key,
		Data:              map[string]interface{}{"ingestion_job_id": 101},
		Acknowledged:      false,
		DatasetID:         intPtr(5),
		DataSourceModelID: intPtr(22),
		PutCount:          1,
		ReceiveCount:      4,
		RescheduleCount:   0,
		MaxReceiveCount:   intPtr(4),
		IsDead:            boolPtr(true),
		EnqueuedAt:        time.Date(2025, 1, 18, 9, 0, 0, 0, time.UTC),
		DequeuedAt:        &dequeued,
	}
}

func TestClient_GetMessages(t *testing.T) {
	tests := []struct {
		name       string
		params     GetMessagesParams
		mockStatus int
		wantErr    bool
		wantCount  int
		wantQuery  map[string]string
		wantAbsent []string
	}{
		{
			name:       "successful fetch",
			params:     GetMessagesParams{Limit: 20, Page: 1, OrderBy: "id"},
			mockStatus: http.StatusOK,
			wantCount:  1,
			wantQuery:  map[string]string{"limit": "20", "page": "1", "order_by": "id"},
			// The default listing must not pin an ordering direction: leaving
			// reverse off is what lets the server's own default stand.
			wantAbsent: []string{"reverse", "is_dead", "acknowledged", "q_name"},
		},
		{
			// The house inversion, asserted rather than assumed: this endpoint
			// defaults reverse=true, so --reverse sends false.
			name:       "reverse sends reverse=false",
			params:     GetMessagesParams{Reverse: true},
			mockStatus: http.StatusOK,
			wantCount:  1,
			wantQuery:  map[string]string{"reverse": "false"},
		},
		{
			name:       "dead filter",
			params:     GetMessagesParams{IsDead: boolPtr(true)},
			mockStatus: http.StatusOK,
			wantCount:  1,
			wantQuery:  map[string]string{"is_dead": "true"},
		},
		{
			name:       "live filter is sent, not dropped",
			params:     GetMessagesParams{IsDead: boolPtr(false)},
			mockStatus: http.StatusOK,
			wantCount:  1,
			wantQuery:  map[string]string{"is_dead": "false"},
		},
		{
			name: "every filter maps to its query parameter",
			params: GetMessagesParams{
				Acknowledged:      boolPtr(true),
				QName:             strPtr("CUEUE_INGESTION"),
				DatasetID:         intPtr(5),
				DataSourceModelID: intPtr(22),
				SearchQuery:       "orders",
			},
			mockStatus: http.StatusOK,
			wantCount:  1,
			wantQuery: map[string]string{
				"acknowledged":         "true",
				"q_name":               "CUEUE_INGESTION",
				"dataset_id":           "5",
				"data_source_model_id": "22",
				"search_query":         "orders",
			},
		},
		{
			name:       "unauthorized",
			params:     GetMessagesParams{},
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name:       "forbidden for a non-ops caller",
			params:     GetMessagesParams{},
			mockStatus: http.StatusForbidden,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			var gotQuery url.Values
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/queue-messages", func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query()
				if tt.mockStatus != http.StatusOK {
					testutil.RespondError(w, tt.mockStatus, "error")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, MessagesPage{
					Results:   []Message{sampleMessage()},
					TotalRows: intPtr(1),
					Page:      intPtr(1),
				})
			})

			client, err := NewClient(mock.Server.URL, testOrgID, 0)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			resp, err := client.GetMessages("test-token", tt.params)

			if (err != nil) != tt.wantErr {
				t.Fatalf("GetMessages() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(resp.Results) != tt.wantCount {
				t.Errorf("GetMessages() count = %d, want %d", len(resp.Results), tt.wantCount)
			}
			for k, want := range tt.wantQuery {
				if got := gotQuery.Get(k); got != want {
					t.Errorf("query %s = %q, want %q", k, got, want)
				}
			}
			for _, k := range tt.wantAbsent {
				if _, ok := gotQuery[k]; ok {
					t.Errorf("query %s was sent (%q); it must be left to the server default", k, gotQuery.Get(k))
				}
			}
		})
	}
}

func TestClient_GetMessagesExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantCode int
	}{
		{"unauthorized is 4", http.StatusUnauthorized, qerrors.ExitUnauthorized},
		{"forbidden is 4", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"not found is 3", http.StatusNotFound, qerrors.ExitNotFound},
		{"server error is 1", http.StatusInternalServerError, qerrors.ExitGenericError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/queue-messages", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondError(w, tt.status, "nope")
			})

			client, _ := NewClient(mock.Server.URL, testOrgID, 0)
			_, err := client.GetMessages("test-token", GetMessagesParams{})
			if err == nil {
				t.Fatal("GetMessages() error = nil, want an error")
			}
			if got := qerrors.GetExitCode(err); got != tt.wantCode {
				t.Errorf("GetExitCode() = %d, want %d", got, tt.wantCode)
			}
		})
	}
}

func TestClient_GetMessage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/queue-messages/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleMessage())
	})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	msg, err := client.GetMessage("test-token", 42)
	if err != nil {
		t.Fatalf("GetMessage() error = %v", err)
	}
	if msg.ID != 42 {
		t.Errorf("GetMessage() id = %d, want 42", msg.ID)
	}
	if msg.QName != "CUEUE_INGESTION" {
		t.Errorf("GetMessage() q_name = %q, want CUEUE_INGESTION", msg.QName)
	}
}

func TestClient_GetMessageNotFound(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/queue-messages/99", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Queue msg 99 can not be found.")
	})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.GetMessage("test-token", 99)
	if err == nil {
		t.Fatal("GetMessage() error = nil, want an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("GetExitCode() = %d, want %d", got, qerrors.ExitNotFound)
	}
}

func TestClient_ListDeadIDs(t *testing.T) {
	tests := []struct {
		name        string
		params      ListDeadParams
		wantIdleFor string
		wantSent    bool
	}{
		{
			name:        "an interval is sent as idle_for",
			params:      ListDeadParams{IdleFor: "2 hours", Limit: 50},
			wantIdleFor: "2 hours",
			wantSent:    true,
		},
		{
			// An empty interval is omitted, so the server applies the default
			// window resurrect-batch falls back to as well. "" would reach
			// validate_postgres_interval and be refused.
			name:   "an empty interval is not sent",
			params: ListDeadParams{Limit: 50},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			var gotQuery url.Values
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/queue-messages/dead", func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query()
				testutil.RespondJSON(w, http.StatusOK, DeadIDs{DeadIds: []int{7, 8, 9}, TotalCount: 3})
			})

			client, _ := NewClient(mock.Server.URL, testOrgID, 0)
			got, err := client.ListDeadIDs("test-token", tt.params)
			if err != nil {
				t.Fatalf("ListDeadIDs() error = %v", err)
			}
			if got.TotalCount != 3 || len(got.DeadIds) != 3 {
				t.Errorf("ListDeadIDs() = %+v, want 3 ids", got)
			}
			if gotQuery.Get("limit") != "50" {
				t.Errorf("limit = %q, want 50", gotQuery.Get("limit"))
			}
			if _, sent := gotQuery["idle_for"]; sent != tt.wantSent {
				t.Errorf("idle_for sent = %v, want %v", sent, tt.wantSent)
			}
			if gotQuery.Get("idle_for") != tt.wantIdleFor {
				t.Errorf("idle_for = %q, want %q", gotQuery.Get("idle_for"), tt.wantIdleFor)
			}
			// resurrect-batch takes no queue, so a queue-scoped list would not
			// preview it.
			if _, ok := gotQuery["q_name"]; ok {
				t.Error("q_name was sent")
			}
		})
	}
}

func TestClient_Reschedule(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/queue-messages/reschedule", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		testutil.RespondJSON(w, http.StatusOK, RescheduleReply{Result: "ok"})
	})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	resp, err := client.Reschedule("test-token", []int{1, 2, 3})
	if err != nil {
		t.Fatalf("Reschedule() error = %v", err)
	}
	if resp.Result != "ok" {
		t.Errorf("Reschedule() result = %q, want ok", resp.Result)
	}
	ids, _ := gotBody["ids"].([]interface{})
	if len(ids) != 3 {
		t.Errorf("body ids = %v, want 3 entries", gotBody["ids"])
	}
}

func TestClient_RescheduleEmptyIDsIsA422(t *testing.T) {
	// The backend refuses an empty ids list with 422. Commands validate the
	// arguments locally, but the client must still surface the refusal as a
	// bad-usage exit rather than a generic failure.
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/queue-messages/reschedule", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusUnprocessableEntity, "No message ids are passed")
	})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.Reschedule("test-token", nil)
	if err == nil {
		t.Fatal("Reschedule() error = nil, want an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode() = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestClient_Resurrect(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/queue-messages/42/resurrect", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		testutil.RespondJSON(w, http.StatusOK, ResurrectReply{
			Message:       "Successfully resurrected message 42",
			ResurrectedID: intPtr(42),
		})
	})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	resp, err := client.Resurrect("test-token", 42, 30)
	if err != nil {
		t.Fatalf("Resurrect() error = %v", err)
	}
	if resp.ResurrectedID == nil || *resp.ResurrectedID != 42 {
		t.Errorf("Resurrect() resurrected_id = %v, want 42", resp.ResurrectedID)
	}
	if got, ok := gotBody["backoff_seconds"].(float64); !ok || int(got) != 30 {
		t.Errorf("body backoff_seconds = %v, want 30", gotBody["backoff_seconds"])
	}
}

func TestClient_ResurrectSendsZeroBackoffExplicitly(t *testing.T) {
	// backoff_seconds is omitempty on the generated struct, so a pointer to
	// zero is what keeps "no delay" an explicit request rather than an absent
	// key the server re-defaults.
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/queue-messages/42/resurrect", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		testutil.RespondJSON(w, http.StatusOK, ResurrectReply{Message: "ok", ResurrectedID: intPtr(42)})
	})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.Resurrect("test-token", 42, 0); err != nil {
		t.Fatalf("Resurrect() error = %v", err)
	}
	if _, ok := gotBody["backoff_seconds"]; !ok {
		t.Errorf("body = %v, want an explicit backoff_seconds", gotBody)
	}
}

func TestClient_ResurrectNotDeadIsAServerError(t *testing.T) {
	// Documented so the 500 is not mistaken for a transport fault: the route
	// answers 500 when the message is not soft-dead. It is why the command
	// checks state before calling.
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/queue-messages/42/resurrect", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError,
			"Message 42 was not resurrected (may not be soft-dead or already processed or not exist)")
	})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.Resurrect("test-token", 42, 0)
	if err == nil {
		t.Fatal("Resurrect() error = nil, want an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("GetExitCode() = %d, want %d", got, qerrors.ExitGenericError)
	}
}

func TestClient_ResurrectBatch(t *testing.T) {
	tests := []struct {
		name       string
		params     ResurrectBatchParams
		wantKeys   map[string]interface{}
		absentKeys []string
	}{
		{
			name:     "every field set",
			params:   ResurrectBatchParams{OlderThan: "1 day", Limit: 100, BackoffSeconds: 60},
			wantKeys: map[string]interface{}{"older_than": "1 day", "limit": float64(100), "backoff_seconds": float64(60)},
		},
		{
			// An omitted --older-than must reach the server as JSON null, not
			// as "": null is the server's `older_than: str | None = None`, and
			// it falls back to CUEUE_INVISIBLE_TIME, while "" would go
			// to validate_postgres_interval and be refused. The generated
			// field has no omitempty, so null is what a nil pointer produces
			// and the key itself is expected to be present.
			name:     "omitted older_than is sent as null, never as an empty interval",
			params:   ResurrectBatchParams{Limit: 10},
			wantKeys: map[string]interface{}{"limit": float64(10), "older_than": nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			var gotBody map[string]interface{}
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/queue-messages/resurrect-batch", func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &gotBody)
				testutil.RespondJSON(w, http.StatusOK, ResurrectBatchReply{ResurrectedIds: []int{1, 2}, Count: 2})
			})

			client, _ := NewClient(mock.Server.URL, testOrgID, 0)
			resp, err := client.ResurrectBatch("test-token", tt.params)
			if err != nil {
				t.Fatalf("ResurrectBatch() error = %v", err)
			}
			if resp.Count != 2 {
				t.Errorf("ResurrectBatch() count = %d, want 2", resp.Count)
			}
			for k, want := range tt.wantKeys {
				got, present := gotBody[k]
				if !present || got != want {
					t.Errorf("body %s = %v (present %t), want %v", k, got, present, want)
				}
			}
			for _, k := range tt.absentKeys {
				if _, ok := gotBody[k]; ok {
					t.Errorf("body %s = %v, want it absent", k, gotBody[k])
				}
			}
		})
	}
}

func TestClient_ResurrectBatchInvalidIntervalIsBadUsage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/queue-messages/resurrect-batch", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusBadRequest,
			"Invalid older_than value 'yesterday'. Must be a valid PostgreSQL interval format like '1 day'")
	})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.ResurrectBatch("test-token", ResurrectBatchParams{OlderThan: "yesterday"})
	if err == nil {
		t.Fatal("ResurrectBatch() error = nil, want an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode() = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestClient_Retire(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotBody map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/queue-messages/retire-old", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		testutil.RespondJSON(w, http.StatusOK, RetireReply{RetiredIds: []int{3, 4, 5}, Count: 3})
	})

	client, _ := NewClient(mock.Server.URL, testOrgID, 0)
	resp, err := client.Retire("test-token", RetireParams{OlderThan: "30 days", Limit: 500})
	if err != nil {
		t.Fatalf("Retire() error = %v", err)
	}
	if resp.Count != 3 {
		t.Errorf("Retire() count = %d, want 3", resp.Count)
	}
	if gotBody["older_than"] != "30 days" {
		t.Errorf("body older_than = %v, want 30 days", gotBody["older_than"])
	}
	if gotBody["limit"] != float64(500) {
		t.Errorf("body limit = %v, want 500", gotBody["limit"])
	}
}

func TestNewClientRejectsABadOrganizationID(t *testing.T) {
	if _, err := NewClient("http://example.invalid", "not-a-uuid", 0); err == nil {
		t.Fatal("NewClient() error = nil, want an error")
	}
}
