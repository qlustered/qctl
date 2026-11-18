package composite_dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/pollwait"
	"github.com/qlustered/qctl/internal/terminal"
)

// ErrWaitTimeout is wrapped by the error Wait returns when the dry run has not
// settled inside WaitOptions.Timeout; that error exits 124.
var ErrWaitTimeout = errors.New("timed out waiting for the composite dry run")

// waitNow and waitSleep are Wait's clock.
var (
	waitNow   = time.Now
	waitSleep = time.Sleep
)

// SetWaitClock replaces Wait's clock (tests only). sleep advances the polling clock.
func SetWaitClock(now func() time.Time, sleep func(time.Duration)) {
	waitNow, waitSleep = now, sleep
}

// ResetWaitClock restores the real clock.
func ResetWaitClock() { waitNow, waitSleep = time.Now, time.Sleep }

// WaitOptions configures Wait.
type WaitOptions struct {
	// Timeout bounds the whole wait.
	Timeout time.Duration
	// Progress, when set, receives each poll of an unsettled dry run whose
	// state or auto_enable_state differs from the previous one it received.
	Progress func(*Detail)
}

// Settled reports whether d's state is neither created nor running and its
// auto_enable_state is not pending.
func Settled(d *Detail) bool {
	return !inFlight(d) && !autoEnablePending(d)
}

func inFlight(d *Detail) bool {
	switch d.Job.State {
	case api.IngestionJobStateCreated, api.IngestionJobStateRunning:
		return true
	}
	return false
}

func autoEnablePending(d *Detail) bool {
	s := d.Job.AutoEnableState
	return s != nil && *s == api.CompositeDryRunAutoEnableStatePending
}

// Wait polls the dry run jobID until it is Settled and returns that poll's
// detail. A poll that fails with a permanent error (qerrors.IsPermanent) ends
// the wait with that error; any other failed poll is retried. A cancelled
// ctx ends it with pollwait.ErrStopped.
func (c *Client) Wait(ctx context.Context, accessToken string, jobID int, opts WaitOptions) (*Detail, error) {
	// pollwait.Interval's cadence fits the one atlas publishes for this route
	// in docs/composite_dataset/how_to_launch_composite_dry_run.md, "Polling":
	// 2-5 s, easing to 5-10 s once the run has taken 30 s.
	detail, expired, err := pollwait.Run(ctx, func(ctx context.Context) (*Detail, error) { return c.GetContext(ctx, accessToken, jobID) }, pollwait.Options[*Detail]{
		Timeout:  opts.Timeout,
		Now:      waitNow,
		Sleep:    waitSleep,
		Settled:  Settled,
		Changed:  progressChanged,
		Progress: opts.Progress,
	})
	switch {
	case err != nil:
		return nil, err
	case expired != nil:
		return nil, waitTimeoutError(jobID, opts.Timeout, expired.Last, expired.LastErr)
	}
	return detail, nil
}

func progressChanged(prev, next *Detail) bool {
	if prev.Job.State != next.Job.State {
		return true
	}
	a, b := prev.Job.AutoEnableState, next.Job.AutoEnableState
	if a == nil || b == nil {
		return a != b
	}
	return *a != *b
}

// waitTimeoutError is the qerrors.NewTimedOut error naming what the wait was
// still waiting for. last is the latest detail a poll returned, nil when none
// did; lastErr is the latest poll's error, nil when that poll succeeded.
func waitTimeoutError(jobID int, timeout time.Duration, last *Detail, lastErr error) error {
	var reason string
	switch {
	case last == nil:
		reason = fmt.Sprintf("no poll of composite dry run %d succeeded", jobID)
	case inFlight(last):
		reason = fmt.Sprintf("composite dry run %d is still %s; only the wait stopped",
			jobID, terminal.Sanitize(string(last.Job.State)))
	default:
		reason = fmt.Sprintf("composite dry run %d reached %s, but its auto-enable is still pending, "+
			"and the backend does not retry it; apply the manifest again", jobID, terminal.Sanitize(string(last.Job.State)))
	}
	err := fmt.Errorf("%w after %s: %s\n\nNext: qctl describe composite-dry-run-job %d", ErrWaitTimeout, timeout, reason, jobID)
	if lastErr != nil {
		err = fmt.Errorf("%w\n\nlast poll error: %w", err, lastErr)
	}
	return qerrors.NewTimedOut(err)
}

