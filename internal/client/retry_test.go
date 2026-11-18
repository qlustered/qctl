package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// countingBody records whether the loser of a retry had its body drained and
// closed. Leaving one unread is what stops the connection returning to the
// keep-alive pool, on exactly the flaky link that made us retry.
//
// The reader is a field rather than an embedded *strings.Reader on purpose:
// embedding promotes strings.Reader.WriteTo, and io.Copy prefers WriteTo over
// Read, so an embedded double would report read=false for a body that was in
// fact fully drained.
type countingBody struct {
	reader *strings.Reader
	closed bool
	read   bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	b.read = true
	return b.reader.Read(p)
}

func (b *countingBody) Close() error {
	b.closed = true
	return nil
}

// scriptedDoer answers each call with the next entry of its script, repeating
// the last one once exhausted, and records what it was asked.
type scriptedDoer struct {
	mu       sync.Mutex
	script   []func() (*http.Response, error)
	calls    int
	bodies   []*countingBody
	requests []*http.Request
}

func (d *scriptedDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	i := d.calls
	if i >= len(d.script) {
		i = len(d.script) - 1
	}
	d.calls++
	d.requests = append(d.requests, req)
	step := d.script[i]
	// Released before the step runs: respond's closure takes this same lock
	// to record the body it mints.
	d.mu.Unlock()
	return step()
}

// respond builds a scripted response whose body is observable.
func (d *scriptedDoer) respond(status int, header http.Header) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		body := &countingBody{reader: strings.NewReader("payload")}
		d.mu.Lock()
		d.bodies = append(d.bodies, body)
		d.mu.Unlock()
		if header == nil {
			header = http.Header{}
		}
		return &http.Response{StatusCode: status, Header: header, Body: body}, nil
	}
}

func failWith(err error) func() (*http.Response, error) {
	return func() (*http.Response, error) { return nil, err }
}

// recordWaits swaps the retry clock for one that costs no wall time and
// records the schedule, and redirects the retry notice away from stderr.
// Returns a pointer to the recorded waits and to the notice.
func recordWaits(t *testing.T) (*[]time.Duration, *bytes.Buffer) {
	t.Helper()
	var waits []time.Duration
	SetRetryWait(func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	})
	notice := &bytes.Buffer{}
	SetRetryOutput(notice)
	t.Cleanup(func() {
		ResetRetryWait()
		ResetRetryOutput()
	})
	return &waits, notice
}

func newRequest(t *testing.T, method string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "https://api.example.com/api/datasets", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return req
}

// TestRetryRepeatsOnlySafeMethods is the load-bearing one. `qctl alert resolve
// --action upgrade_rule_revision` creates a replacement rule and answers
// RULE_UPGRADE_REPLACEMENT_EXISTS to a second send, so a POST repeated behind
// the user's back is a defect, not a convenience.
//
// The GET row is the harness-alive control: without it, every "not retried"
// row would also pass against a retryDoer that never retried anything.
func TestRetryRepeatsOnlySafeMethods(t *testing.T) {
	tests := []struct {
		method   string
		attempts int
	}{
		{http.MethodGet, 3},
		{"", 3}, // http.Request documents the empty method as GET
		{http.MethodHead, 3},
		{http.MethodOptions, 3},
		{http.MethodPost, 1},
		{http.MethodPut, 1},
		{http.MethodPatch, 1},
		{http.MethodDelete, 1},
	}

	for _, tt := range tests {
		name := tt.method
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			recordWaits(t)
			inner := &scriptedDoer{}
			inner.script = []func() (*http.Response, error){inner.respond(http.StatusServiceUnavailable, nil)}
			d := &retryDoer{inner: inner, retries: 2}

			resp, err := d.Do(newRequest(t, tt.method))
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			resp.Body.Close()

			if inner.calls != tt.attempts {
				t.Errorf("%s made %d attempts, want %d", name, inner.calls, tt.attempts)
			}
		})
	}
}

// TestRetryDoesNotRepeatARequestCarryingABody. http.Client.Do closes the body
// it was given, so a second attempt with the same request would send an
// already-spent reader and fail for a reason unrelated to the first failure.
// The generated client never sends a body on a safe method, which is what
// makes refusing outright the right answer rather than a rewind.
func TestRetryDoesNotRepeatARequestCarryingABody(t *testing.T) {
	recordWaits(t)
	inner := &scriptedDoer{}
	inner.script = []func() (*http.Response, error){inner.respond(http.StatusServiceUnavailable, nil)}
	d := &retryDoer{inner: inner, retries: 2}

	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/api/datasets", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := d.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	resp.Body.Close()

	if inner.calls != 1 {
		t.Errorf("made %d attempts for a GET with a body, want 1", inner.calls)
	}
}

