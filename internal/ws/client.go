package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qlustered/qctl/internal/apierror"
	qclient "github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
)

// Verbosity levels (mirrors client.VerbosityCurlRedacted / VerbosityCurlFull)
const (
	verbosityCurlRedacted = 8
	verbosityCurlFull     = 9
)

// defaultHandshakeTimeout bounds the WebSocket upgrade when the user has not
// asked for something else.
const defaultHandshakeTimeout = 10 * time.Second

// closeWriteWait bounds writing the close frame in Close.
const closeWriteWait = time.Second

// maxExcerptWidth caps, in terminal cells, server text quoted in an error.
const maxExcerptWidth = 160

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

	// OnReconnect, when set, is called before each reconnect backoff sleep
	// with the attempt number (from 1, and from 1 again after a message
	// arrives), the sleep, and the error that ended the connection or failed
	// the last redial. It runs on the goroutine ReadMessages starts.
	OnReconnect func(attempt int, wait time.Duration, err error)
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

// ReconnectNotice returns an OnReconnect that writes one line per attempt to w.
func ReconnectNotice(w io.Writer) func(attempt int, wait time.Duration, err error) {
	return func(attempt int, wait time.Duration, err error) {
		cause := "unknown error"
		if err != nil {
			cause = oneLine(err.Error())
		}
		fmt.Fprintf(w, "connection lost (%s); reconnecting in %s (attempt %d)\n", cause, wait, attempt)
	}
}

// Client is a reconnecting WebSocket client.
type Client struct {
	config Config
	conn   *websocket.Conn
	// stopRead cancels the context of a running ReadMessages stream.
	stopRead context.CancelFunc
	mu       sync.Mutex
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

// Connect establishes a WebSocket connection. A handshake refused with 401 or
// 403 fails with exit code 4, with 404 exit code 3, and with any other 4xx
// but 429 exit code 2.
func (c *Client) Connect(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		// The dial reads under ctx's deadline, which can expire before ctx is
		// marked done; wait for it, so ctx.Err() agrees once Connect returns.
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			<-ctx.Done()
		}
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	return nil
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	url := c.wsURL()

	if c.config.Verbosity >= verbosityCurlRedacted {
		displayURL := url
		tokenDisplay := "<REDACTED>"
		if c.config.Verbosity >= verbosityCurlFull {
			tokenDisplay = c.config.AccessToken
		}
		fmt.Fprintf(qclient.DiagnosticWriter(ctx, os.Stderr), "ws connect %s (Authorization: Bearer %s)\n", displayURL, tokenDisplay)
	}

	header := http.Header{}
	if c.config.AccessToken != "" {
		header.Set("Authorization", "Bearer "+c.config.AccessToken)
	}

	// The handshake is an ordinary HTTP request, so --request-timeout governs
	// it -- but only when the user actually set one. TimeoutOverride rather
	// than ResolveTimeout is what keeps the unset case at this package's own
	// 10 seconds instead of silently tripling it to DefaultTimeout.
	//
	// Nothing after the handshake is covered: the stream is long-lived by
	// design, and its interruptions are handled by the reconnect backoff in
	// stream, not by a deadline.
	handshake := defaultHandshakeTimeout
	if override, ok := qclient.TimeoutOverride(); ok {
		handshake = override
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: handshake,
	}

	conn, resp, err := dialer.DialContext(ctx, url, header)
	if err != nil {
		return nil, dialError(resp, err)
	}
	return conn, nil
}

// handshakeError is an upgrade the server answered with an HTTP status
// instead of 101.
type handshakeError struct {
	status  int
	excerpt string
	err     error
}

func (e *handshakeError) Error() string {
	msg := fmt.Sprintf("websocket handshake failed: HTTP %d %s", e.status, http.StatusText(e.status))
	if e.excerpt != "" {
		msg += ": " + e.excerpt
	}
	if e.status == http.StatusUnauthorized {
		msg += "\n\n" + apierror.LoginHint
	}
	return msg
}

func (e *handshakeError) Unwrap() error { return e.err }

