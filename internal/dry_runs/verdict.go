package dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/terminal"
)

// terminalStates are the states IsTerminal accepts.
var terminalStates = map[IngestionJobState]bool{
	api.IngestionJobStateFinished:          true,
	api.IngestionJobStateError:             true,
	api.IngestionJobStateKilled:            true,
	api.IngestionJobStateStopped:           true,
	api.IngestionJobStateVersionIDMismatch: true,
	api.IngestionJobStateFileMissing:       true,
	api.IngestionJobStateBlocked:           true,
	api.IngestionJobStatePartitioned:       true,
}

// IsTerminal reports whether state ends a dry-run progress watch.
// A watch stop state may later be retried.
func IsTerminal(state IngestionJobState) bool {
	return terminalStates[state]
}

// Blocker is the alert that blocks a dry run.
type Blocker struct {
	AlertID int
	// Message is the alert's message; "" when unknown.
	Message string
}

// Verdict returns nil when job is finished. For any other state it returns an
// error naming the state, then job's rule load errors and error message when
// it has them, then a Next line: "qctl describe dry-run-job <id>" except for a
// blocked job.
//
// A blocked job's alert is blocker, or else job's alert_item_id; the error
// names it and its Next line describes it. Without either, the Next line
// describes job's ingestion job when job has one. Any other job's
// alert_item_id is listed before the Next line, and blocker is ignored.
func Verdict(job *DryRunJobFull, blocker *Blocker) error {
	if job.State == api.IngestionJobStateFinished {
		return nil
	}
	blocked := job.State == api.IngestionJobStateBlocked
	switch {
	case !blocked:
		blocker = nil
	case blocker == nil && job.AlertItemID != nil:
		blocker = &Blocker{AlertID: *job.AlertItemID}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "dry-run-job %d ended in state %s", job.ID, terminal.Sanitize(string(job.State)))
	next := fmt.Sprintf("qctl describe dry-run-job %d", job.ID)
	if blocked {
		switch {
		case blocker != nil:
			fmt.Fprintf(&b, ": alert %d blocks it", blocker.AlertID)
			if msg := strings.TrimSpace(blocker.Message); msg != "" {
				b.WriteString("\n\nalert message: " + indentContinuation(markdown.PlainText(msg)))
			}
			next = fmt.Sprintf("qctl describe alert %d", blocker.AlertID)
		case job.IngestionJobID != nil:
			next = fmt.Sprintf("qctl describe ingestion-job %d", *job.IngestionJobID)
		}
	}
	WriteFailureDetail(&b, job)
	if job.AlertItemID != nil && blocker == nil {
		fmt.Fprintf(&b, "\n\nalert: %d (qctl describe alert %d)", *job.AlertItemID, *job.AlertItemID)
	}
	return errors.New(b.String() + "\n\nNext: " + next)
}

// WriteFailureDetail appends job's rule load errors and error message.
func WriteFailureDetail(b *strings.Builder, job *DryRunJobFull) {
	if job.RuleLoadErrors != nil && len(*job.RuleLoadErrors) > 0 {
		b.WriteString("\n\nrule load errors:")
		for _, e := range *job.RuleLoadErrors {
			fmt.Fprintf(b, "\n  position %d (%s): %s", e.Position, terminal.Sanitize(e.ErrorType),
				indentContinuation(terminal.Sanitize(e.Message)))
		}
	}
	if job.ErrorMessage != nil && strings.TrimSpace(*job.ErrorMessage) != "" {
		b.WriteString("\n\nerror message: " + indentContinuation(markdown.PlainText(*job.ErrorMessage)))
	}
}

// indentContinuation indents every line of s after the first by two spaces.
func indentContinuation(s string) string {
	return strings.ReplaceAll(s, "\n", "\n  ")
}

// Verdict returns Verdict(job, blocker). For a blocked job without an
// alert_item_id, blocker comes from the alert_item_id of job's ingestion job;
// it is nil when that read fails or names no alert.
func (c *Client) Verdict(accessToken string, job *DryRunJobFull) error {
	return c.VerdictContext(context.Background(), accessToken, job)
}

// VerdictContext is Verdict with ctx controlling the blocker lookup.
func (c *Client) VerdictContext(ctx context.Context, accessToken string, job *DryRunJobFull) error {
	if job.State != api.IngestionJobStateBlocked || job.AlertItemID != nil || job.IngestionJobID == nil {
		return Verdict(job, nil)
	}
	blocker := c.blocker(ctx, accessToken, *job.IngestionJobID)
	return Verdict(job, blocker)
}

// blocker reads the alert ingestion job ingestionJobID names; nil when the
// read fails or names none.
func (c *Client) blocker(ctx context.Context, accessToken string, ingestionJobID int) *Blocker {
	ic, err := ingestion.NewClient(c.baseURL, c.organizationID.String(), c.verbosity)
	if err != nil {
		return nil
	}
	ij, err := ic.GetIngestionJobContext(ctx, accessToken, ingestionJobID)
	if err != nil || ctx.Err() != nil || ij.AlertItemID == nil {
		return nil
	}
	b := &Blocker{AlertID: *ij.AlertItemID}
	if ij.AlertItemMsg != nil {
		b.Message = *ij.AlertItemMsg
	}
	return b
}
