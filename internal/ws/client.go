package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Verbosity levels (mirrors client.VerbosityCurlRedacted / VerbosityCurlFull)
const (
	verbosityCurlRedacted = 8
	verbosityCurlFull     = 9
)

// Config holds WebSocket client configuration.
type Config struct {
	BaseURL     string // e.g. "https://api.example.com"
	AccessToken string
	Path        string // e.g. "/api/orgs/{org}/ws/datasets/{id}/job-activity"
	Verbosity   int

	// Reconnection settings (zero values use defaults)
	ReconnectInitial time.Duration // default 1s
	ReconnectMax     time.Duration // default 30s
	ReconnectFactor  float64       // default 2.0
}

func (c *Config) reconnectInitial() time.Duration {
	if c.ReconnectInitial > 0 {
		return c.ReconnectInitial
	}
	return 1 * time.Second
}

func (c *Config) reconnectMax() time.Duration {
	if c.ReconnectMax > 0 {
		return c.ReconnectMax
	}
	return 30 * time.Second
}

func (c *Config) reconnectFactor() float64 {
	if c.ReconnectFactor > 0 {
		return c.ReconnectFactor
	}
	return 2.0
}

// Client is a reconnecting WebSocket client.
type Client struct {
	config Config
	conn   *websocket.Conn
	mu     sync.Mutex
}

// NewClient creates a new WebSocket client.
func NewClient(cfg Config) *Client {
	return &Client{config: cfg}
}

// httpToWS converts an HTTP(S) base URL to a WS(S) URL with the configured path.
func (c *Client) wsURL() string {
	base := c.config.BaseURL
	base = strings.TrimRight(base, "/")

	if strings.HasPrefix(base, "https://") {
		base = "wss://" + strings.TrimPrefix(base, "https://")
	} else if strings.HasPrefix(base, "http://") {
		base = "ws://" + strings.TrimPrefix(base, "http://")
	}

	return base + c.config.Path
}

// Connect establishes a WebSocket connection.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectLocked(ctx)
}

func (c *Client) connectLocked(ctx context.Context) error {
	url := c.wsURL()

	if c.config.Verbosity >= verbosityCurlRedacted {
		displayURL := url
		tokenDisplay := "<REDACTED>"
		if c.config.Verbosity >= verbosityCurlFull {
			tokenDisplay = c.config.AccessToken
		}
		fmt.Fprintf(os.Stderr, "ws connect %s (Authorization: Bearer %s)\n", displayURL, tokenDisplay)
	}

	header := http.Header{}
	if c.config.AccessToken != "" {
		header.Set("Authorization", "Bearer "+c.config.AccessToken)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, url, header)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	c.conn = conn
	return nil
}

// ReadMessages returns channels for incoming messages and errors.
// It reconnects automatically on abnormal close. It stops when ctx is cancelled.
func (c *Client) ReadMessages(ctx context.Context) (<-chan json.RawMessage, <-chan error) {
	msgCh := make(chan json.RawMessage, 16)
	errCh := make(chan error, 1)

	go c.readLoop(ctx, msgCh, errCh)

	return msgCh, errCh
}

func (c *Client) readLoop(ctx context.Context, msgCh chan<- json.RawMessage, errCh chan<- error) {
	defer close(msgCh)
	defer close(errCh)

	attempt := 0

	for {
		// Ensure we have a connection
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			if err := c.Connect(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				backoff := c.backoff(attempt)
				if c.config.Verbosity >= verbosityCurlRedacted {
					fmt.Fprintf(os.Stderr, "ws reconnect failed (attempt %d), retrying in %s: %v\n", attempt+1, backoff, err)
				}
				attempt++
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
					continue
				}
			}
			attempt = 0
			c.mu.Lock()
			conn = c.conn
			c.mu.Unlock()
		}

		// Read message
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			// Check for normal close — don't reconnect
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return
			}

			// Abnormal close — reconnect
			if c.config.Verbosity >= verbosityCurlRedacted {
				fmt.Fprintf(os.Stderr, "ws read error, will reconnect: %v\n", err)
			}
			c.mu.Lock()
			c.conn.Close()
			c.conn = nil
			c.mu.Unlock()
			continue
		}

		select {
		case msgCh <- json.RawMessage(msg):
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) backoff(attempt int) time.Duration {
	initial := c.config.reconnectInitial()
	maxD := c.config.reconnectMax()
	factor := c.config.reconnectFactor()

	d := time.Duration(float64(initial) * math.Pow(factor, float64(attempt)))
	if d > maxD {
		d = maxD
	}
	return d
}

// Close sends a close frame and closes the underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}

	// Send close frame
	_ = c.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)

	err := c.conn.Close()
	c.conn = nil
	return err
}

// WSURL returns the computed WebSocket URL (exposed for testing).
func (c *Client) WSURL() string {
	return c.wsURL()
}
