package deliverables

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/pollwait"
	"github.com/qlustered/qctl/internal/terminal"
)

// RenderResult is a render request's answer: the run as it was before any
// worker picked the render up, and the original response body.
type RenderResult struct {
	Run  *Run
	Body []byte
}

// RequestRender asks the server to render run runID. Numbers in the run's
// interface{} fields decode as jsonnum.Unmarshal decodes them.
func (c *Client) RequestRender(token string, runID int) (*RenderResult, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.RequestDeliverableRunRenderWithResponse(context.Background(), c.orgID, runID, &api.RequestDeliverableRunRenderParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "render deliverable run failed", "request_deliverable_run_render")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "render deliverable run failed", "request_deliverable_run_render")
	}
	var run Run
	if err := jsonnum.Unmarshal(resp.Body, &run); err != nil {
		return nil, fmt.Errorf("decode deliverable run: %w", err)
	}
	return &RenderResult{Run: &run, Body: resp.Body}, nil
}

// ErrRenderWaitTimeout is wrapped by the error WaitForRender returns when the
// render has not settled inside RenderWaitOptions.Timeout; that error exits 124.
var ErrRenderWaitTimeout = errors.New("timed out waiting for the render")

// renderWaitNow and renderWaitSleep are WaitForRender's clock.
var (
	renderWaitNow   = time.Now
	renderWaitSleep = time.Sleep
)

// SetRenderWaitClock replaces WaitForRender's clock (tests only). sleep
// advances the polling clock.
func SetRenderWaitClock(now func() time.Time, sleep func(time.Duration)) {
	renderWaitNow, renderWaitSleep = now, sleep
}

// ResetRenderWaitClock restores the real clock.
func ResetRenderWaitClock() { renderWaitNow, renderWaitSleep = time.Now, time.Sleep }

// RenderBaseline is a run's render attempt and render time from before a
// render request.
type RenderBaseline struct {
	AttemptID  int
	RenderedAt *time.Time
}

// BaselineOf returns run's RenderBaseline.
func BaselineOf(run *Run) RenderBaseline {
	return RenderBaseline{AttemptID: run.AttemptID, RenderedAt: run.RenderedAt}
}

// RenderWaitOptions configures WaitForRender.
type RenderWaitOptions struct {
	// Timeout bounds the whole wait.
	Timeout time.Duration
	// Progress, when set, receives each poll of an unsettled run whose state or
	// attempt_id differs from the previous one it received.
	Progress func(*Run)
}

// RenderSettled reports whether run is signed_off, delivered or cancelled,
// or has an attempt_id above base's and either a rendered_at other than
// base's or a render_error.
func RenderSettled(base RenderBaseline, run *Run) bool {
	if renderEnded(run.State) {
		return true
	}
	return run.AttemptID > base.AttemptID && (!sameTime(run.RenderedAt, base.RenderedAt) || hasText(run.RenderError))
}

