package get

import (
	"context"
	"fmt"
	"io"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/stats"
)

// watchSnapshot is the comparable subset of a menu-stats response. Change
// detection is equality against the PREVIOUS RESPONSE FROM THIS ENDPOINT and
// nothing else: menu-stats and the alert list apply different server-side
// filters, so comparing a count here against a list length would report a
// change forever.
type watchSnapshot struct {
	alertID     int // 0 when the response carried none
	alertMsg    string
	alertType   string
	unresolved  int
	cantResolve int
	errors      int
}

func newWatchSnapshot(s *stats.MenuStats) watchSnapshot {
	snap := watchSnapshot{
		unresolved:  s.UnresolvedAlertsCount,
		cantResolve: s.AlertCountThatUserCantResolve,
		errors:      s.ErrorsCount,
		alertType:   alertTypeString(s.LatestAlertItemType),
	}
	if s.LatestAlertItemID != nil {
		snap.alertID = *s.LatestAlertItemID
	}
	if s.LatestAlertItemMsg != nil {
		snap.alertMsg = *s.LatestAlertItemMsg
	}
	return snap
}

// watchOptions is the caller's configuration.
type watchOptions struct {
	render       renderOptions
	interval     time.Duration
	exitOnChange bool
}

// watchDeps is everything the loop touches that is not pure computation, so it
// runs in milliseconds under test without cobra, sockets, or a real clock.
type watchDeps struct {
	fetch  func(context.Context) (*stats.MenuStats, error)
	out    io.Writer
	errOut io.Writer
	now    func() time.Time
}

// backoffFor grows the poll interval after consecutive failures: base, doubling
// each time, capped at 30s -- or at base itself when base is already above
// 30s, so a slow poll of a failing server never becomes faster than the
// healthy-server interval the user configured. Same shape as internal/ws's
// reconnect backoff.
func backoffFor(base time.Duration, failures int) time.Duration {
	ceiling := 30 * time.Second
	if base > ceiling {
		ceiling = base
	}
	wait := base
	for i := 0; i < failures; i++ {
		wait *= 2
		if wait >= ceiling {
			return ceiling
		}
	}
	return wait
}

// watchFatal returns err when it ends the watch (qerrors.IsPermanent), and nil
// when the next poll may succeed.
func watchFatal(err error) error {
	if qerrors.IsPermanent(err) {
		return err
	}
	return nil
}

// runWatchLoop polls deps.fetch and writes the baseline snapshot (unless
// opts.exitOnChange is set) and then each change; with opts.exitOnChange it
// returns nil after the first change. Once ctx is done it returns nil, or,
// with opts.exitOnChange, an error saying that nothing
// changed, whose Unwrap is ctx's cause.
func runWatchLoop(ctx context.Context, deps watchDeps, opts watchOptions) error {
	stopped := func() error {
		if opts.exitOnChange {
			return qerrors.Caused("no change to the latest alert on table "+opts.render.label(), context.Cause(ctx))
		}
		return nil
	}

	first, cancelled, err := fetchWatch(ctx, deps.fetch)
	if cancelled {
		return stopped()
	}
	if err != nil {
		return err // the very first poll failing is worth reporting, not retrying
	}
	previous := newWatchSnapshot(first)

	if err := renderWatchBaseline(deps, opts, first); err != nil {
		return err
	}

	failures := 0
	wait := opts.interval
	for {
		select {
		case <-ctx.Done():
			return stopped()
		case <-time.After(wait):
		}

		current, cancelled, err := fetchWatch(ctx, deps.fetch)
		if cancelled {
			return stopped()
		}
		if err != nil {
			if fatal := watchFatal(err); fatal != nil {
				return fatal
			}
			failures++
			wait = backoffFor(opts.interval, failures)
			fmt.Fprintf(deps.errOut, "poll failed (%v); retrying in %s\n", err, wait)
			continue
		}
		failures = 0
		wait = opts.interval

		snapshot := newWatchSnapshot(current)
		if snapshot == previous {
			continue
		}
		previous = snapshot

		if err := renderWatchEvent(deps, opts, current); err != nil {
			return err
		}
		if opts.exitOnChange {
			return nil
		}
	}
}

// renderWatchBaseline prints the initial state unless exitOnChange is set. The text view
// shows the full block so a human sees where they are; every structured format
// emits the same per-event record as the changes that follow, so a consumer
// parses one shape for the whole stream rather than an indented object first
// and NDJSON afterwards.
func renderWatchBaseline(deps watchDeps, opts watchOptions, s *stats.MenuStats) error {
	if opts.exitOnChange {
		return nil
	}
	if opts.render.format == "table" { //nolint:goconst // Keep output format handling local to this command.
		return renderLatestAlert(deps.out, opts.render, s)
	}
	return renderWatchEvent(deps, opts, s)
}

// renderWatchEvent writes one change. The text view is a timestamped line so a
// long-running watch reads as a log; structured formats emit one record per
// event so a consumer can read them incrementally.
func renderWatchEvent(deps watchDeps, opts watchOptions, s *stats.MenuStats) error {
	switch opts.render.format {
	case "json": //nolint:goconst // Keep output format handling local to this command.
		// NDJSON: one compact object per line, same contract as the
		// job-activity watcher.
		return writeCompactJSONLine(deps.out, buildLatestAlertPayload(opts.render, s))
	case "yaml": //nolint:goconst // Keep output format handling local to this command.
		fmt.Fprintln(deps.out, "---")
		return encodeStructuredTo(deps.out, "yaml", buildLatestAlertPayload(opts.render, s))
	case "name": //nolint:goconst // Keep output format handling local to this command.
		if s.LatestAlertItemID != nil {
			fmt.Fprintf(deps.out, "alert/%d\n", *s.LatestAlertItemID)
		}
		return nil
	default:
		stamp := deps.now().Format("15:04:05")
		if s.LatestAlertItemID == nil {
			fmt.Fprintf(deps.out, "[%s] no alert\n", stamp)
			fmt.Fprintf(deps.out, "           %d unresolved (%d you cannot resolve), %d errors\n",
				s.UnresolvedAlertsCount, s.AlertCountThatUserCantResolve, s.ErrorsCount)
			return nil
		}
		message := ""
		if s.LatestAlertItemMsg != nil {
			message = markdown.Inline(*s.LatestAlertItemMsg, markdown.InlineOptions{})
		}
		fmt.Fprintf(deps.out, "[%s] %d  %s  %s\n", stamp, *s.LatestAlertItemID,
			alertTypeString(s.LatestAlertItemType), message)
		fmt.Fprintf(deps.out, "           %d unresolved (%d you cannot resolve), %d errors\n",
			s.UnresolvedAlertsCount, s.AlertCountThatUserCantResolve, s.ErrorsCount)
		fmt.Fprintf(deps.out, "           -> qctl describe alert %d\n", *s.LatestAlertItemID)
		return nil
	}
}
