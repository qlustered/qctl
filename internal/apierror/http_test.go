package apierror

import (
	stderrors "errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// A 429 whose detail conforms to nothing qctl knows -- an older server's bare
// string, or a gateway's own page -- must still be a ThrottleError. Detection
// keys off the status alone, which is why the backend's move to an
// object-shaped detail with an error_code changed nothing here.
func TestThrottleErrorIsDetectableFromStatusAlone(t *testing.T) {
	body := []byte(`{"detail":"You have 3 dry run(s) in progress (limit: 3)."}`)
	err := HandleHTTPErrorWithHeader(http.StatusTooManyRequests, body, nil, "instantiate failed")

	var throttled *ThrottleError
	if !stderrors.As(err, &throttled) {
		t.Fatalf("expected a *ThrottleError for 429, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 0 {
		t.Errorf("RetryAfter = %s, want 0 when the header is absent", throttled.RetryAfter)
	}
}

func TestThrottleErrorReadsRetryAfterSeconds(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "12")
	err := HandleHTTPErrorWithHeader(http.StatusTooManyRequests, []byte(`{"detail":"slow down"}`), header, "instantiate failed")

	var throttled *ThrottleError
	if !stderrors.As(err, &throttled) {
		t.Fatalf("expected a *ThrottleError, got %T", err)
	}
	if throttled.RetryAfter != 12*time.Second {
		t.Errorf("RetryAfter = %s, want 12s", throttled.RetryAfter)
	}
}

// A throttle is transient: retrying the identical command fixes it, so it is
// not "bad CLI usage".
func TestThrottleErrorExitsGeneric(t *testing.T) {
	err := HandleHTTPErrorWithHeader(http.StatusTooManyRequests, []byte(`{"detail":"slow down"}`), nil, "instantiate failed")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}

// HandleHTTPError is the *http.Response entry point (zero callers today, but
// it is the one place that actually has resp.Header) -- it must not drop the
// header before delegating, or a 429 that reaches it can never honour
// Retry-After.
func TestHandleHTTPErrorForwardsHeader(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "7")
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(`{"detail":"slow down"}`)),
	}
	err := HandleHTTPError(resp, "instantiate failed")

	var throttled *ThrottleError
	if !stderrors.As(err, &throttled) {
		t.Fatalf("expected a *ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %s, want 7s -- HandleHTTPError must forward resp.Header", throttled.RetryAfter)
	}
}

// ThrottleError is exported, so nothing stops a caller outside this package
// from building one directly with no err set. Error() must not panic on that.
func TestThrottleErrorDoesNotPanicWithNilErr(t *testing.T) {
	e := &ThrottleError{RetryAfter: 5 * time.Second}
	if msg := e.Error(); msg == "" {
		t.Error("expected a non-empty message even with a nil err")
	}
}

func TestNonThrottleStatusesAreUnchanged(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusBadRequest, qerrors.ExitBadUsage},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	}
	for _, c := range cases {
		err := HandleHTTPErrorFromBytes(c.status, []byte(`{"detail":"nope"}`), "ctx")
		var throttled *ThrottleError
		if stderrors.As(err, &throttled) {
			t.Errorf("status %d must not be a ThrottleError", c.status)
		}
		if got := qerrors.GetExitCode(err); got != c.want {
			t.Errorf("status %d exit code = %d, want %d", c.status, got, c.want)
		}
	}
}

// objectShaped429 is what ../atlas actually sends on every throttled dry-run
// route: dry_run_limit_api_error (cettings/cettings/controllers/dry_run_helpers.py)
// builds an APIError whose detail is an object carrying error_code and a
// context map, not the bare string these fixtures used to assume.
const objectShaped429 = `{"detail":{"msg":"You have 3 dry run(s) in progress (limit: 3). ` +
	`Please wait for an existing dry run to complete before starting a new one.",` +
	`"error_code":"DRY_RUN_RATE_LIMIT","context":{"current_count":3,"max_allowed":3,` +
	`"limit_kind":"concurrent_execution","retry_after_seconds":15}}}`