// dialError classifies a failed DialContext. A 4xx response is typed with
// qerrors.FromHTTPStatusWithCause; any other failure is not.
func dialError(resp *http.Response, err error) error {
	if resp == nil || resp.StatusCode == http.StatusSwitchingProtocols {
		return fmt.Errorf("websocket dial failed: %w", err)
	}
	he := &handshakeError{status: resp.StatusCode, excerpt: bodyExcerpt(resp.Body), err: err}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return qerrors.FromHTTPStatusWithCause(resp.StatusCode, he, "")
	}
	return he
}

// bodyExcerpt returns the start of a failed handshake's response body,
// quoted: the detail string of a {"detail": "..."} body, else the body
// itself. It returns "" for a blank body.
func bodyExcerpt(body io.Reader) string {
	if body == nil {
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(body, 1024))
	text := string(raw)
	var payload struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(raw, &payload) == nil && payload.Detail != "" {
		text = payload.Detail
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return quote(text)
}

// quote renders server-supplied text as one bounded, quoted line.
func quote(text string) string {
	return strconv.Quote(terminal.TruncateCell(strings.Join(strings.Fields(text), " "), maxExcerptWidth))
}

// oneLine renders an error message as one terminal-safe line.
func oneLine(text string) string {
	return terminal.Sanitize(strings.Join(strings.Fields(text), " "))
}

// ReadMessages streams the connection's messages, redialing with backoff
// after a lost connection, until ctx is done, Close is called, or the server
// closes with 1000. A failure that redialing cannot fix is sent on the error
// channel, which closes before the message channel, so once the message
// channel is closed a receive from the error channel returns at once: the
// failure, or nil.
func (c *Client) ReadMessages(ctx context.Context) (<-chan json.RawMessage, <-chan error) {
	msgCh := make(chan json.RawMessage, 16)
	errCh := make(chan error, 1)

	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.stopRead = cancel
	c.mu.Unlock()

	go c.readLoop(ctx, cancel, msgCh, errCh)

	return msgCh, errCh
}

func (c *Client) readLoop(ctx context.Context, cancel context.CancelFunc, msgCh chan<- json.RawMessage, errCh chan<- error) {
	defer func() {
		// errCh first: ReadMessages promises it is closed once msgCh is.
		close(errCh)
		close(msgCh)
	}()
	defer cancel()

	// A read blocked on a silent server only returns once its connection
	// closes, so ctx being done has to close it.
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()

	if err := c.stream(ctx, msgCh); err != nil {
		errCh <- err
	}
}

// stream feeds msgCh until the stream ends. It returns nil when ctx is done
// or the server closed with 1000, and the permanent failure otherwise.
func (c *Client) stream(ctx context.Context, msgCh chan<- json.RawMessage) error {
	failures := 0
	var lost error // what ended the last connection; nil until one is lost

	for {
		conn, err := c.current(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = pump(ctx, conn, msgCh, &failures)
			if ctx.Err() != nil {
				return nil
			}
			c.drop(conn)
			end, permanent := readOutcome(err)
			if end || permanent != nil {
				return permanent
			}
			if c.config.Verbosity >= verbosityCurlRedacted {
				fmt.Fprintf(qclient.DiagnosticWriter(ctx, os.Stderr), "ws read error, will reconnect: %v\n", err)
			}
			lost = err
		} else {
			if qerrors.IsPermanent(err) {
				return redialRefused(lost, err)
			}
			if c.config.Verbosity >= verbosityCurlRedacted {
				fmt.Fprintf(qclient.DiagnosticWriter(ctx, os.Stderr), "ws reconnect failed (attempt %d), retrying in %s: %v\n", failures+1, c.backoff(failures), err)
			}
		}
		failures++
		if !c.sleep(ctx, failures, err) {
			return nil
		}
	}
}

// pump forwards conn's messages to msgCh until a read fails or ctx is done,
// and returns the error that stopped it. Each message resets *failures.
func pump(ctx context.Context, conn *websocket.Conn, msgCh chan<- json.RawMessage, failures *int) error {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		*failures = 0
		select {
		case msgCh <- json.RawMessage(msg):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// current returns the open connection, dialing one when there is none.
func (c *Client) current(ctx context.Context) (*websocket.Conn, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		return conn, nil
	}
	return c.redial(ctx)
}

// redial dials a replacement connection and installs it, unless ctx is done
// by then, in which case it closes the new connection and returns nil.
func (c *Client) redial(ctx context.Context) (*websocket.Conn, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		_ = conn.Close()
		return nil, nil
	}
	c.conn = conn
	return conn, nil
}

// drop closes conn and forgets it if it is still the current connection.
func (c *Client) drop(conn *websocket.Conn) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
	_ = conn.Close()
}

