package fileul

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
)

// craftedStatusServer answers one request with a raw status line whose reason
// phrase the test chooses. httptest cannot do this -- WriteHeader always emits
// the canonical phrase -- and the reason phrase is exactly the half of that
// line the peer controls: net/http hands it back in resp.Status verbatim.
func craftedStatusServer(t *testing.T, reason string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
			return
		}
		_, _ = io.WriteString(conn,
			"HTTP/1.1 200 "+reason+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}()
	return "http://" + ln.Addr().String()
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what
// was written. The verbose upload trace logs to the process stderr directly, so
// there is no writer to inject.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = saved }()

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()

	fn()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// The upload goes to a presigned URL at a storage host, not to the API, so the
// reason phrase on the response's status line is attacker-reachable the moment
// that host is. -v echoes it to stderr, and a trace is read most closely when
// something has already gone wrong.
func TestExecuteUploadSanitizesTheVerboseStatusLine(t *testing.T) {
	url := craftedStatusServer(t, "\x1b[31mOK\x1b]0;pwned\x07")
	req, err := http.NewRequest(http.MethodPut, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	httpClient := &http.Client{}

	got := captureStderr(t, func() {
		if err := executeUpload(httpClient, req, http.MethodPut, 1); err != nil {
			t.Errorf("executeUpload: %v", err)
		}
	})

	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("the verbose trace reached the terminal with a live control byte: %q", got)
	}
	if !strings.Contains(got, `\x1b[31mOK\x1b]0;pwned\x07`) {
		t.Errorf("missing the visible escape: %q", got)
	}
}
