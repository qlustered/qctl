package composite_dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/pollwait"
	"github.com/qlustered/qctl/internal/testutil"
)

// waitDetail is a detail body for dry run 5512 in state, carrying
// auto_enable_state unless autoEnable is "", with overrides on top.
func waitDetail(state, autoEnable string, overrides map[string]any) map[string]any {
	d := map[string]any{
		"id":                         5512,
		"composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
		"composite_spec_group_id":    "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10",
		"downstream_dataset_id":      12,
		"organization_id":            testOrgID,
		"state":                      state,
		"max_spine_rows":             1000,
		"created_at":                 "2026-09-22T10:00:00Z",
		"ingestion_job_id":           9921,
		"user_message":               nil,
		"wizard_step":                nil,
		"structured_summary":         nil,
		"revision_state":             "draft",
	}
	if autoEnable != "" {
		d["auto_enable_state"] = autoEnable
	}
	for k, v := range overrides {
		d[k] = v
	}
	return d
}

// serveSequence answers each poll of dry run 5512 with the next response,
// repeating the last one. A response is a detail body, or an int status the
// route fails with. It returns the client and the poll count.
func serveSequence(t *testing.T, responses ...any) (*Client, func() int) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	var mu sync.Mutex
	polls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/composite-dry-runs/5512", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		response := responses[min(polls, len(responses)-1)]
		polls++
		mu.Unlock()
		if status, ok := response.(int); ok {
			testutil.RespondJSON(w, status, map[string]any{"detail": "boom"})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})
	c, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c, func() int {
		mu.Lock()
		defer mu.Unlock()
		return polls
	}
}

// stubClock replaces Wait's clock with one that only moves when Wait sleeps,
// and returns the sleeps it was asked for.
func stubClock(t *testing.T) *[]time.Duration {
	t.Helper()
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	SetWaitClock(func() time.Time { return now }, func(d time.Duration) {
		sleeps = append(sleeps, d)
		now = now.Add(d)
	})
	t.Cleanup(ResetWaitClock)
	return &sleeps
}