func renderEnded(state api.DeliverableRunState) bool {
	switch state {
	case api.DeliverableRunStateSignedOff, api.DeliverableRunStateDelivered, api.DeliverableRunStateCancelled:
		return true
	}
	return false
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func hasText(s *string) bool { return s != nil && *s != "" }

// WaitForRender polls run runID until it is RenderSettled against base and
// returns that poll's run. A poll that fails with a permanent error
// (qerrors.IsPermanent) ends the wait with that error; any other failed poll
// is retried. A cancelled ctx ends it with pollwait.ErrStopped.
func (c *Client) WaitForRender(ctx context.Context, token string, runID int, base RenderBaseline, opts RenderWaitOptions) (*Run, error) {
	run, expired, err := pollwait.Run(ctx, func(ctx context.Context) (*Run, error) { return c.getRun(ctx, token, runID) }, pollwait.Options[*Run]{
		Timeout:  opts.Timeout,
		Now:      renderWaitNow,
		Sleep:    renderWaitSleep,
		Settled:  func(run *Run) bool { return RenderSettled(base, run) },
		Changed:  renderProgressChanged,
		Progress: opts.Progress,
	})
	switch {
	case err != nil:
		return nil, err
	case expired != nil:
		return nil, renderWaitTimeoutError(runID, base, opts.Timeout, expired.Last, expired.LastErr)
	}
	return run, nil
}

func renderProgressChanged(prev, next *Run) bool {
	return prev.State != next.State || prev.AttemptID != next.AttemptID
}

// renderWaitTimeoutError is the qerrors.NewTimedOut error naming what the
// wait was still waiting for. last is the latest run a poll returned, nil
// when none did; lastErr is the latest poll's error, nil when that poll
// succeeded.
func renderWaitTimeoutError(runID int, base RenderBaseline, timeout time.Duration, last *Run, lastErr error) error {
	var reason string
	switch {
	case last == nil:
		reason = fmt.Sprintf("no poll of deliverable run %d succeeded", runID)
	case last.AttemptID <= base.AttemptID:
		reason = fmt.Sprintf("no worker picked up the render of deliverable run %d (attempt_id is still %d). "+
			"The render stays queued until a worker takes it, so wait for it again with: "+
			"qctl deliverable-run render %d --wait", runID, last.AttemptID, runID)
	default:
		reason = fmt.Sprintf("render attempt %d of deliverable run %d is still running; only the wait stopped. "+
			"The server stops a render after 60 minutes by default", last.AttemptID, runID)
	}
	err := fmt.Errorf("%w after %s: %s\n\nNext: qctl describe deliverable-run %d", ErrRenderWaitTimeout, timeout, reason, runID)
	if lastErr != nil {
		err = fmt.Errorf("%w\n\nlast poll error: %w", err, lastErr)
	}
	return qerrors.NewTimedOut(err)
}

// WriteRenderProgress prints one line for a poll of a run whose render has not
// settled against base.
func WriteRenderProgress(w io.Writer, base RenderBaseline, run *Run) {
	if run.AttemptID <= base.AttemptID {
		fmt.Fprintf(w, "deliverable run %d: waiting for a worker to pick up the render\n", run.ID)
		return
	}
	fmt.Fprintf(w, "deliverable run %d: render attempt %d running\n", run.ID, run.AttemptID)
}

// RenderVerdict returns nil for a settled run in state rendered with no
// render_error. Otherwise it returns an error naming how the render ended,
// with the run's render_error or message and the command to run next.
func RenderVerdict(run *Run) error {
	failed := hasText(run.RenderError)
	if run.State == api.DeliverableRunStateRendered && !failed {
		return nil
	}

	var b strings.Builder
	next := fmt.Sprintf("qctl describe deliverable-run %d", run.ID)
	switch {
	case renderEnded(run.State):
		fmt.Fprintf(&b, "deliverable run %d is %s, so the render was not recorded", run.ID, terminal.Sanitize(string(run.State)))
	case failed:
		fmt.Fprintf(&b, "render attempt %d of deliverable run %d failed: %s", run.AttemptID, run.ID, terminal.Sanitize(*run.RenderError))
		switch run.State {
		case api.DeliverableRunStateOpen:
			b.WriteString("\n\nThe run has no draft file and stays open.")
		case api.DeliverableRunStateRendered:
			fmt.Fprintf(&b, "\n\nThe run keeps its previous draft file%s and stays rendered. "+
				"Sign-off does not check the failed render, so that older file can still be signed off.",
				renderedAtText(run.RenderedAt))
		default:
			fmt.Fprintf(&b, "\n\nThe run keeps its previous draft file%s and stays %s.",
				renderedAtText(run.RenderedAt), terminal.Sanitize(string(run.State)))
		}
		next = fmt.Sprintf("qctl deliverable-run readiness %d", run.ID)
	default:
		fmt.Fprintf(&b, "deliverable run %d rendered, but is %s", run.ID, terminal.Sanitize(string(run.State)))
		if hasText(run.Msg) {
			b.WriteString(": " + terminal.Sanitize(*run.Msg))
		} else if hasText(run.StaleReason) {
			b.WriteString(" (" + terminal.Sanitize(*run.StaleReason) + ")")
		}
		next = fmt.Sprintf("qctl deliverable-run render %d --wait", run.ID)
	}
	fmt.Fprintf(&b, "\n\nNext: %s", next)
	return errors.New(b.String())
}

func renderedAtText(t *time.Time) string {
	if t == nil {
		return ""
	}
	return " (rendered " + t.Format(time.RFC3339) + ")"
}

// WriteRenderRequested prints a render request's answer: the run, its state
// and, unless waiting, a Next: line.
func WriteRenderRequested(w io.Writer, run *Run, waiting bool) {
	fmt.Fprintf(w, "deliverable-run/%d render requested\n", run.ID)
	fmt.Fprintf(w, "  state   : %s (%s)\n", terminal.Sanitize(string(run.State)), terminal.Sanitize(RunStateLabel(run.State)))
	if !waiting {
		fmt.Fprintf(w, "Next: qctl describe deliverable-run %d\n", run.ID)
	}
}

// WriteRendered prints a run RenderVerdict passed: its render attempt and
// time, and a Next: line.
func WriteRendered(w io.Writer, run *Run) {
	fmt.Fprintf(w, "deliverable-run/%d rendered\n", run.ID)
	fmt.Fprintf(w, "  attempt : %d\n", run.AttemptID)
	if run.RenderedAt != nil {
		fmt.Fprintf(w, "  rendered: %s\n", run.RenderedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(w, "Next: qctl describe deliverable-run %d\n", run.ID)
}