func TestRetryRepeatsOnlyTheTransientOutcomes(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		attempts int
	}{
		{"success is not retried", http.StatusOK, 1},
		{"not found is the server's answer, not a hiccup", http.StatusNotFound, 1},
		{"a validation failure is deterministic", http.StatusUnprocessableEntity, 1},
		// A plain 500 is the application answering, and it answers the same
		// way next time. Retrying it would turn one stack trace into four.
		{"a plain 500 is not retried", http.StatusInternalServerError, 1},
		{"429 is retried", http.StatusTooManyRequests, 3},
		{"502 is retried", http.StatusBadGateway, 3},
		{"503 is retried", http.StatusServiceUnavailable, 3},
		{"504 is retried", http.StatusGatewayTimeout, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recordWaits(t)
			inner := &scriptedDoer{}
			inner.script = []func() (*http.Response, error){inner.respond(tt.status, nil)}
			d := &retryDoer{inner: inner, retries: 2}

			resp, err := d.Do(newRequest(t, http.MethodGet))
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			resp.Body.Close()

			if inner.calls != tt.attempts {
				t.Errorf("%d made %d attempts, want %d", tt.status, inner.calls, tt.attempts)
			}
		})
	}
}

func TestRetryRepeatsATransportFailure(t *testing.T) {
	recordWaits(t)
	inner := &scriptedDoer{script: []func() (*http.Response, error){
		failWith(errors.New("connection reset by peer")),
	}}
	d := &retryDoer{inner: inner, retries: 2}

	if _, err := d.Do(newRequest(t, http.MethodGet)); err == nil {
		t.Fatal("Do() error = nil, want the transport failure")
	}
	if inner.calls != 3 {
		t.Errorf("made %d attempts, want 3", inner.calls)
	}
}

func TestRetryReturnsTheFirstSuccessAndStops(t *testing.T) {
	recordWaits(t)
	inner := &scriptedDoer{}
	inner.script = []func() (*http.Response, error){
		inner.respond(http.StatusServiceUnavailable, nil),
		inner.respond(http.StatusOK, nil),
		inner.respond(http.StatusServiceUnavailable, nil),
	}
	d := &retryDoer{inner: inner, retries: 5}

	resp, err := d.Do(newRequest(t, http.MethodGet))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if inner.calls != 2 {
		t.Errorf("made %d attempts, want 2 -- it kept going after a success", inner.calls)
	}
}

// TestRetryStopsAtTheConfiguredCount: --retries counts retries, not attempts,
// which is what the flag's help says.
func TestRetryStopsAtTheConfiguredCount(t *testing.T) {
	for retries := 0; retries <= 4; retries++ {
		recordWaits(t)
		inner := &scriptedDoer{}
		inner.script = []func() (*http.Response, error){inner.respond(http.StatusBadGateway, nil)}
		d := &retryDoer{inner: inner, retries: retries}

		resp, err := d.Do(newRequest(t, http.MethodGet))
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		resp.Body.Close()

		if want := retries + 1; inner.calls != want {
			t.Errorf("--retries %d made %d attempts, want %d", retries, inner.calls, want)
		}
	}
}

func TestRetryDrainsAndClosesTheDiscardedResponses(t *testing.T) {
	recordWaits(t)
	inner := &scriptedDoer{}
	inner.script = []func() (*http.Response, error){inner.respond(http.StatusServiceUnavailable, nil)}
	d := &retryDoer{inner: inner, retries: 2}

	resp, err := d.Do(newRequest(t, http.MethodGet))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if len(inner.bodies) != 3 {
		t.Fatalf("got %d bodies, want 3", len(inner.bodies))
	}
	for i, b := range inner.bodies[:2] {
		if !b.read || !b.closed {
			t.Errorf("discarded body %d: read=%v closed=%v, want both true", i, b.read, b.closed)
		}
	}
	// The one handed back is the caller's to close, not ours.
	if last := inner.bodies[2]; last.closed {
		t.Error("the returned response's body was closed before the caller saw it")
	}
}

func TestRetryHonoursRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"a usable delay is honoured verbatim", "15", 15 * time.Second},
		// Anything unusable falls back to the computed backoff rather than
		// clamping flat, so an out-of-range value still gets the doubling
		// schedule's shape. Same rule as cmdutil.RetryOnThrottle.
		{"an absent header falls back to the backoff", "", retryBaseWait},
		{"junk falls back to the backoff", "in a bit", retryBaseWait},
		{"an HTTP-date falls back to the backoff", "Wed, 21 Oct 2026 07:28:00 GMT", retryBaseWait},
		{"zero falls back to the backoff", "0", retryBaseWait},
		{"a negative falls back to the backoff", "-5", retryBaseWait},
		// An intermediary must not be able to park the CLI for a day.
		{"past the cap falls back to the backoff", "86400", retryBaseWait},
		{"exactly the cap is still honoured", "30", retryMaxWait},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			waits, _ := recordWaits(t)
			header := http.Header{}
			if tt.header != "" {
				header.Set("Retry-After", tt.header)
			}
			inner := &scriptedDoer{}
			inner.script = []func() (*http.Response, error){inner.respond(http.StatusTooManyRequests, header)}
			d := &retryDoer{inner: inner, retries: 1}

			resp, err := d.Do(newRequest(t, http.MethodGet))
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			resp.Body.Close()

			if len(*waits) != 1 {
				t.Fatalf("waited %d times, want 1", len(*waits))
			}
			if (*waits)[0] != tt.want {
				t.Errorf("waited %s, want %s", (*waits)[0], tt.want)
			}
		})
	}
}

func TestRetryBackoffDoublesAndCaps(t *testing.T) {
	waits, _ := recordWaits(t)
	inner := &scriptedDoer{}
	inner.script = []func() (*http.Response, error){inner.respond(http.StatusBadGateway, nil)}
	d := &retryDoer{inner: inner, retries: 8}

	resp, err := d.Do(newRequest(t, http.MethodGet))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	resp.Body.Close()

	want := []time.Duration{
		1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second,
	}
	if len(*waits) != len(want) {
		t.Fatalf("waited %d times, want %d", len(*waits), len(want))
	}
	for i := range want {
		if (*waits)[i] != want[i] {
			t.Errorf("wait %d = %s, want %s", i+1, (*waits)[i], want[i])
		}
	}
}

// TestRetryStopsOnACancelledContext: Ctrl-C during a backoff must end the
// command, not be noticed a backoff later.
func TestRetryStopsOnACancelledContext(t *testing.T) {
	recordWaits(t)
	inner := &scriptedDoer{}
	inner.script = []func() (*http.Response, error){inner.respond(http.StatusServiceUnavailable, nil)}
	d := &retryDoer{inner: inner, retries: 5}

	ctx, cancel := context.WithCancel(context.Background())
	req := newRequest(t, http.MethodGet).WithContext(ctx)
	cancel()

	resp, err := d.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	resp.Body.Close()

	if inner.calls != 1 {
		t.Errorf("made %d attempts on a cancelled context, want 1", inner.calls)
	}
}

// TestRetryNoticeGoesToTheRetryWriter: stdout is a machine-readable document
// for half of qctl's commands, so the notice must not land there.
func TestRetryNoticeGoesToTheRetryWriter(t *testing.T) {
	_, notice := recordWaits(t)
	inner := &scriptedDoer{}
	inner.script = []func() (*http.Response, error){inner.respond(http.StatusTooManyRequests, nil)}
	d := &retryDoer{inner: inner, retries: 2}

	resp, err := d.Do(newRequest(t, http.MethodGet))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	resp.Body.Close()

	lines := strings.Count(notice.String(), "\n")
	if lines != 2 {
		t.Errorf("printed %d notice lines, want 2:\n%s", lines, notice.String())
	}
	// attempt N of M counts attempts, so the last one named is retries+1.
	if !strings.Contains(notice.String(), "attempt 3 of 3") {
		t.Errorf("notice does not number the attempts as 'attempt 3 of 3':\n%s", notice.String())
	}
	if !strings.Contains(notice.String(), "throttling") {
		t.Errorf("notice does not say why it retried:\n%s", notice.String())
	}
}

// TestRetryIsInertWithoutTheFlag: the shipped default must reach the inner
// client unchanged, not merely behave the same.
func TestRetryIsInertWithoutTheFlag(t *testing.T) {
	recordWaits(t)
	inner := &scriptedDoer{}
	inner.script = []func() (*http.Response, error){inner.respond(http.StatusServiceUnavailable, nil)}
	d := &retryDoer{inner: inner, retries: 0}

	resp, err := d.Do(newRequest(t, http.MethodGet))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if inner.calls != 1 {
		t.Errorf("made %d attempts with --retries 0, want 1", inner.calls)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "payload" {
		t.Errorf("body = %q, want the response passed through untouched", body)
	}
}
