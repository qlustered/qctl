//nolint:revive // Tests intentionally match the command package directory.
package queue_message

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestRetrySendsEveryIDInOneCall(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	var bodies []api.QueuesRescheduleRequestSchema
	mock.RegisterHandler("POST", queuePath+"/reschedule", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body api.QueuesRescheduleRequestSchema
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body %q is not a reschedule body: %v", raw, err)
		}
		bodies = append(bodies, body)
		testutil.RespondJSON(w, http.StatusOK, api.QueuesRescheduleResponseSchema{Result: "ok"})
	})

	out, _, err := runQueueMessage(t, "retry", "42", "43", "44")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("made %d reschedule calls, want 1", len(bodies))
	}
	if got, want := bodies[0].Ids, []int{42, 43, 44}; len(got) != len(want) {
		t.Fatalf("sent ids %v, want %v", got, want)
	}
	for i, id := range []int{42, 43, 44} {
		if bodies[0].Ids[i] != id {
			t.Errorf("sent ids %v, want [42 43 44]", bodies[0].Ids)
			break
		}
	}
	if !strings.Contains(out, "42, 43, 44") {
		t.Errorf("output %q does not name the ids it sent", out)
	}
}

// The route discards the UPDATE's rowcount, so any number in this output would
// be invented. The command must say so instead of implying one.
func TestRetryReportsNoCountAndSaysWhy(t *testing.T) {
	mock := newQueueMessageTestEnv(t)
	mock.RegisterHandler("POST", queuePath+"/reschedule", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.QueuesRescheduleResponseSchema{Result: "ok"})
	})

	out, _, err := runQueueMessage(t, "retry", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "does not report how many were applied") {
		t.Errorf("output %q does not explain the missing count", out)
	}
	for _, claim := range []string{"1 message", "1 queue message retried", "retried 1"} {
		if strings.Contains(out, claim) {
			t.Errorf("output %q claims a count the server never returned", out)
		}
	}
}

func TestRetryUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		// ParseIntArgs. Runs before Bootstrap, so this exits 2 with no config
		// and no server in sight.
		{"non-numeric id", []string{"retry", "abc"}},
		// Arity. Exit 2 only because the harness calls TypeArgsErrors, exactly
		// as root does — and it is what keeps an empty ids list off the wire,
		// where the route answers 422.
		{"no ids", []string{"retry"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newQueueMessageTestEnv(t)
			called := false
			mock.RegisterHandler("POST", queuePath+"/reschedule", func(w http.ResponseWriter, r *http.Request) {
				called = true
				testutil.RespondJSON(w, http.StatusOK, api.QueuesRescheduleResponseSchema{Result: "ok"})
			})

			_, _, err := runQueueMessage(t, tt.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if called {
				t.Error("a client-side usage error still reached the server")
			}
		})
	}
}
