package apply

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/testutil"
)

// stubSleep drives the shared retry clock in internal/cmdutil so the schedule
// costs no wall time, and records what the code would have waited.
func stubSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	cmdutil.SetThrottleSleep(func(d time.Duration) { slept = append(slept, d) })
	t.Cleanup(cmdutil.ResetThrottleSleep)
	return &slept
}

// Atlas declares no 429 on instantiate, but apply wraps it in RetryOnThrottle:
// a 429 that reaches the route anyway -- here a body conforming to nothing, as
// a proxy's would -- is retried, and the identical request is accepted once
// the refusals stop.
func TestApplyTableRuleInstantiate_RetriesAThrottledRequest(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	slept := stubSleep(t)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(nil))
	})
	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.Header().Set("Retry-After", "7")
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("<html><body><h1>429 Too Many Requests</h1></body></html>"))
			return
		}
		testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
	})

	path := env.CreateFile("rule.yaml", `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: `+testRuleRevID+`
  dataset_id: 1
  column_mapping:
    email: email_col
`)
	out, err := execApply(t, path, "--yes")
	if err != nil {
		t.Fatalf("expected the retry to succeed, got: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 instantiate attempts, got %d", calls)
	}
	if len(*slept) != 2 {
		t.Fatalf("expected 2 backoff waits, got %v", *slept)
	}
	// Not the computed backoff [2s 4s]: RetryOnThrottle honours the server's
	// Retry-After over its schedule, so this also pins that InstantiateRule
	// forwards the header.
	if (*slept)[0] != 7*time.Second || (*slept)[1] != 7*time.Second {
		t.Errorf("backoff schedule = %v, want [7s 7s] from the Retry-After header", *slept)
	}
	if !strings.Contains(out, "throttling") {
		t.Errorf("expected the delay to be explained on stdout, got:\n%s", out)
	}
}

func TestApplyTableRuleInstantiate_GivesUpAfterTheAttemptLimit(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	stubSleep(t)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(nil))
	})
	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]interface{}{"detail": "still busy"})
	})

	path := env.CreateFile("rule.yaml", `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: `+testRuleRevID+`
  dataset_id: 1
  column_mapping:
    email: email_col
`)
	if _, err := execApply(t, path, "--yes"); err == nil {
		t.Fatal("expected an error once the attempts are exhausted")
	}
	if calls != cmdutil.ThrottleMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", cmdutil.ThrottleMaxAttempts, calls)
	}
}

// A non-429 failure is permanent: retrying it just wastes the user's time.
func TestApplyTableRuleInstantiate_DoesNotRetryAPermanentFailure(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	stubSleep(t)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(nil))
	})
	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusBadRequest, map[string]interface{}{"detail": "column not found"})
	})

	path := env.CreateFile("rule.yaml", `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: `+testRuleRevID+`
  dataset_id: 1
  column_mapping:
    email: email_col
`)
	if _, err := execApply(t, path, "--yes"); err == nil {
		t.Fatal("expected an error for a 400")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a permanent failure, got %d", calls)
	}
}
