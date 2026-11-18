package cmdutil

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/apierror"
)

// stubSleep replaces the retry clock so the schedule costs no wall time, and
// records what the code would have waited.
func stubSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	throttleSleep = func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(ResetThrottleSleep)
	return &slept
}

func TestThrottleWaitDoublesAndCaps(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 30 * time.Second},
		{9, 30 * time.Second},
	}
	for _, c := range cases {
		if got := throttleWait(c.attempt); got != c.want {
			t.Errorf("throttleWait(%d) = %s, want %s", c.attempt, got, c.want)
		}
	}
}

// throttledErr builds the *apierror.ThrottleError RetryOnThrottle actually
// consumes, the same way the real HTTP path does (HandleHTTPErrorWithHeader),
// so the test exercises the real construction rather than a hand-built stub.
func throttledErr(t *testing.T, retryAfterSeconds string) error {
	t.Helper()
	header := http.Header{}
	if retryAfterSeconds != "" {
		header.Set("Retry-After", retryAfterSeconds)
	}
	return apierror.HandleHTTPErrorWithHeader(http.StatusTooManyRequests, []byte(`{"detail":"slow down"}`), header, "instantiate failed")
}

// RetryOnThrottle is exercised directly (not through the HTTP mock plumbing)
// so the two RetryAfter branches -- honoured and clamped -- are fast,
// deterministic unit tests rather than end-to-end ones.
func TestRetryOnThrottle_HonoursAnInRangeRetryAfter(t *testing.T) {
	slept := stubSleep(t)
	err := throttledErr(t, "5") // well under throttleMaxWait (30s)

	calls := 0
	got, gotErr := RetryOnThrottle(io.Discard, "dry runs", func() (int, error) {
		calls++
		if calls == 1 {
			return 0, err
		}
		return 42, nil
	})
	if gotErr != nil {
		t.Fatalf("expected the retry to succeed, got: %v", gotErr)
	}
	if got != 42 {
		t.Errorf("result = %d, want 42", got)
	}
	if len(*slept) != 1 || (*slept)[0] != 5*time.Second {
		t.Fatalf("expected a single 5s wait honouring Retry-After, got %v", *slept)
	}
}

// A Retry-After past throttleMaxWait must not be honoured verbatim: a mock
// returning "Retry-After: 86400" would otherwise produce hours of
// uninterruptible client-side sleep, bypassing throttleMaxWait entirely. It
// falls back to the computed backoff instead of pegging every wait to the cap.
func TestRetryOnThrottle_FallsBackToBackoffWhenRetryAfterExceedsTheCap(t *testing.T) {
	slept := stubSleep(t)
	err := throttledErr(t, "86400") // 24h -- far past throttleMaxWait

	calls := 0
	_, gotErr := RetryOnThrottle(io.Discard, "dry runs", func() (int, error) {
		calls++
		if calls == 1 {
			return 0, err
		}
		return 42, nil
	})
	if gotErr != nil {
		t.Fatalf("expected the retry to succeed, got: %v", gotErr)
	}
	if len(*slept) != 1 {
		t.Fatalf("expected exactly 1 wait, got %v", *slept)
	}
	if (*slept)[0] != throttleWait(1) {
		t.Errorf("wait = %s, want the computed backoff %s, not the raw Retry-After", (*slept)[0], throttleWait(1))
	}
	if (*slept)[0] >= 24*time.Hour {
		t.Fatalf("Retry-After leaked through uncapped: slept %s", (*slept)[0])
	}
}

// A non-429 is permanent: the loop must hand it straight back, unretried, with
// the caller's own error untouched.
func TestRetryOnThrottle_DoesNotRetryANon429(t *testing.T) {
	slept := stubSleep(t)
	permanent := apierror.HandleHTTPErrorWithHeader(http.StatusBadRequest, []byte(`{"detail":"column not found"}`), nil, "instantiate failed")

	calls := 0
	_, err := RetryOnThrottle(io.Discard, "dry runs", func() (int, error) {
		calls++
		return 0, permanent
	})
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a permanent failure, got %d", calls)
	}
	if len(*slept) != 0 {
		t.Fatalf("expected no waits, got %v", *slept)
	}
	if err != permanent {
		t.Errorf("error was not returned verbatim: %v", err)
	}
}
