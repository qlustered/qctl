package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	qclient "github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
)

// newTestWSServer creates an httptest server that upgrades to WebSocket
// and calls handler for each connection.
func newTestWSServer(t *testing.T, handler func(conn *websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		handler(conn)
	}))
	return server
}

func TestURLSchemeConversion(t *testing.T) {
	tests := []struct {
		baseURL string
		path    string
		wantURL string
	}{
		{"https://api.example.com", "/ws/test", "wss://api.example.com/ws/test"},
		{"http://localhost:8000", "/ws/test", "ws://localhost:8000/ws/test"},
		{"https://api.example.com/", "/ws/test", "wss://api.example.com/ws/test"},
		{"http://api.example.com", "/api/ws", "ws://api.example.com/api/ws"},
	}

	for _, tt := range tests {
		c := NewClient(Config{BaseURL: tt.baseURL, Path: tt.path})
		got := c.WSURL()
		if got != tt.wantURL {
			t.Errorf("WSURL(%q, %q) = %q, want %q", tt.baseURL, tt.path, got, tt.wantURL)
		}
	}
}

func TestConnectAndReadMessages(t *testing.T) {
	want := map[string]interface{}{
		"running_ingestion_job_count": float64(1),
		"waiting_ingestion_job_count": float64(3),
	}

	server := newTestWSServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		data, _ := json.Marshal(want)
		conn.WriteMessage(websocket.TextMessage, data)
		// Wait for client to receive before closing
		time.Sleep(100 * time.Millisecond)
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	})
	defer server.Close()

	// Convert http://... to ws://...
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	c := NewClient(Config{
		BaseURL: server.URL,
		Path:    "/ws",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	msgs, _ := c.ReadMessages(ctx)

	var received json.RawMessage
	select {
	case msg, ok := <-msgs:
		if !ok {
			t.Fatal("message channel closed before receiving message")
		}
		received = msg
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for message")
	}

	var got map[string]interface{}
	if err := json.Unmarshal(received, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if got["running_ingestion_job_count"] != want["running_ingestion_job_count"] {
		t.Errorf("running_ingestion_job_count = %v, want %v", got["running_ingestion_job_count"], want["running_ingestion_job_count"])
	}

	// Wait for channel to close (normal close code)
	for range msgs { //nolint:revive // Intentionally drain until the channel closes.
	}

	_ = wsURL // used for documentation
}

func TestNormalClose_NoReconnect(t *testing.T) {
	connectionCount := 0

	server := newTestWSServer(t, func(conn *websocket.Conn) {
		connectionCount++
		defer conn.Close()
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"))
	})
	defer server.Close()

	c := NewClient(Config{
		BaseURL:          server.URL,
		Path:             "/ws",
		ReconnectInitial: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	msgs, errCh := c.ReadMessages(ctx)

	// Drain — should close without reconnecting
	for range msgs { //nolint:revive // Intentionally drain until the channel closes.
	}

	if connectionCount != 1 {
		t.Errorf("expected 1 connection (no reconnect on normal close), got %d", connectionCount)
	}
	if err := streamErr(t, errCh); err != nil {
		t.Errorf("a 1000 close sent an error: %v", err)
	}
}

func TestAbnormalClose_Reconnects(t *testing.T) {
	connectionCount := 0

	server := newTestWSServer(t, func(conn *websocket.Conn) {
		connectionCount++
		defer conn.Close()

		if connectionCount == 1 {
			// First connection: send a message then close abnormally
			data := []byte(`{"count":1}`)
			conn.WriteMessage(websocket.TextMessage, data)
			time.Sleep(50 * time.Millisecond)
			conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "error"))
			return
		}

		// Second connection: send a message then close normally
		data := []byte(`{"count":2}`)
		conn.WriteMessage(websocket.TextMessage, data)
		time.Sleep(50 * time.Millisecond)
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"))
	})
	defer server.Close()

	c := NewClient(Config{
		BaseURL:          server.URL,
		Path:             "/ws",
		ReconnectInitial: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	msgs, _ := c.ReadMessages(ctx)

	var received []json.RawMessage
	for msg := range msgs {
		received = append(received, msg)
	}

	if len(received) < 2 {
		t.Errorf("expected at least 2 messages (across reconnect), got %d", len(received))
	}

	if connectionCount < 2 {
		t.Errorf("expected at least 2 connections (reconnect), got %d", connectionCount)
	}
}

func TestContextCancellation(t *testing.T) {
	server := newTestWSServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		// Keep sending messages until connection drops
		for {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"alive":true}`)); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	defer server.Close()

	c := NewClient(Config{
		BaseURL: server.URL,
		Path:    "/ws",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	msgs, _ := c.ReadMessages(ctx)

	// Read at least one message
	select {
	case _, ok := <-msgs:
		if !ok {
			t.Fatal("channel closed before first message")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first message")
	}

	// Cancel context
	cancel()

	// Channels should close promptly
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-msgs:
			if !ok {
				return // success
			}
		case <-timeout:
			t.Fatal("message channel not closed after context cancellation")
		}
	}
}

func TestAuthHeader(t *testing.T) {
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		conn.Close()
	}))
	defer server.Close()

	c := NewClient(Config{
		BaseURL:     server.URL,
		AccessToken: "test-token-123",
		Path:        "/ws",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	c.Close()

	if gotAuth != "Bearer test-token-123" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer test-token-123")
	}
}

func TestBackoff(t *testing.T) {
	c := NewClient(Config{
		ReconnectInitial: 1 * time.Second,
		ReconnectMax:     30 * time.Second,
		ReconnectFactor:  2.0,
	})

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 30 * time.Second}, // capped at max
		{10, 30 * time.Second},
		// Past ~2^33 seconds the product overflows a Duration; it must
		// still cap rather than wrap negative.
		{40, 30 * time.Second},
		{1000, 30 * time.Second},
	}

	for _, tt := range tests {
		got := c.backoff(tt.attempt)
		if got != tt.want {
			t.Errorf("backoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

// dialServer is an httptest server that hands every request, numbered from 1,
// to handler and counts them.
type dialServer struct {
	*httptest.Server
	dials atomic.Int32
}

func newDialServer(t *testing.T, handler func(dial int32, w http.ResponseWriter, r *http.Request)) *dialServer {
	t.Helper()
	s := &dialServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(s.dials.Add(1), w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// upgradeOrFail upgrades the request, returning nil when that fails.
func upgradeOrFail(t *testing.T, w http.ResponseWriter, r *http.Request) *websocket.Conn {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		t.Logf("upgrade error: %v", err)
		return nil
	}
	return conn
}

// sendThenClose writes msg, then a close frame with code and reason. A code
// of 0 drops the TCP connection without a close frame instead.
func sendThenClose(conn *websocket.Conn, msg string, code int, reason string) {
	defer conn.Close()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(msg))
	time.Sleep(20 * time.Millisecond)
	if code == 0 {
		_ = conn.UnderlyingConn().Close()
		return
	}
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
	// Hold the connection until the client answers the close frame, so the
	// client reads the close frame rather than a reset.
	_, _, _ = conn.ReadMessage()
}

// waitSilently holds the connection open without writing until the client
// goes away.
func waitSilently(conn *websocket.Conn) {
	defer conn.Close()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// drainMessages collects messages until the channel closes.
func drainMessages(t *testing.T, msgs <-chan json.RawMessage, within time.Duration) []string {
	t.Helper()
	var got []string
	deadline := time.After(within)
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				return got
			}
			got = append(got, string(msg))
		case <-deadline:
			t.Fatalf("message channel still open after %s (got %v)", within, got)
		}
	}
}

// streamErr reads the error channel after the message channel closed: it must
// not block, must hold at most one error, and must then be closed.
func streamErr(t *testing.T, errCh <-chan error) error {
	t.Helper()
	var err error
	select {
	case e, ok := <-errCh:
		if !ok {
			return nil
		}
		err = e
	default:
		t.Fatal("error channel neither closed nor holding an error after the message channel closed")
	}
	select {
	case e, ok := <-errCh:
		if ok {
			t.Fatalf("error channel held a second value: %v", e)
		}
	default:
		t.Fatal("error channel not closed after its error")
	}
	return err
}

// reconnectCall is one OnReconnect invocation.
type reconnectCall struct {
	attempt int
	wait    time.Duration
	err     error
}

// reconnectRecorder records OnReconnect calls.
type reconnectRecorder struct {
	mu    sync.Mutex
	calls []reconnectCall
}

func (r *reconnectRecorder) record(attempt int, wait time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, reconnectCall{attempt, wait, err})
}

func (r *reconnectRecorder) snapshot() []reconnectCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]reconnectCall(nil), r.calls...)
}

// startStream connects to s and starts reading, failing the test when the
// first connect fails.
func startStream(t *testing.T, s *dialServer, cfg Config) (msgs <-chan json.RawMessage, errCh <-chan error) {
	t.Helper()
	cfg.BaseURL = s.URL
	cfg.Path = "/ws"
	if cfg.ReconnectInitial == 0 {
		cfg.ReconnectInitial = 20 * time.Millisecond
	}
	c := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c.ReadMessages(ctx)
}

func TestConnect_HandshakeRejectionIsTyped(t *testing.T) {
	tests := []struct {
		status      int
		body        string
		wantExit    int
		wantExcerpt string
		wantLogin   bool
	}{
		{http.StatusUnauthorized, `{"detail":"Not authenticated"}`, qerrors.ExitUnauthorized, `"Not authenticated"`, true},
		{http.StatusForbidden, "", qerrors.ExitUnauthorized, "", false},
		{http.StatusNotFound, `{"detail":"dry_run_job 7 can not be found."}`, qerrors.ExitNotFound, `"dry_run_job 7 can not be found."`, false},
		{http.StatusBadRequest, "bad \x1b[31mred\x1b[0m\nrequest", qerrors.ExitBadUsage, `"bad \x1b[31mred\x1b[0m request"`, false},
		// FromHTTPStatusWithCause treats a 429 as transient, as REST does.
		{http.StatusTooManyRequests, "", qerrors.ExitGenericError, "", false},
		{http.StatusInternalServerError, `{"detail":"boom"}`, qerrors.ExitGenericError, `"boom"`, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			s := newDialServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			c := NewClient(Config{BaseURL: s.URL, Path: "/ws"})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			err := c.Connect(ctx)
			if err == nil {
				t.Fatal("Connect succeeded against a rejecting server")
			}
			if got := qerrors.GetExitCode(err); got != tt.wantExit {
				t.Errorf("exit code = %d, want %d (err: %v)", got, tt.wantExit, err)
			}
			if !errors.Is(err, websocket.ErrBadHandshake) {
				t.Errorf("error chain lost the dial error: %v", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, fmt.Sprintf("HTTP %d", tt.status)) {
				t.Errorf("message does not name the status: %q", msg)
			}
			if tt.wantExcerpt != "" && !strings.Contains(msg, tt.wantExcerpt) {
				t.Errorf("message %q does not quote %s", msg, tt.wantExcerpt)
			}
			if strings.Contains(msg, "\x1b") {
				t.Errorf("message carries a raw escape: %q", msg)
			}
			if got := strings.Contains(msg, "qctl auth login"); got != tt.wantLogin {
				t.Errorf("login hint present = %v, want %v: %q", got, tt.wantLogin, msg)
			}
			if got := s.dials.Load(); got != 1 {
				t.Errorf("dials = %d, want 1", got)
			}
		})
	}
}

func TestConnect_NetworkErrorIsUntyped(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	url := s.URL
	s.Close()

	c := NewClient(Config{BaseURL: url, Path: "/ws"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("Connect succeeded against a closed server")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitGenericError, err)
	}
}

func TestPolicyViolationClose_IsPermanent(t *testing.T) {
	const reason = "Authentication expired or revoked."
	s := newDialServer(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
		if conn := upgradeOrFail(t, w, r); conn != nil {
			sendThenClose(conn, `{"n":1}`, websocket.ClosePolicyViolation, reason)
		}
	})
	var rec reconnectRecorder
	msgs, errCh := startStream(t, s, Config{OnReconnect: rec.record})

	got := drainMessages(t, msgs, 3*time.Second)
	err := streamErr(t, errCh)

	if len(got) != 1 {
		t.Errorf("messages = %v, want the one sent before the close", got)
	}
	if err == nil {
		t.Fatal("a 1008 close sent no error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitUnauthorized, err)
	}
	if !strings.Contains(err.Error(), `"`+reason+`"`) {
		t.Errorf("message does not quote the close reason: %q", err)
	}
	if !strings.Contains(err.Error(), "qctl auth login") {
		t.Errorf("message does not point at qctl auth login: %q", err)
	}
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.ClosePolicyViolation {
		t.Errorf("error chain lost the close error: %v", err)
	}
	if n := s.dials.Load(); n != 1 {
		t.Errorf("dials = %d, want 1 (no redial after 1008)", n)
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("OnReconnect called %d times, want 0", len(calls))
	}
}

func TestPermanentCloseCodes_EndWithoutRedial(t *testing.T) {
	for _, code := range []int{websocket.CloseUnsupportedData, websocket.CloseMessageTooBig, 4001} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			s := newDialServer(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
				if conn := upgradeOrFail(t, w, r); conn != nil {
					sendThenClose(conn, `{"n":1}`, code, "go away\x1b[2J")
				}
			})
			var rec reconnectRecorder
			msgs, errCh := startStream(t, s, Config{OnReconnect: rec.record})

			drainMessages(t, msgs, 3*time.Second)
			err := streamErr(t, errCh)

			if err == nil {
				t.Fatalf("close %d sent no error", code)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitGenericError, err)
			}
			msg := err.Error()
			if !strings.Contains(msg, fmt.Sprintf("close %d", code)) || !strings.Contains(msg, `"go away\x1b[2J"`) {
				t.Errorf("message does not name the code and quote the reason: %q", msg)
			}
			if strings.Contains(msg, "\x1b") {
				t.Errorf("message carries a raw escape: %q", msg)
			}
			if n := s.dials.Load(); n != 1 {
				t.Errorf("dials = %d, want 1", n)
			}
			if calls := rec.snapshot(); len(calls) != 0 {
				t.Errorf("OnReconnect called %d times, want 0", len(calls))
			}
		})
	}
}

func TestRetryableClose_ReconnectsAndReports(t *testing.T) {
	tests := []struct {
		name string
		code int
	}{
		{"1011 internal error", websocket.CloseInternalServerErr},
		{"1012 service restart", websocket.CloseServiceRestart},
		{"1001 going away", websocket.CloseGoingAway},
		{"dropped without a close frame", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newDialServer(t, func(dial int32, w http.ResponseWriter, r *http.Request) {
				conn := upgradeOrFail(t, w, r)
				if conn == nil {
					return
				}
				if dial == 1 {
					sendThenClose(conn, `{"n":1}`, tt.code, "transient")
					return
				}
				sendThenClose(conn, `{"n":2}`, websocket.CloseNormalClosure, "")
			})
			var rec reconnectRecorder
			msgs, errCh := startStream(t, s, Config{OnReconnect: rec.record})

			got := drainMessages(t, msgs, 3*time.Second)
			if err := streamErr(t, errCh); err != nil {
				t.Errorf("stream ended with an error: %v", err)
			}

			if want := []string{`{"n":1}`, `{"n":2}`}; fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("messages = %v, want %v", got, want)
			}
			if n := s.dials.Load(); n != 2 {
				t.Errorf("dials = %d, want 2", n)
			}
			calls := rec.snapshot()
			if len(calls) != 1 {
				t.Fatalf("OnReconnect calls = %+v, want exactly one", calls)
			}
			if calls[0].attempt != 1 || calls[0].wait != 20*time.Millisecond || calls[0].err == nil {
				t.Errorf("OnReconnect(%d, %s, %v), want (1, 20ms, the read error)", calls[0].attempt, calls[0].wait, calls[0].err)
			}
		})
	}
}

func TestRedialServerError_RetriesWithGrowingBackoff(t *testing.T) {
	s := newDialServer(t, func(dial int32, w http.ResponseWriter, r *http.Request) {
		switch dial {
		case 1:
			if conn := upgradeOrFail(t, w, r); conn != nil {
				sendThenClose(conn, `{"n":1}`, websocket.CloseInternalServerErr, "")
			}
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			if conn := upgradeOrFail(t, w, r); conn != nil {
				sendThenClose(conn, `{"n":2}`, websocket.CloseNormalClosure, "")
			}
		}
	})
	var rec reconnectRecorder
	msgs, errCh := startStream(t, s, Config{OnReconnect: rec.record})

	got := drainMessages(t, msgs, 3*time.Second)
	if err := streamErr(t, errCh); err != nil {
		t.Errorf("stream ended with an error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("messages = %v, want 2", got)
	}
	calls := rec.snapshot()
	if len(calls) != 2 {
		t.Fatalf("OnReconnect calls = %+v, want 2", calls)
	}
	if calls[0].attempt != 1 || calls[0].wait != 20*time.Millisecond {
		t.Errorf("first call (%d, %s), want (1, 20ms)", calls[0].attempt, calls[0].wait)
	}
	if calls[1].attempt != 2 || calls[1].wait != 40*time.Millisecond || !strings.Contains(calls[1].err.Error(), "HTTP 503") {
		t.Errorf("second call (%d, %s, %v), want (2, 40ms, the 503)", calls[1].attempt, calls[1].wait, calls[1].err)
	}
}

func TestRedialRejected_IsPermanent(t *testing.T) {
	tests := []struct {
		status    int
		wantExit  int
		wantLogin bool
	}{
		{http.StatusForbidden, qerrors.ExitUnauthorized, true},
		{http.StatusUnauthorized, qerrors.ExitUnauthorized, true},
		{http.StatusNotFound, qerrors.ExitNotFound, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			s := newDialServer(t, func(dial int32, w http.ResponseWriter, r *http.Request) {
				if dial > 1 {
					w.WriteHeader(tt.status)
					return
				}
				if conn := upgradeOrFail(t, w, r); conn != nil {
					sendThenClose(conn, `{"n":1}`, websocket.CloseServiceRestart, "")
				}
			})
			var rec reconnectRecorder
			msgs, errCh := startStream(t, s, Config{OnReconnect: rec.record})

			drainMessages(t, msgs, 3*time.Second)
			err := streamErr(t, errCh)

			if err == nil {
				t.Fatalf("a redial refused with %d sent no error", tt.status)
			}
			if got := qerrors.GetExitCode(err); got != tt.wantExit {
				t.Errorf("exit code = %d, want %d (err: %v)", got, tt.wantExit, err)
			}
			msg := err.Error()
			if !strings.Contains(msg, "refused to reconnect") || !strings.Contains(msg, fmt.Sprintf("HTTP %d", tt.status)) {
				t.Errorf("message does not say the redial was refused: %q", msg)
			}
			wantHints := 0
			if tt.wantLogin {
				wantHints = 1
			}
			if got := strings.Count(msg, "qctl auth login"); got != wantHints {
				t.Errorf("login hint appears %d times, want %d: %q", got, wantHints, msg)
			}
			if n := s.dials.Load(); n != 2 {
				t.Errorf("dials = %d, want 2", n)
			}
			if calls := rec.snapshot(); len(calls) != 1 {
				t.Errorf("OnReconnect calls = %+v, want 1", calls)
			}
		})
	}
}

func TestContextCancelledDuringBackoff(t *testing.T) {
	s := newDialServer(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
		if conn := upgradeOrFail(t, w, r); conn != nil {
			sendThenClose(conn, `{"n":1}`, websocket.CloseInternalServerErr, "")
		}
	})
	sleeping := make(chan struct{})
	var once sync.Once
	c := NewClient(Config{
		BaseURL:          s.URL,
		Path:             "/ws",
		ReconnectInitial: 10 * time.Second,
		OnReconnect: func(int, time.Duration, error) {
			once.Do(func() { close(sleeping) })
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	msgs, errCh := c.ReadMessages(ctx)

	select {
	case <-sleeping:
	case <-time.After(3 * time.Second):
		t.Fatal("client never started a reconnect backoff")
	}
	cancel()

	drainMessages(t, msgs, 2*time.Second)
	if err := streamErr(t, errCh); err != nil {
		t.Errorf("cancelling ctx sent an error: %v", err)
	}
	if n := s.dials.Load(); n != 1 {
		t.Errorf("dials = %d, want 1", n)
	}
}

func TestStreamEndsOnSilentConnection(t *testing.T) {
	tests := []struct {
		name string
		end  func(cancel context.CancelFunc, c *Client)
	}{
		{"ctx cancelled", func(cancel context.CancelFunc, _ *Client) { cancel() }},
		{"Close called", func(_ context.CancelFunc, c *Client) { _ = c.Close() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newDialServer(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
				if conn := upgradeOrFail(t, w, r); conn != nil {
					waitSilently(conn)
				}
			})
			var rec reconnectRecorder
			c := NewClient(Config{BaseURL: s.URL, Path: "/ws", ReconnectInitial: 20 * time.Millisecond, OnReconnect: rec.record})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := c.Connect(ctx); err != nil {
				t.Fatalf("Connect failed: %v", err)
			}
			msgs, errCh := c.ReadMessages(ctx)

			time.Sleep(50 * time.Millisecond)
			tt.end(cancel, c)

			drainMessages(t, msgs, 2*time.Second)
			if err := streamErr(t, errCh); err != nil {
				t.Errorf("ending the stream sent an error: %v", err)
			}
			if calls := rec.snapshot(); len(calls) != 0 {
				t.Errorf("OnReconnect calls = %+v, want none", calls)
			}
			if n := s.dials.Load(); n != 1 {
				t.Errorf("dials = %d, want 1", n)
			}
		})
	}
}

func TestReconnectNotice(t *testing.T) {
	var buf bytes.Buffer
	notice := ReconnectNotice(&buf)

	notice(3, 2*time.Second, errors.New("websocket: close 1011 (internal server error): boom"))
	notice(1, 50*time.Millisecond, errors.New("bad\x1b[31m\nreason"))

	want := "connection lost (websocket: close 1011 (internal server error): boom); reconnecting in 2s (attempt 3)\n" +
		`connection lost (bad\x1b[31m reason); reconnecting in 50ms (attempt 1)` + "\n"
	if got := buf.String(); got != want {
		t.Errorf("notice output:\n%q\nwant:\n%q", got, want)
	}
}

func TestVerboseReconnectDiagnosticsPreserveTableRedraw(t *testing.T) {
	s := newDialServer(t, func(dial int32, w http.ResponseWriter, r *http.Request) {
		if dial == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if conn := upgradeOrFail(t, w, r); conn != nil {
			if dial == 1 {
				sendThenClose(conn, `{"n":1}`, websocket.CloseInternalServerErr, "transient")
			} else {
				sendThenClose(conn, `{"n":2}`, websocket.CloseNormalClosure, "")
			}
		}
	})
	var out bytes.Buffer
	stream := output.NewPrinter(output.Options{Writer: &out}).NewStream(true, true)
	notices := stream.NoticeWriter(&out)
	ctx, cancel := context.WithTimeout(qclient.WithDiagnosticWriter(t.Context(), notices), 5*time.Second)
	defer cancel()
	c := NewClient(Config{
		BaseURL: s.URL, Path: "/ws", AccessToken: "secret-token", Verbosity: verbosityCurlRedacted,
		ReconnectInitial: time.Millisecond,
		OnReconnect: func(attempt int, wait time.Duration, err error) {
			ReconnectNotice(notices)(attempt, wait, err)
			// Buffered frames can redraw while the reader waits to redial.
			if err := stream.WriteTable(fmt.Sprintf("during backoff %d\n", attempt)); err != nil {
				t.Error(err)
			}
		},
	})
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	msgs, errCh := c.ReadMessages(ctx)
	got := drainMessages(t, msgs, 3*time.Second)
	if err := streamErr(t, errCh); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || s.dials.Load() != 3 {
		t.Fatalf("messages=%v dials=%d", got, s.dials.Load())
	}
	if err := stream.WriteTable("after redial\n"); err != nil {
		t.Fatal(err)
	}
	if err := stream.WriteTable("changed again\n"); err != nil {
		t.Fatal(err)
	}
	log := out.String()
	for _, want := range []string{"ws read error, will reconnect:", "ws reconnect failed (attempt 2)", "during backoff 1\n", "during backoff 2\n"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in %q", want, log)
		}
	}
	if strings.Count(log, "ws connect ") != 3 || strings.Contains(log, "secret-token") {
		t.Errorf("dial diagnostics = %q", log)
	}
	wantEnd := "during backoff 2\nws connect " + c.WSURL() + " (Authorization: Bearer <REDACTED>)\nafter redial\n\033[1A\033[Jchanged again\n"
	if !strings.HasSuffix(log, wantEnd) {
		t.Errorf("terminal = %q, want suffix %q", log, wantEnd)
	}
}
