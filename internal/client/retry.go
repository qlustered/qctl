package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
)

const (
	// retryBaseWait is the pause before the first retry, doubling from there.
	// It is shorter than cmdutil's throttle backoff on purpose: that one waits
	// out a server-side budget the client cannot influence, while this one is
	// mostly repeating a request that never got an answer.
	retryBaseWait = 1 * time.Second
	// retryMaxWait caps a single pause, and also clamps a server Retry-After:
	// an intermediary answering 429 with `Retry-After: 86400` must not be able
	// to park the CLI for a day. Same value, same reason, as
	// cmdutil.throttleMaxWait.
	retryMaxWait = 30 * time.Second
)

// defaultRetryWait sleeps for d, or returns early if the caller gives up
// first. It is context-aware so that Ctrl-C during a long backoff ends the
// command instead of being noticed a backoff later.
func defaultRetryWait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retryWait performs the pause before a retry. Tests replace it so the
// schedule costs no wall clock -- same shape as cmdutil.SetThrottleSleep.
var retryWait = defaultRetryWait

// SetRetryWait replaces the retry clock (tests only).
func SetRetryWait(f func(context.Context, time.Duration) error) { retryWait = f }

// ResetRetryWait restores the real clock.
func ResetRetryWait() { retryWait = defaultRetryWait }

// retryOutput is where the one-line-per-retry notice goes. Stderr, so a
// command whose stdout is a JSON document stays parseable.
var retryOutput io.Writer = os.Stderr

// SetRetryOutput redirects the retry notice (tests only).
func SetRetryOutput(w io.Writer) { retryOutput = w }

// ResetRetryOutput restores stderr.
func ResetRetryOutput() { retryOutput = os.Stderr }

// retryDoer repeats a failed request up to --retries times.
//
// What it repeats is deliberately narrow: only the SAFE methods -- GET, HEAD,
// OPTIONS -- which are the ones defined to have no effect on the server, so a
// second attempt cannot duplicate work no matter where the first one died.
// POST, PATCH, PUT and DELETE are never repeated here. That is not caution for
// its own sake: `qctl alert resolve --action upgrade_rule_revision` creates a
// replacement rule and answers RULE_UPGRADE_REPLACEMENT_EXISTS to a second
// send (see alerts.NextActions, which tells the reader exactly that), and an
// automatic retry of a POST whose response was lost in transit would be
// indistinguishable from the user re-running it.
//
// A 429 on a non-safe request is still retried, just not here:
// cmdutil.RetryOnThrottle wraps the three dry-run launch calls, which are
// POSTs, and is safe there only because the backend rolls those requests back
// whole before answering. The two mechanisms cannot double up, because their
// method sets do not overlap.
type retryDoer struct {
	inner   Doer
	retries int
}

func (d *retryDoer) Do(req *http.Request) (*http.Response, error) {
	// req.Body != nil is belt and braces rather than a real case -- the
	// generated client sends no body on a safe method, and http.NewRequest
	// leaves Body nil when given one -- but http.Client.Do CLOSES the body it
	// was given, so a second attempt with the same request would send an
	// already-spent reader and fail for a reason that has nothing to do with
	// the first failure.
	if d.retries <= 0 || !isSafeMethod(req.Method) || req.Body != nil {
		return d.inner.Do(req)
	}

	for attempt := 0; ; attempt++ {
		resp, err := d.inner.Do(req)
		if attempt >= d.retries {
			return resp, err
		}
		// A cancelled or expired caller context is the user's decision, not a
		// transient failure; repeating it would only fail again, later.
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return resp, err
		}
		wait, why, again := retryDelay(resp, err, attempt)
		if !again {
			return resp, err
		}
		// The response is being thrown away, so drain it: leaving an
		// unread body behind is what stops the connection going back to
		// the keep-alive pool, which matters most on exactly the flaky
		// link that made us retry.
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		fmt.Fprintf(DiagnosticWriter(req.Context(), retryOutput), "  %s; retrying in %s (attempt %d of %d)\n",
			why, wait, attempt+2, d.retries+1)
		if waitErr := retryWait(req.Context(), wait); waitErr != nil {
			return nil, waitErr
		}
	}
}

// isSafeMethod reports whether repeating the method is defined to be without
// effect on the server (RFC 9110 §9.2.1). An empty method is GET, as
// http.Request documents.
func isSafeMethod(method string) bool {
	switch method {
	case "", http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// retryDelay decides whether one attempt's outcome is worth repeating, how
// long to wait first, and what to tell the user it was.
//
// The retryable set is the one where a second attempt can plausibly land
// somewhere else: a request that got no answer at all, the server saying it is
// over budget (429), and the three gateway statuses that mean an intermediary
// could not reach a healthy backend. A plain 500 is NOT in it -- that is the
// application answering, and answering the same way next time.
func retryDelay(resp *http.Response, err error, attempt int) (wait time.Duration, why string, again bool) {
	backoff := retryBackoff(attempt)
	if err != nil {
		return backoff, fmt.Sprintf("request failed (%v)", err), true
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		// A positive, in-range Retry-After is honoured verbatim; anything else
		// (absent, junk, an HTTP-date, or past the cap) falls back to the
		// computed backoff rather than clamping flat, so an out-of-range value
		// still gets the doubling schedule's shape. Same rule as
		// cmdutil.RetryOnThrottle, which is the other half of this behaviour.
		if after := apierror.ParseRetryAfter(resp.Header); after > 0 && after <= retryMaxWait {
			return after, "server is throttling this request", true
		}
		return backoff, "server is throttling this request", true
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return backoff, fmt.Sprintf("server answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)), true
	}
	return 0, "", false
}

// retryBackoff is the pause before retry number attempt (0-based): the base
// wait doubling each time, capped. Same shape as cmdutil.throttleWait, and as
// internal/ws's reconnect backoff.
func retryBackoff(attempt int) time.Duration {
	wait := retryBaseWait
	for i := 0; i < attempt; i++ {
		wait *= 2
		if wait >= retryMaxWait {
			return retryMaxWait
		}
	}
	return wait
}
