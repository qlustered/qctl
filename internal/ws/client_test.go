package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
		baseURL  string
		path     string
		wantURL  string
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
	for range msgs {
		// drain
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

	msgs, _ := c.ReadMessages(ctx)

	// Drain — should close without reconnecting
	for range msgs {
	}

	if connectionCount != 1 {
		t.Errorf("expected 1 connection (no reconnect on normal close), got %d", connectionCount)
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
	}

	for _, tt := range tests {
		got := c.backoff(tt.attempt)
		if got != tt.want {
			t.Errorf("backoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}