// WriteProgress prints one line for a poll of an unsettled dry run.
func WriteProgress(w io.Writer, d *Detail) {
	line := fmt.Sprintf("composite dry run %d: %s", d.Job.ID, terminal.Sanitize(string(d.Job.State)))
	if !inFlight(d) && autoEnablePending(d) {
		line += "; waiting for its auto-enable"
	}
	fmt.Fprintln(w, line)
}

// Verdict returns nil for a settled dry run that finished with an
// auto_enable_state of null or enabled. Otherwise it returns an error naming
// how the dry run ended, with its error_message, user_message and
// auto_enable_detail.
func Verdict(d *Detail) error {
	job := d.Job
	autoEnable := ""
	if job.AutoEnableState != nil {
		autoEnable = terminal.Sanitize(string(*job.AutoEnableState))
	}
	finished := job.State == api.IngestionJobStateFinished
	if finished && (autoEnable == "" || autoEnable == string(api.CompositeDryRunAutoEnableStateEnabled)) {
		return nil
	}

	var b strings.Builder
	if finished {
		fmt.Fprintf(&b, "composite dry run %d finished, but did not enable the revision (auto_enable_state: %s)", job.ID, autoEnable)
	} else {
		fmt.Fprintf(&b, "composite dry run %d did not finish (state: %s", job.ID, terminal.Sanitize(string(job.State)))
		if job.ErrorType != nil && *job.ErrorType != "" {
			fmt.Fprintf(&b, ", error_type: %s", terminal.Sanitize(*job.ErrorType))
		}
		b.WriteString(")")
	}
	for _, part := range []*string{job.ErrorMessage, job.UserMessage} {
		if part != nil && *part != "" {
			b.WriteString("\n\n" + markdown.PlainText(*part))
		}
	}
	if job.AutoEnableDetail != nil && *job.AutoEnableDetail != "" {
		label := "auto-enable"
		if autoEnable != "" {
			label += " " + autoEnable
		}
		fmt.Fprintf(&b, "\n\n%s: %s", label, markdown.PlainText(*job.AutoEnableDetail))
	}
	fmt.Fprintf(&b, "\n\nNext: qctl describe composite-dry-run-job %d", job.ID)
	return errors.New(b.String())
}

// WriteOutcome prints a dry run Verdict passed: a finished line, the revision
// line when the detail carries revision_state, a summary line when it carries
// a structured summary, and a Next: line.
func WriteOutcome(w io.Writer, d *Detail, revisionName string) {
	job := d.Job
	fmt.Fprintf(w, "composite-dry-run-job/%d %s\n", job.ID, terminal.Sanitize(string(job.State)))

	enabled := false
	if job.RevisionState != nil {
		state := terminal.Sanitize(*job.RevisionState)
		enabled = *job.RevisionState == "enabled"
		if job.AutoEnableState != nil {
			state += "; auto-enable: " + terminal.Sanitize(string(*job.AutoEnableState))
		}
		fmt.Fprintf(w, "  revision : %s (%s)\n", terminal.Sanitize(revisionName), state)
	}
	if d.StructuredSummary != nil {
		summary := job.StructuredSummary
		fmt.Fprintf(w, "  summary  : sampled %d spine rows; activation blockers: %d\n",
			summary.Sample.SampledSpineRows, summary.ActivationBlockerCount)
	}

	if enabled {
		fmt.Fprintf(w, "Next: qctl get composite-run-jobs --group-id %s\n", job.CompositeSpecGroupID)
		return
	}
	fmt.Fprintf(w, "Next: qctl describe composite-dry-run-job %d\n", job.ID)
}
