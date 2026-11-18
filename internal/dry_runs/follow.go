package dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/qlustered/qctl/internal/ws"
)

// StateFrame returns a progress-stream frame carrying state.
func StateFrame(state IngestionJobState) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"state": string(state)})
	return raw
}

// FollowOptions configures Follow.
type FollowOptions struct {
	// OnFrame handles one progress frame and returns the state it carries, ""
	// for none. When the server ends the stream before a terminal frame,
	// Follow also hands it a StateFrame for the state it then reads over REST,
	// if that differs from the last state seen.
	OnFrame func(raw json.RawMessage) (string, error)
	// OnReconnect becomes the stream's ws.Config.OnReconnect.
	OnReconnect func(attempt int, wait time.Duration, err error)
	// Resume is the command line that follows the job again.
	Resume string
}

// Follow hands job's progress frames to opts.OnFrame until one carries a
// state IsTerminal accepts, the stream ends, or ctx is done. job is the job as
// read before the stream opens. Follow returns:
//
//   - after a terminal frame, nil for finished, and otherwise Client.Verdict
//     for the job re-read over REST with the frame's state, or for a job
//     carrying only that state when the read fails;
//   - when ctx is done, an error saying the job is still in the last state
//     seen and was not stopped, whose Unwrap is ctx's cause;
//   - when the stream fails, that failure, wrapped;
//   - when the server ends the stream before a terminal frame, the outcome of
//     reading the job over REST: the read's failure, wrapped; Client.Verdict
//     for a terminal state; for any other, an error saying the server closed
//     the stream while the job is still in that state, then opts.Resume.
func (c *Client) Follow(ctx context.Context, accessToken string, job *DryRunJobFull, opts FollowOptions) error {
	last := job.State
	stream := ws.NewClient(ws.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Path:        fmt.Sprintf("/api/orgs/%s/ws/dry-runs/%d", c.organizationID, job.ID),
		Verbosity:   c.verbosity,
		OnReconnect: opts.OnReconnect,
	})
	if err := stream.Connect(ctx); err != nil {
		if ctx.Err() != nil {
			return stillIn(ctx, job.ID, last)
		}
		return fmt.Errorf("failed to connect to websocket: %w", err)
	}
	defer stream.Close()

	msgs, errCh := stream.ReadMessages(ctx)
	for {
		select {
		case raw, ok := <-msgs:
			if !ok {
				if err := <-errCh; err != nil {
					return fmt.Errorf("websocket error: %w", err)
				}
				if ctx.Err() != nil {
					return stillIn(ctx, job.ID, last)
				}
				return c.afterClose(ctx, accessToken, job.ID, last, opts)
			}
			state, err := opts.OnFrame(raw)
			if err != nil {
				return err
			}
			if state == "" {
				continue
			}
			last = IngestionJobState(state)
			if IsTerminal(last) {
				return c.ended(ctx, accessToken, job.ID, last)
			}
		case <-ctx.Done():
			return stillIn(ctx, job.ID, last)
		}
	}
}

// ended is Follow's outcome for a terminal frame carrying state.
func (c *Client) ended(ctx context.Context, accessToken string, jobID int, state IngestionJobState) error {
	if state == api.IngestionJobStateFinished {
		return nil
	}
	job, err := c.GetDryRunJobContext(ctx, accessToken, jobID)
	if err != nil || ctx.Err() != nil {
		job = &DryRunJobFull{ID: jobID, State: state}
	} else if job.State != state {
		moved := *job
		moved.State = state
		job = &moved
	}
	return c.VerdictContext(ctx, accessToken, job)
}

// afterClose is Follow's outcome for a stream the server ended before a
// terminal frame, last being the last state seen.
func (c *Client) afterClose(ctx context.Context, accessToken string, jobID int, last IngestionJobState, opts FollowOptions) error {
	job, err := c.GetDryRunJobContext(ctx, accessToken, jobID)
	if ctx.Err() != nil && !qerrors.IsPermanent(err) {
		return stillIn(ctx, jobID, last)
	}
	if err != nil {
		return fmt.Errorf("the server closed the stream for dry-run-job %d, and reading the job failed: %w", jobID, err)
	}
	if job.State != last {
		if _, err := opts.OnFrame(StateFrame(job.State)); err != nil {
			return err
		}
	}
	if IsTerminal(job.State) {
		return c.VerdictContext(ctx, accessToken, job)
	}
	msg := fmt.Sprintf("the server closed the stream while dry-run-job %d is still %s", jobID, terminal.Sanitize(string(job.State)))
	if opts.Resume != "" {
		msg += "\n\nResume with: " + opts.Resume
	}
	return errors.New(msg)
}

// stillIn describes a job a wait gave up on while it was in state; its Unwrap
// is ctx's cause.
func stillIn(ctx context.Context, jobID int, state IngestionJobState) error {
	return qerrors.Caused(fmt.Sprintf("dry-run-job %d is still %s; the job was not stopped",
		jobID, terminal.Sanitize(string(state))), context.Cause(ctx))
}