// The backend commits `finished` before it records the auto-enable outcome,
// so a finished dry run whose auto-enable is still pending is not done.
func TestWait_WaitsOutAPendingAutoEnable(t *testing.T) {
	stubClock(t)
	c, polls := serveSequence(t,
		waitDetail("running", "pending", nil),
		waitDetail("finished", "pending", nil),
		waitDetail("finished", "enabled", map[string]any{"revision_state": "enabled"}),
	)

	d, err := c.Wait(context.Background(), "token", 5512, WaitOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if polls() != 3 {
		t.Errorf("polls = %d, want 3: the finished-but-pending poll must not end the wait", polls())
	}
	if d.Job.AutoEnableState == nil || *d.Job.AutoEnableState != "enabled" {
		t.Errorf("auto_enable_state = %v, want the settled poll's enabled", d.Job.AutoEnableState)
	}
}

// A dry run with no auto-enable intent settles on its terminal state alone.
func TestWait_NoIntentSettlesOnTheTerminalState(t *testing.T) {
	stubClock(t)
	c, polls := serveSequence(t, waitDetail("created", "", nil), waitDetail("finished", "", nil))

	if _, err := c.Wait(context.Background(), "token", 5512, WaitOptions{Timeout: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if polls() != 2 {
		t.Errorf("polls = %d, want 2", polls())
	}
}

// A state outside created and running ends the wait rather than running it to
// its timeout.
func TestWait_AnUnexpectedStateSettles(t *testing.T) {
	stubClock(t)
	c, polls := serveSequence(t, waitDetail("killed", "", nil))

	d, err := c.Wait(context.Background(), "token", 5512, WaitOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if polls() != 1 {
		t.Errorf("polls = %d, want 1", polls())
	}
	if Verdict(d) == nil {
		t.Error("Verdict passed a dry run that never finished")
	}
}

// Progress hears each change once, and never the settled poll.
func TestWait_ReportsEachChangeOnce(t *testing.T) {
	stubClock(t)
	c, _ := serveSequence(t,
		waitDetail("created", "pending", nil),
		waitDetail("running", "pending", nil),
		waitDetail("running", "pending", nil),
		waitDetail("finished", "pending", nil),
		waitDetail("finished", "withheld", nil),
	)
	var progress bytes.Buffer
	_, err := c.Wait(context.Background(), "token", 5512, WaitOptions{
		Timeout:  time.Minute,
		Progress: func(d *Detail) { WriteProgress(&progress, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `composite dry run 5512: created
composite dry run 5512: running
composite dry run 5512: finished; waiting for its auto-enable
`
	if progress.String() != want {
		t.Errorf("progress:\n--- got ---\n%s--- want ---\n%s", progress.String(), want)
	}
}

// The cadence is 2 s for the first 30 s and 5 s after, and the last sleep
// stops at the deadline, where one final poll runs.
func TestWait_FollowsThePublishedCadence(t *testing.T) {
	tests := []struct {
		name      string
		timeout   time.Duration
		wantSleep []time.Duration
	}{
		{
			name:    "eases off after 30s",
			timeout: 40 * time.Second,
			wantSleep: []time.Duration{
				2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second,
				2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second,
				2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second,
				5 * time.Second, 5 * time.Second,
			},
		},
		{
			name:      "the last sleep is clipped to the deadline",
			timeout:   3 * time.Second,
			wantSleep: []time.Duration{2 * time.Second, time.Second},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sleeps := stubClock(t)
			c, polls := serveSequence(t, waitDetail("running", "", nil))

			_, err := c.Wait(context.Background(), "token", 5512, WaitOptions{Timeout: tt.timeout})
			if !errors.Is(err, ErrWaitTimeout) {
				t.Fatalf("err = %v, want ErrWaitTimeout", err)
			}
			if got := *sleeps; !equalDurations(got, tt.wantSleep) {
				t.Errorf("sleeps = %v, want %v", got, tt.wantSleep)
			}
			if polls() != len(tt.wantSleep)+1 {
				t.Errorf("polls = %d, want %d: one before each sleep and one at the deadline", polls(), len(tt.wantSleep)+1)
			}
		})
	}
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A timeout exits 124 and says what the wait was still waiting for.
func TestWait_TimeoutNamesWhatItWaitedFor(t *testing.T) {
	tests := []struct {
		name      string
		responses []any
		want      []string
	}{
		{
			name:      "still running",
			responses: []any{waitDetail("running", "pending", nil)},
			want:      []string{"after 5s", "composite dry run 5512 is still running; only the wait stopped"},
		},
		{
			name:      "a stranded auto-enable",
			responses: []any{waitDetail("finished", "pending", nil)},
			want: []string{
				"composite dry run 5512 reached finished, but its auto-enable is still pending",
				"the backend does not retry it; apply the manifest again",
			},
		},
		{
			name:      "the last poll failed",
			responses: []any{waitDetail("running", "", nil), http.StatusInternalServerError},
			want:      []string{"is still running", "last poll error:"},
		},
		{
			name:      "no poll succeeded",
			responses: []any{http.StatusInternalServerError},
			want:      []string{"no poll of composite dry run 5512 succeeded", "last poll error:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubClock(t)
			c, _ := serveSequence(t, tt.responses...)

			_, err := c.Wait(context.Background(), "token", 5512, WaitOptions{Timeout: 5 * time.Second})
			if !errors.Is(err, ErrWaitTimeout) {
				t.Fatalf("err = %v, want ErrWaitTimeout", err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitTimeout)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%s", want, err)
				}
			}
			if !strings.Contains(err.Error(), "Next: qctl describe composite-dry-run-job 5512") {
				t.Errorf("error lacks the describe recipe:\n%s", err)
			}
		})
	}
}

// A 5xx is retried; a 404 cannot change and ends the wait with its own code.
func TestWait_RetriesOnlyWhatCanChange(t *testing.T) {
	t.Run("a 500 is retried", func(t *testing.T) {
		stubClock(t)
		c, polls := serveSequence(t, http.StatusInternalServerError, waitDetail("finished", "", nil))
		if _, err := c.Wait(context.Background(), "token", 5512, WaitOptions{Timeout: time.Minute}); err != nil {
			t.Fatal(err)
		}
		if polls() != 2 {
			t.Errorf("polls = %d, want 2", polls())
		}
	})
	t.Run("a 404 ends the wait", func(t *testing.T) {
		sleeps := stubClock(t)
		c, polls := serveSequence(t, http.StatusNotFound)
		_, err := c.Wait(context.Background(), "token", 5512, WaitOptions{Timeout: time.Minute})
		if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
			t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitNotFound, err)
		}
		if polls() != 1 || len(*sleeps) != 0 {
			t.Errorf("polls = %d, sleeps = %v, want one poll and no sleep", polls(), *sleeps)
		}
	})
}

// A closed Done ends the wait with pollwait.ErrStopped before the next poll.
func TestWait_DoneStopsTheWait(t *testing.T) {
	stubClock(t)
	c, polls := serveSequence(t, waitDetail("running", "", nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	_, err := c.Wait(ctx, "token", 5512, WaitOptions{Timeout: time.Minute})
	if !errors.Is(err, pollwait.ErrStopped) {
		t.Fatalf("err = %v, want pollwait.ErrStopped", err)
	}
	if polls() != 0 {
		t.Errorf("polls = %d, want 0", polls())
	}
}

// getDetail decodes body through Get, as Wait does.
func getDetail(t *testing.T, body map[string]any) *Detail {
	t.Helper()
	c, _ := serveSequence(t, body)
	d, err := c.Get("token", 5512)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestVerdict(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want []string // nil: the verdict passes
	}{
		{name: "finished without an intent", body: waitDetail("finished", "", nil)},
		{name: "finished and enabled", body: waitDetail("finished", "enabled", nil)},
		{
			name: "finished but withheld",
			body: waitDetail("finished", "withheld", map[string]any{"auto_enable_detail": "superseded by newer apply"}),
			want: []string{
				"composite dry run 5512 finished, but did not enable the revision (auto_enable_state: withheld)",
				"\n\nauto-enable withheld: superseded by newer apply",
			},
		},
		{
			name: "finished but the enable raised",
			body: waitDetail("finished", "failed", map[string]any{"auto_enable_detail": "RuntimeError: boom"}),
			want: []string{"(auto_enable_state: failed)", "auto-enable failed: RuntimeError: boom"},
		},
		{
			name: "an error",
			body: waitDetail("error", "withheld", map[string]any{
				"error_type":         "CompositeRunnerSpecError",
				"error_message":      "field policy_ref is not on claims",
				"user_message":       "The column policy_ref no longer exists on claims.",
				"auto_enable_detail": "dry run failed: CompositeRunnerSpecError: field policy_ref is not on claims",
			}),
			want: []string{
				"composite dry run 5512 did not finish (state: error, error_type: CompositeRunnerSpecError)",
				"\n\nfield policy_ref is not on claims\n\nThe column policy_ref no longer exists on claims.",
				"auto-enable withheld: dry run failed",
			},
		},
		{
			name: "superseded",
			body: waitDetail("stopped", "", map[string]any{
				"error_type":    "superseded",
				"error_message": "superseded by a new composite dry run launch",
			}),
			want: []string{"did not finish (state: stopped, error_type: superseded)", "superseded by a new composite dry run launch"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Verdict(getDetail(t, tt.body))
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Verdict = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Verdict = nil, want an error")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitGenericError)
			}
			for _, want := range append(tt.want, "\n\nNext: qctl describe composite-dry-run-job 5512") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%s", want, err)
				}
			}
		})
	}
}

func TestWriteOutcome(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "auto-enabled",
			body: waitDetail("finished", "enabled", map[string]any{
				"revision_state": "enabled",
				"structured_summary": map[string]any{
					"mode":                     "composite",
					"activation_blocker_count": 0,
					"sample":                   map[string]any{"sampled_spine_rows": 200, "requested_spine_rows": 200, "eligible_spine_rows": 5000},
				},
			}),
			want: `composite-dry-run-job/5512 finished
  revision : v3 (enabled; auto-enable: enabled)
  summary  : sampled 200 spine rows; activation blockers: 0
Next: qctl get composite-run-jobs --group-id 0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10
`,
		},
		{
			name: "a draft with no summary",
			body: waitDetail("finished", "", nil),
			want: `composite-dry-run-job/5512 finished
  revision : v3 (draft)
Next: qctl describe composite-dry-run-job 5512
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			WriteOutcome(&out, getDetail(t, tt.body), "v3")
			if out.String() != tt.want {
				t.Errorf("--- got ---\n%s--- want ---\n%s", out.String(), tt.want)
			}
		})
	}
}

func TestWaitCancelsAStalledHTTPRequest(t *testing.T) {
	// Use a real server to cover transport cancellation as well as the loop.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		testutil.WaitForRequestCancellation(t, r)
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, testOrgID, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Wait(t.Context(), "token", 5512, WaitOptions{Timeout: 50 * time.Millisecond})
	if !errors.Is(err, ErrWaitTimeout) || qerrors.GetExitCode(err) != qerrors.ExitTimeout {
		t.Fatalf("Wait = %v (exit %d), want timeout", err, qerrors.GetExitCode(err))
	}
}
