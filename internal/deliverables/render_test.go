package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const renderRunPath = "/api/orgs/" + testOrgID + "/deliverable-runs/42"

var (
	renderedBefore = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	renderedAfter  = renderedBefore.Add(time.Hour)
)

func strPtr(s string) *string { return &s }

// renderRun is run 42 in state with attempt and renderedAt, and render_error
// when renderError is not "".
func renderRun(state api.DeliverableRunState, attempt int, renderedAt *time.Time, renderError string) *Run {
	run := &Run{ID: 42, State: state, AttemptID: attempt, RenderedAt: renderedAt}
	if renderError != "" {
		run.RenderError = &renderError
	}
	return run
}

func TestRenderSettled(t *testing.T) {
	before, after := renderedBefore, renderedAfter
	sameInstant := renderedBefore.In(time.FixedZone("CEST", 2*60*60))
	rendered := RenderBaseline{AttemptID: 2, RenderedAt: &before}
	fresh := RenderBaseline{AttemptID: 0}
	for _, tc := range []struct {
		name string
		base RenderBaseline
		run  *Run
		want bool
	}{
		{"not yet picked up", rendered, renderRun(api.DeliverableRunStateRendered, 2, &before, ""), false},
		{"claimed, still rendering", rendered, renderRun(api.DeliverableRunStateRendered, 3, &before, ""), false},
		{"claimed, same instant in another zone", rendered, renderRun(api.DeliverableRunStateRendered, 3, &sameInstant, ""), false},
		{"re-rendered", rendered, renderRun(api.DeliverableRunStateRendered, 3, &after, ""), true},
		{"re-render failed", rendered, renderRun(api.DeliverableRunStateRendered, 3, &before, "DeliverableStateError: boom"), true},
		// An earlier attempt's error, before the request's claim clears it.
		{"old error, not yet picked up", rendered, renderRun(api.DeliverableRunStateRendered, 2, &before, "DeliverableStateError: old"), false},
		{"first render claimed", fresh, renderRun(api.DeliverableRunStateOpen, 1, nil, ""), false},
		{"first render made", fresh, renderRun(api.DeliverableRunStateRendered, 1, &after, ""), true},
		{"first render failed", fresh, renderRun(api.DeliverableRunStateOpen, 1, nil, "DeliverableStateError: empty"), true},
		{"rendered stale in flight", rendered, renderRun(api.DeliverableRunStateStale, 3, &after, ""), true},
		{"signed off meanwhile", rendered, renderRun(api.DeliverableRunStateSignedOff, 2, &before, ""), true},
		{"delivered meanwhile", rendered, renderRun(api.DeliverableRunStateDelivered, 2, &before, ""), true},
		{"cancelled meanwhile", fresh, renderRun(api.DeliverableRunStateCancelled, 0, nil, ""), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderSettled(tc.base, tc.run); got != tc.want {
				t.Errorf("RenderSettled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRenderVerdict(t *testing.T) {
	before, after := renderedBefore, renderedAfter
	stale := renderRun(api.DeliverableRunStateStale, 3, &after, "")
	stale.Msg = strPtr("Render attempt 3 succeeded, but a table this run reads was overridden while the render was in flight; re-render required.")
	for _, tc := range []struct {
		name    string
		run     *Run
		want    []string
		notWant string
	}{
		{"rendered", renderRun(api.DeliverableRunStateRendered, 3, &after, ""), nil, ""},
		{"first render failed", renderRun(api.DeliverableRunStateOpen, 1, nil, "DeliverableStateError: render refused: in-scope rows are blocked and unwaived (x: 2)"),
			[]string{"render attempt 1 of deliverable run 42 failed: DeliverableStateError: render refused", "no draft file and stays open",
				"Next: qctl deliverable-run readiness 42"}, "signed off"},
		{"re-render failed", renderRun(api.DeliverableRunStateRendered, 3, &before, "DeliverableStateError: no backup settings"),
			[]string{"keeps its previous draft file (rendered 2026-10-01T12:00:00Z) and stays rendered", "can still be signed off"}, ""},
		{"stale run's re-render failed", renderRun(api.DeliverableRunStateStale, 3, &before, "DeliverableStateError: no template"),
			[]string{"stays stale."}, "signed off"},
		{"stale in flight", stale, []string{"deliverable run 42 rendered, but is stale: Render attempt 3 succeeded, but a table",
			"Next: qctl deliverable-run render 42 --wait"}, ""},
		{"cancelled", renderRun(api.DeliverableRunStateCancelled, 2, &before, ""),
			[]string{"deliverable run 42 is cancelled, so the render was not recorded", "Next: qctl describe deliverable-run 42"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RenderVerdict(tc.run)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("RenderVerdict = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("RenderVerdict passed a render that did not succeed")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Errorf("exit = %d, want 1", code)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %q, want %q in it", err, want)
				}
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Errorf("err = %q, want no %q in it", err, tc.notWant)
			}
		})
	}
}

// renderPoll is a rendered run 42 body for one poll.
func renderPoll(attempt int, renderedAt *time.Time) map[string]any {
	body := map[string]any{
		"id": 42, "deliverable_id": "c0ffee01-2222-4333-8444-555555555555",
		"revision_id": "e1e1e1e1-2222-4333-8444-777777777777", "revision_mode": "new_period",
		"state": "rendered", "attempt_id": attempt, "sequence": 1, "allow_empty": false,
		"run_params": map[string]any{}, "rendered_at": nil, "render_error": nil,
	}
	if renderedAt != nil {
		body["rendered_at"] = renderedAt.Format(time.RFC3339)
	}
	return body
}

// serveRenderPolls answers each read of run 42 with the next response,
// repeating the last one. A response is a run body, or an int status the
// route fails with. It returns the client and the poll count.
func serveRenderPolls(t *testing.T, responses ...any) (*Client, func() int) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	var mu sync.Mutex
	polls := 0
	mock.RegisterHandler("GET", renderRunPath, func(w http.ResponseWriter, _ *http.Request) {
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
	return NewClient(mock.URL(), testOrgID, 0), func() int {
		mu.Lock()
		defer mu.Unlock()
		return polls
	}
}

// stubRenderClock replaces WaitForRender's clock with one that only moves
// when the wait sleeps.
func stubRenderClock(t *testing.T) {
	t.Helper()
	now := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	SetRenderWaitClock(func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	t.Cleanup(ResetRenderWaitClock)
}

func TestWaitForRenderWaitsForTheAttemptAfterTheRequest(t *testing.T) {
	stubRenderClock(t)
	before, after := renderedBefore, renderedAfter
	c, polls := serveRenderPolls(t,
		renderPoll(2, &before),
		http.StatusBadGateway,
		renderPoll(3, &before),
		renderPoll(3, &before),
		renderPoll(3, &after),
	)
	var progress bytes.Buffer
	base := RenderBaseline{AttemptID: 2, RenderedAt: &before}
	run, err := c.WaitForRender(context.Background(), "token", 42, base, RenderWaitOptions{
		Timeout:  time.Minute,
		Progress: func(run *Run) { WriteRenderProgress(&progress, base, run) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if polls() != 5 || run.AttemptID != 3 || !run.RenderedAt.Equal(after) {
		t.Errorf("polls = %d, run = attempt %d rendered %v; want the fifth poll's", polls(), run.AttemptID, run.RenderedAt)
	}
	want := "deliverable run 42: waiting for a worker to pick up the render\n" +
		"deliverable run 42: render attempt 3 running\n"
	if progress.String() != want {
		t.Errorf("progress = %q, want %q", progress.String(), want)
	}
}

func TestWaitForRenderTimeout(t *testing.T) {
	before := renderedBefore
	base := RenderBaseline{AttemptID: 2, RenderedAt: &before}
	for _, tc := range []struct {
		name string
		poll any
		want string
	}{
		{"never picked up", renderPoll(2, &before), "The render stays queued until a worker takes it"},
		{"still rendering", renderPoll(3, &before), "render attempt 3 of deliverable run 42 is still running"},
		{"no poll succeeded", http.StatusBadGateway, "no poll of deliverable run 42 succeeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubRenderClock(t)
			c, _ := serveRenderPolls(t, tc.poll)
			_, err := c.WaitForRender(context.Background(), "token", 42, base, RenderWaitOptions{Timeout: 10 * time.Second})
			if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
				t.Fatalf("exit = %d, want 124: %v", code, err)
			}
			if !errors.Is(err, ErrRenderWaitTimeout) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q in it", err, tc.want)
			}
		})
	}
}

func TestWaitForRenderStopsOnAPermanentError(t *testing.T) {
	stubRenderClock(t)
	c, polls := serveRenderPolls(t, http.StatusNotFound)
	_, err := c.WaitForRender(context.Background(), "token", 42, RenderBaseline{}, RenderWaitOptions{Timeout: time.Minute})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound || polls() != 1 {
		t.Fatalf("polls = %d, exit = %d: %v", polls(), code, err)
	}
}

func TestRequestRenderPostsAndKeepsTheBody(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	before := renderedBefore
	body, _ := json.Marshal(renderPoll(2, &before))
	posts := 0
	mock.RegisterHandler("POST", renderRunPath+"/render", func(w http.ResponseWriter, _ *http.Request) {
		posts++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
	result, err := NewClient(mock.URL(), testOrgID, 0).RequestRender("token", 42)
	if err != nil {
		t.Fatal(err)
	}
	if posts != 1 || result.Run.AttemptID != 2 || !bytes.Equal(bytes.TrimSpace(result.Body), body) {
		t.Errorf("posts = %d, attempt = %d, body = %s", posts, result.Run.AttemptID, result.Body)
	}
}