// sleep reports attempt to OnReconnect and waits out its backoff. It returns
// false when ctx is done first.
func (c *Client) sleep(ctx context.Context, attempt int, err error) bool {
	wait := c.backoff(attempt - 1)
	if c.config.OnReconnect != nil {
		c.config.OnReconnect(attempt, wait, err)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// readOutcome classifies a read error: end is true for a 1000 close, and
// permanent is non-nil for a close code that redialing cannot change. Any
// other error is worth a reconnect.
func readOutcome(err error) (end bool, permanent error) {
	var ce *websocket.CloseError
	if !errors.As(err, &ce) {
		return false, nil
	}
	switch {
	case ce.Code == websocket.CloseNormalClosure:
		return true, nil
	case ce.Code == websocket.ClosePolicyViolation:
		return false, qerrors.Wrap(qerrors.ExitUnauthorized,
			qerrors.Caused(closedMessage(ce)+"\n\n"+apierror.LoginHint, err), "")
	case isPermanentClose(ce.Code):
		return false, qerrors.Caused(closedMessage(ce), err)
	}
	return false, nil
}

// isPermanentClose reports whether a close code other than 1000 and 1008 ends
// the stream rather than prompting a reconnect.
func isPermanentClose(code int) bool {
	switch code {
	case websocket.CloseProtocolError,
		websocket.CloseUnsupportedData,
		websocket.CloseInvalidFramePayloadData,
		websocket.CloseMessageTooBig,
		websocket.CloseMandatoryExtension:
		return true
	}
	return code >= 3000 && code <= 4999
}

// closedMessage names a close frame's code and quotes its reason.
func closedMessage(ce *websocket.CloseError) string {
	msg := "the server closed the stream with " +
		strings.TrimPrefix((&websocket.CloseError{Code: ce.Code}).Error(), "websocket: ")
	if ce.Text != "" {
		msg += ": " + quote(ce.Text)
	}
	return msg
}

// redialRefused describes a redial the server refused permanently after the
// connection was lost to cause. It returns err unchanged when cause is nil;
// otherwise a 403 also gains the login hint.
func redialRefused(cause, err error) error {
	if cause == nil {
		return err
	}
	refused := fmt.Errorf("connection lost (%s) and the server refused to reconnect: %w", oneLine(cause.Error()), err)
	var he *handshakeError
	if errors.As(err, &he) && he.status == http.StatusForbidden {
		return fmt.Errorf("%w\n\n%s", refused, apierror.LoginHint)
	}
	return refused
}

func (c *Client) backoff(attempt int) time.Duration {
	initial := c.config.reconnectInitial()
	maxD := c.config.reconnectMax()
	factor := c.config.reconnectFactor()

	// Compared as a float: a product beyond the Duration range has no usable
	// conversion.
	d := float64(initial) * math.Pow(factor, float64(attempt))
	if d >= float64(maxD) {
		return maxD
	}
	return time.Duration(d)
}

// Close ends a running ReadMessages stream, sends a close frame, and closes
// the underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.stopRead != nil {
		c.stopRead()
	}
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn == nil {
		return nil
	}

	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(closeWriteWait),
	)

	return conn.Close()
}

// WSURL returns the computed WebSocket URL (exposed for testing).
func (c *Client) WSURL() string {
	return c.wsURL()
}
