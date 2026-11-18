//nolint:revive // Tests intentionally match the command package directory.
package queue_message

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

// deadMessage is a message the resurrect route's UPDATE would match:
// unacknowledged, with its receive count at the ceiling.
func deadMessage() api.QueueSchema {
	return api.QueueSchema{
		ID:              42,
		QName:           "CUEUE_INGESTION",
		Acknowledged:    false,
		IsDead:          boolPtr(true),
		ReceiveCount:    4,
		MaxReceiveCount: intPtr(4),
		EnqueuedAt:      time.Date(2025, 1, 18, 9, 0, 0, 0, time.UTC),
	}
}

// registerGetOne serves the pre-flight read every resurrect run makes.
func registerGetOne(mock *testutil.MockAPIServer, msg api.QueueSchema) *int {
	calls := new(int)
	mock.RegisterHandler("GET", queuePath+"/42", func(w http.ResponseWriter, r *http.Request) {
		*calls++
		testutil.RespondJSON(w, http.StatusOK, msg)
	})
	return calls
}

func TestResurrectRevivesADeadMessage(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	registerGetOne(mock, deadMessage())
	var bodies []api.QueueResurrectOneRequestSchema
	mock.RegisterHandler("POST", queuePath+"/42/resurrect", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body api.QueueResurrectOneRequestSchema
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body %q is not a resurrect body: %v", raw, err)
		}
		bodies = append(bodies, body)
		testutil.RespondJSON(w, http.StatusOK, api.QueueResurrectOneResponseSchema{
			Message: "Successfully resurrected message 42", ResurrectedID: intPtr(42),
		})
	})

	out, _, err := runQueueMessage(t, "resurrect", "42", "--backoff", "300")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("made %d resurrect calls, want 1", len(bodies))
	}
	if bodies[0].BackoffSeconds == nil || *bodies[0].BackoffSeconds != 300 {
		t.Errorf("backoff_seconds = %v, want 300", bodies[0].BackoffSeconds)
	}
	if !strings.Contains(out, "Queue message 42 resurrected") {
		t.Errorf("output %q does not confirm the resurrection", out)
	}
	if !strings.Contains(out, "300 second(s)") {
		t.Errorf("output %q does not state the delay it asked for", out)
	}
}

func TestResurrectSendsZeroBackoffByDefault(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	registerGetOne(mock, deadMessage())
	var body api.QueueResurrectOneRequestSchema
	mock.RegisterHandler("POST", queuePath+"/42/resurrect", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		testutil.RespondJSON(w, http.StatusOK, api.QueueResurrectOneResponseSchema{
			Message: "ok", ResurrectedID: intPtr(42),
		})
	})

	out, _, err := runQueueMessage(t, "resurrect", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.BackoffSeconds == nil || *body.BackoffSeconds != 0 {
		t.Errorf("backoff_seconds = %v, want 0", body.BackoffSeconds)
	}
	if !strings.Contains(out, "deliverable now") {
		t.Errorf("output %q does not say the message is deliverable now", out)
	}
}

// The guard exists because the route answers 500 -- not 400 or 404 -- when its
// UPDATE matches nothing. Each arm must refuse before the POST, and the two
// arms must not give the same advice: retry is a real alternative for a live
// message and a no-op for an acknowledged one.
func TestResurrectRefusesWhatTheRouteWould500On(t *testing.T) {
	live := deadMessage()
	live.IsDead = boolPtr(false)
	live.ReceiveCount = 1

	acked := deadMessage()
	acked.Acknowledged = true
	acked.IsDead = boolPtr(false)

	tests := []struct {
		name       string
		msg        api.QueueSchema
		wantPhrase string
		wantsRetry bool
	}{
		{"not dead", live, "is not dead", true},
		{"acknowledged", acked, "is acknowledged", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newQueueMessageTestEnv(t)
			registerGetOne(mock, tt.msg)
			posted := false
			mock.RegisterHandler("POST", queuePath+"/42/resurrect", func(w http.ResponseWriter, r *http.Request) {
				posted = true
				testutil.RespondJSON(w, http.StatusOK, api.QueueResurrectOneResponseSchema{Message: "ok"})
			})

			_, _, err := runQueueMessage(t, "resurrect", "42")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if posted {
				t.Error("the refusal still called the resurrect route")
			}
			if !strings.Contains(err.Error(), tt.wantPhrase) {
				t.Errorf("error %q does not contain %q", err, tt.wantPhrase)
			}
			namesRetry := strings.Contains(err.Error(), "queue-message retry")
			if namesRetry != tt.wantsRetry {
				t.Errorf("error %q names retry = %v, want %v "+
					"(retry carries the same acknowledged=false guard, so it is not an alternative for an acknowledged message)",
					err, namesRetry, tt.wantsRetry)
			}
		})
	}
}

// A negative delay and a malformed id are both decidable locally, so neither
// may cost a request -- not even the pre-flight read.
func TestResurrectUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"negative backoff", []string{"resurrect", "42", "--backoff", "-1"}},
		{"non-numeric id", []string{"resurrect", "abc"}},
		{"no id", []string{"resurrect"}},
		{"two ids", []string{"resurrect", "42", "43"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newQueueMessageTestEnv(t)
			called := registerGetOne(mock, deadMessage())

			_, _, err := runQueueMessage(t, tt.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if *called != 0 {
				t.Error("a client-side usage error still read the message from the server")
			}
		})
	}
}