// The backend puts the same number in the Retry-After header and in
// context.retry_after_seconds. An intermediary can forward the body while
// dropping the header; the frontend already reads the body in that case
// (cosmosview src/common/dryRunRateLimit.ts), so qctl does too.
func TestThrottleErrorFallsBackToContextRetryAfter(t *testing.T) {
	err := HandleHTTPErrorWithHeader(http.StatusTooManyRequests, []byte(objectShaped429), nil, "failed to launch dry-run job")

	var throttled *ThrottleError
	if !stderrors.As(err, &throttled) {
		t.Fatalf("expected a *ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 15*time.Second {
		t.Errorf("RetryAfter = %s, want 15s from context.retry_after_seconds", throttled.RetryAfter)
	}
}

// The header is the HTTP-standard channel and wins whenever it is usable, so a
// proxy that rewrites the delay is honoured over a stale body copy.
func TestThrottleErrorPrefersTheHeaderOverContext(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "7")
	err := HandleHTTPErrorWithHeader(http.StatusTooManyRequests, []byte(objectShaped429), header, "failed to launch dry-run job")

	var throttled *ThrottleError
	if !stderrors.As(err, &throttled) {
		t.Fatalf("expected a *ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %s, want 7s: the header outranks context.retry_after_seconds", throttled.RetryAfter)
	}
}

// The header only outranks the body when it is *usable*. ParseRetryAfter reads
// the delay-seconds form alone -- the HTTP-date form is deliberately not parsed
// -- so a present-but-unparseable header is "no advice" and the body's copy
// still wins. This is the fall-through edge: a response that did carry a header
// taking the body's value anyway.
func TestThrottleErrorFallsThroughAnUnparseableHeaderToContext(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "Wed, 21 Oct 2015 07:28:00 GMT")
	err := HandleHTTPErrorWithHeader(http.StatusTooManyRequests, []byte(objectShaped429), header, "failed to launch dry-run job")

	var throttled *ThrottleError
	if !stderrors.As(err, &throttled) {
		t.Fatalf("expected a *ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 15*time.Second {
		t.Errorf("RetryAfter = %s, want 15s: an HTTP-date header is unparseable, so context.retry_after_seconds stands", throttled.RetryAfter)
	}
}

// Anything that is not a positive integer reads as "no advice", so
// RetryOnThrottle falls back to its doubling schedule instead of hot-looping
// on a 0 or trusting a negative.
func TestThrottleErrorIgnoresANonPositiveContextRetryAfter(t *testing.T) {
	for _, body := range []string{
		`{"detail":{"msg":"slow down","context":{"retry_after_seconds":0}}}`,
		`{"detail":{"msg":"slow down","context":{"retry_after_seconds":-5}}}`,
		`{"detail":{"msg":"slow down","context":{"retry_after_seconds":"15"}}}`,
		`{"detail":{"msg":"slow down","context":{}}}`,
		`{"detail":"You have 3 dry run(s) in progress (limit: 3)."}`,
		`<html><body>429 Too Many Requests</body></html>`,
	} {
		err := HandleHTTPErrorWithHeader(http.StatusTooManyRequests, []byte(body), nil, "failed to launch dry-run job")
		var throttled *ThrottleError
		if !stderrors.As(err, &throttled) {
			t.Fatalf("expected a *ThrottleError for %s, got %T: %v", body, err, err)
		}
		if throttled.RetryAfter != 0 {
			t.Errorf("RetryAfter = %s for %s, want 0", throttled.RetryAfter, body)
		}
	}
}

// The object-shaped body carries an error_code, so the rendered message names
// it. This is the half of the wire contract the string-shaped fixtures could
// never exercise.
func TestObjectShaped429RendersTheErrorCode(t *testing.T) {
	err := HandleHTTPErrorWithHeader(http.StatusTooManyRequests, []byte(objectShaped429), nil, "failed to launch dry-run job")
	if !strings.Contains(err.Error(), "DRY_RUN_RATE_LIMIT") {
		t.Errorf("message does not name the error code:\n%s", err.Error())
	}
	if !strings.Contains(err.Error(), "You have 3 dry run(s) in progress") {
		t.Errorf("message dropped the backend's own sentence:\n%s", err.Error())
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d: a throttle is not bad usage", got, qerrors.ExitGenericError)
	}
}
