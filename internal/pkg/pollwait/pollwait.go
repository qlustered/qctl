// Package pollwait is the poll loop behind the --wait flags.
package pollwait

import (
	"context"
	"errors"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ErrStopped is what Run returns when its parent context is cancelled.
var ErrStopped = errors.New("wait stopped")

// Options configures Run.
type Options[T any] struct {
	// Timeout bounds the whole wait.
	Timeout time.Duration
	// Now and Sleep are the polling clock. Sleep advances what Now returns.
	Now   func() time.Time
	Sleep func(time.Duration)
	// Settled reports whether a polled value ends the wait.
	Settled func(T) bool
	// Changed reports whether next differs from prev in a way Progress
	// reports. Nil means every poll differs.
	Changed func(prev, next T) bool
	// Progress, when set, receives the first unsettled value, and each later
	// one Changed says differs from the previous one it received.
	Progress func(T)
}

// Expired is what Run reports when the wait reached its timeout.
type Expired[T any] struct {
	// Last is the latest value a poll returned, the zero value when none did.
	Last T
	// LastErr is the latest poll's error, nil when that poll succeeded.
	LastErr error
}

// Run calls poll until the value it returns is Settled and returns that value.
// A poll that fails with a permanent error (qerrors.IsPermanent) ends the wait
// with that error; any other failed poll is retried. A wait that reaches
// opts.Timeout returns a non-nil *Expired.
//
// Each poll receives a context bounded by opts.Timeout and parent.
func Run[T any](parent context.Context, poll func(context.Context) (T, error), opts Options[T]) (T, *Expired[T], error) {
	var zero T
	start := opts.Now()
	deadline := start.Add(opts.Timeout)
	ctx, cancel := context.WithTimeout(parent, opts.Timeout)
	defer cancel()
	var last T
	seen := false
	var lastErr error
	for {
		if parent.Err() != nil {
			return zero, nil, ErrStopped
		}
		if ctx.Err() != nil {
			return zero, &Expired[T]{Last: last, LastErr: lastErr}, nil
		}
		value, err := poll(ctx)
		if err != nil && qerrors.IsPermanent(err) {
			return zero, nil, err
		}
		if parent.Err() != nil {
			return zero, nil, ErrStopped
		}
		if ctx.Err() != nil {
			return zero, &Expired[T]{Last: last, LastErr: lastErr}, nil
		}
		switch {
		case err != nil:
			lastErr = err
		default:
			lastErr = nil
			if opts.Settled(value) {
				return value, nil, nil
			}
			if opts.Progress != nil && (!seen || opts.Changed == nil || opts.Changed(last, value)) {
				opts.Progress(value)
			}
			last, seen = value, true
		}

		now := opts.Now()
		remaining := deadline.Sub(now)
		if remaining <= 0 {
			return zero, &Expired[T]{Last: last, LastErr: lastErr}, nil
		}
		sleepUnlessDone(opts.Sleep, min(Interval(now.Sub(start)), remaining), ctx.Done())
	}
}

// sleepUnlessDone calls sleep(d) and returns when that call does or when done
// closes, whichever comes first.
func sleepUnlessDone(sleep func(time.Duration), d time.Duration, done <-chan struct{}) {
	if done == nil {
		sleep(d)
		return
	}
	slept := make(chan struct{})
	go func() {
		defer close(slept)
		sleep(d)
	}()
	select {
	case <-slept:
	case <-done:
	}
}

// Interval is the pause before the next poll, elapsed into the wait: 2s for
// the first 30s, then 5s.
func Interval(elapsed time.Duration) time.Duration {
	if elapsed < 30*time.Second {
		return 2 * time.Second
	}
	return 5 * time.Second
}
