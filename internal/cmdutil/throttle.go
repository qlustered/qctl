package cmdutil

import (
	stderrors "errors"
	"fmt"
	"io"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
)

const (
	// ThrottleMaxAttempts counts the first try, so this is 1 try + 4 retries.
	// With the schedule below that is 4 waits of 2s+4s+8s+16s = 30s total
	// (16s the longest single wait) before the call is reported as failed.
	ThrottleMaxAttempts = 5
	throttleBaseWait    = 2 * time.Second
	// throttleMaxWait caps a single wait. The backoff schedule above never
	// reaches it on its own (it tops out at 16s); its other job is clamping
	// an out-of-range server Retry-After (see RetryOnThrottle) so a
	// misbehaving proxy cannot make the client sleep uninterruptibly for
	// hours.
	throttleMaxWait = 30 * time.Second
)

// throttleSleep is time.Sleep, replaced in tests so the retry schedule costs no
// wall clock. Same shape as SetReader for the prompt.
var throttleSleep = time.Sleep

// SetThrottleSleep replaces the retry clock (tests only).
func SetThrottleSleep(f func(time.Duration)) { throttleSleep = f }

// ResetThrottleSleep restores the real clock.
func ResetThrottleSleep() { throttleSleep = time.Sleep }

// throttleWait is the pause before retry number `attempt` (1-based): the base
// wait doubling each time, capped. Same shape as internal/ws's reconnect
// backoff and internal/commands/get's watch backoff.
func throttleWait(attempt int) time.Duration {
	wait := throttleBaseWait
	for i := 1; i < attempt; i++ {
		wait *= 2
		if wait >= throttleMaxWait {
			return throttleMaxWait
		}
	}
	return wait
}

// RetryOnThrottle runs call, retrying while the server answers 429.
//
// The condition is the *status*, never the message or its error code.
//
// create dry-run-job -> LaunchDryRunJob, create composite-dry-run-job ->
// composite_dry_runs.Launch, and apply -f <linked-table> ->
// linked_tables.Apply all spend the exploratory concurrency cap
// (MAX_CONCURRENT_DRY_RUNS_PER_USER = 3, atlas
// cettings/cettings/controllers/dry_run_helpers.py). It counts only
// EXECUTING runs (see check_dry_run_rate_limit in dry_run_helpers.py, "Counts
// only executing states; queued work is free"), is scoped per user per
// organization, and answers DRY_RUN_RATE_LIMIT. Enforced in
// create_and_run_dry_run (capi/capi/controllers/dry_run_controller.py) and
// create_and_run_composite_dry_run
// (capi/capi/controllers/composite_dry_run_controller.py). A refused request
// is rolled back whole before the answer (get_session_for_fastapi,
// cettings/cettings/db.py), which is why retrying the identical request is
// safe.
//
// apply -f <table-rule> -> InstantiateRule is wrapped as well, although atlas
// declares no 429 on that route.
//
// what names the subject of the progress line ("dry runs", say) so this
// generic helper never hardcodes one caller's noun; every caller passes
// "dry runs", which keeps the rendered line identical to before this
// parameter existed.
//
// Contract on out: this prints one progress line per retry directly to out,
// with no -o json handling of its own — choosing a JSON-safe writer is the
// caller's job. A caller whose stdout is a machine-readable document must pass
// cmd.ErrOrStderr(), as create dry-run-job (its stdout is the launch response
// as JSON), create composite-dry-run-job (under -o json, yaml
// or name its stdout is machine-readable) and apply's applyLinkedTable do (under -o json
// its stdout is the backend's apply body). apply's applyTableRuleInstantiate passes
// cmd.OutOrStdout() unconditionally, which is safe for a single-document
// apply or a non-json multi-document run; under `apply -f multi.yaml -o
// json`, though, the TableRule handler's prose (this line included) still
// lands on stdout alongside JSON emitted by other handlers in the same
// stream (see internal/commands/apply/table_rules.go and
// internal/commands/apply/stream.go) — a pre-existing interleaving risk this
// helper does not fix.
func RetryOnThrottle[T any](out io.Writer, what string, call func() (T, error)) (T, error) {
	var zero T
	for attempt := 1; ; attempt++ {
		result, err := call()
		if err == nil {
			return result, nil
		}
		var throttled *apierror.ThrottleError
		if !stderrors.As(err, &throttled) || attempt >= ThrottleMaxAttempts {
			return zero, err
		}
		// A positive, in-range Retry-After is honoured verbatim; anything else
		// (absent, zero, negative, or past throttleMaxWait) falls back to the
		// computed backoff rather than clamping to the flat cap, so an
		// out-of-range value still gets the doubling schedule's shape instead
		// of a uniform 30s wait on every attempt. Without this, a server (or
		// an intermediary CDN/gateway) sending a large Retry-After -- e.g.
		// 86400 -- would make the client sleep uninterruptibly for as long as
		// the server says, unbounded by throttleMaxWait. --request-timeout
		// does not save anyone here and never will: it bounds one HTTP
		// attempt, and this sleep happens between attempts, above the HTTP
		// client entirely. Same clamp, same reason, in client.retryDelay.
		wait := throttled.RetryAfter
		if wait <= 0 || wait > throttleMaxWait {
			wait = throttleWait(attempt)
		}
		fmt.Fprintf(out, "  server is throttling %s; retrying in %s (attempt %d of %d)\n",
			what, wait, attempt+1, ThrottleMaxAttempts)
		throttleSleep(wait)
	}
}
