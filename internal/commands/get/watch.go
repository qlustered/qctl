package get

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// lwWatch is how a list command was asked to watch.
type lwWatch struct {
	intervalSeconds int
	// budget is how long the watch may run; 0 means no limit.
	budget time.Duration
	// next is the command line that resumes the watch.
	next string
}

// lwWatchFlags reads cmd's --watch and --timeout. ok is false when --watch is
// not set, and --timeout without --watch is a bad-usage error. watch.next is
// rendered from the flags set when lwWatchFlags is called.
func lwWatchFlags(cmd *cobra.Command, intervalSeconds int) (watch lwWatch, ok bool, err error) {
	if on, _ := cmd.Flags().GetBool("watch"); !on {
		if cmd.Flags().Changed("timeout") {
			return lwWatch{}, false, qerrors.NewBadUsage("--timeout requires --watch")
		}
		return lwWatch{}, false, nil
	}
	budget, err := cmdutil.WatchBudget(cmd)
	if err != nil {
		return lwWatch{}, false, err
	}
	return lwWatch{
		intervalSeconds: intervalSeconds,
		budget:          budget,
		next:            cmdutil.ResumeCommand(cmd),
	}, true, nil
}

// watchList runs watchPoll every watch.intervalSeconds, writing to a Stream of
// printer that skips unchanged snapshots, as a cmdutil.WatchStream wait
// bounded by watch.budget and resumed by watch.next. noun names what is
// watched in the wait's banner.
func watchList(cmd *cobra.Command, printer *output.Printer, noun string, watch lwWatch, fetch func(context.Context) (any, error)) error {
	// time.NewTicker panics on a non-positive duration. Every caller validates
	// its own flag first — that check is what produces the exit-2 usage error
	// naming --watch-interval or --interval — so reaching this line means a
	// caller skipped it: a programming error, not something the user typed.
	// Guarding here costs one line and makes the panic unreachable for the next
	// --watch command someone adds.
	if watch.intervalSeconds < 1 {
		return fmt.Errorf("internal error: watch interval must be at least 1 second, got %d", watch.intervalSeconds)
	}
	interval := time.Duration(watch.intervalSeconds) * time.Second

	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:   cmdutil.WatchStream,
		Budget: watch.budget,
		Doing:  fmt.Sprintf("watching %s every %s", noun, interval),
		Next:   watch.next,
	})
	defer w.Stop()

	stream := printer.NewStream(isTerminal(cmd.OutOrStdout()), true)
	return w.Ended(watchPoll(ctx, stream, cmd.ErrOrStderr(), interval, fetch))
}

// watchPoll writes fetch's snapshot to stream, then again every interval until
// ctx is done, when it returns nil. An error from the first fetch or from
// stream is returned, as is a later fetch error qerrors.IsPermanent reports.
// Any other fetch error is written to errOut and the poll retried after
// backoffFor's wait; a successful fetch restores interval.
func watchPoll(ctx context.Context, stream *output.Stream, errOut io.Writer, interval time.Duration, fetch func(context.Context) (any, error)) error {
	if interval <= 0 {
		return fmt.Errorf("internal error: watch interval must be positive, got %s", interval)
	}
	errOut = stream.NoticeWriter(errOut)
	ctx = client.WithDiagnosticWriter(ctx, errOut)

	snapshot, cancelled, err := fetchWatch(ctx, fetch)
	if cancelled {
		return nil
	}
	if err != nil {
		return err
	}
	if err := stream.Write(snapshot); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		snapshot, cancelled, err := fetchWatch(ctx, fetch)
		if cancelled {
			return nil
		}
		if err != nil {
			if fatal := watchFatal(err); fatal != nil {
				return fatal
			}
			failures++
			wait := backoffFor(interval, failures)
			fmt.Fprintln(errOut, terminal.Sanitize(fmt.Sprintf("poll failed (%v); retrying in %s", err, wait)))
			ticker.Reset(wait)
			continue
		}
		if failures > 0 {
			failures = 0
			ticker.Reset(interval)
		}
		if err := stream.Write(snapshot); err != nil {
			return err
		}
	}
}

// fetchWatch reports cancellation unless the poll returned a permanent error.
func fetchWatch[T any](ctx context.Context, fetch func(context.Context) (T, error)) (T, bool, error) {
	var zero T
	if ctx.Err() != nil {
		return zero, true, nil
	}
	snapshot, err := fetch(ctx)
	if ctx.Err() != nil && !qerrors.IsPermanent(err) {
		return zero, true, nil
	}
	return snapshot, false, err
}
