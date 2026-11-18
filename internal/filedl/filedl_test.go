package filedl

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestToFileWritesTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("id,name\n1,\\N\n"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "rows.csv")
	if err := ToFile(srv.Client(), "GET", srv.URL, out, false, 0); err != nil {
		t.Fatalf("ToFile: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// The export writes SQL NULL as the two characters \N (spec 2.5). Nothing
	// in this path may transform the bytes.
	if string(got) != "id,name\n1,\\N\n" {
		t.Errorf("body not written verbatim, got %q", got)
	}
}

func TestToFileRefusesAnExistingPathWithoutForce(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte("new"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "rows.csv")
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := ToFile(srv.Client(), "GET", srv.URL, out, false, 0)
	if err == nil {
		t.Fatal("expected a refusal when the path exists and force is false")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("an existing path is a usage error: exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if requests != 0 {
		t.Errorf("made %d requests; the collision is checked before the transfer", requests)
	}
	if got, _ := os.ReadFile(out); string(got) != "old" {
		t.Errorf("existing file was clobbered: %q", got)
	}
}

func TestToFileOverwritesWithForce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("new"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "rows.csv")
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ToFile(srv.Client(), "GET", srv.URL, out, true, 0); err != nil {
		t.Fatalf("ToFile with force: %v", err)
	}
	if got, _ := os.ReadFile(out); string(got) != "new" {
		t.Errorf("force did not overwrite, got %q", got)
	}
}

// A table export or a decrypted backup is the most sensitive artifact this CLI
// writes, and os.Create would have left it 0666 & umask -- world-readable on
// the usual 022. The mode applies on CREATE, so the overwrite half of this
// test pins the deliberate limit: an existing destination reached through
// --force keeps whatever mode it already had, because O_TRUNC does not change
// one.
func TestToFileCreatesTheDestinationOwnerReadableOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("id,name\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "rows.csv")
	if err := ToFile(srv.Client(), "GET", srv.URL, out, false, 0); err != nil {
		t.Fatalf("ToFile: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("created mode = %04o, want 0600", perm)
	}

	existing := filepath.Join(dir, "already-there.csv")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set the fixture's permissions independently of the process umask.
	if err := os.Chmod(existing, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ToFile(srv.Client(), "GET", srv.URL, existing, true, 0); err != nil {
		t.Fatalf("ToFile with force: %v", err)
	}
	info, err = os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("an existing destination's mode = %04o, want it left at 0644: that file is the user's, not ours to re-permission", perm)
	}
}

// A non-200 must not leave a truncated file behind: O_TRUNC would have
// already emptied the target before the body was read.
func TestToFileNon200LeavesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("expired"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "rows.csv")
	err := ToFile(srv.Client(), "GET", srv.URL, out, false, 0)
	if err == nil {
		t.Fatal("expected an error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should name the status, got: %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("a failed download must not leave a file behind")
	}
}

// A connection that dies mid-body is the dangerous case: the status was 200,
// O_TRUNC has emptied the destination, and some bytes are already on disk.
// A short CSV that looks complete is worse than no CSV.
func TestToFileTruncatedBodyLeavesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "64")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("id,name\n"))
		// Return without writing the promised 64 bytes; net/http closes the
		// connection and the reader sees an unexpected EOF.
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "rows.csv")
	err := ToFile(srv.Client(), "GET", srv.URL, out, false, 0)
	if err == nil {
		t.Fatal("expected an error on a truncated body")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("a partially written file must be removed, not left looking complete")
	}
}

func TestToFileDefaultsToGET(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "f")
	if err := ToFile(srv.Client(), "", srv.URL, out, false, 0); err != nil {
		t.Fatalf("ToFile: %v", err)
	}
	if got != http.MethodGet {
		t.Errorf("method = %q, want GET", got)
	}
}

func TestTransferClientHasNoOverallDeadline(t *testing.T) {
	// A whole-table export is unbounded in size; an overall client timeout
	// would abort a legitimate large transfer. The hung-server case is covered
	// by the transport's response-header timeout instead.
	if c := TransferClient(); c.Timeout != 0 {
		t.Errorf("TransferClient().Timeout = %v, want 0 (no overall deadline)", c.Timeout)
	}
}

// stubRoundTripper stands in for the non-*http.Transport values this repo
// installs into http.DefaultTransport: testutil.NewMockAPIServer swaps in its
// own *mockTransport, and some suites install a roundTripFunc.
type stubRoundTripper struct{}

func (stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, http.ErrNotSupported
}

func TestTransferClientSurvivesASwappedDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = stubRoundTripper{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// Must not panic on the type assertion, and the client it hands back must
	// still be able to transfer a file.
	c := TransferClient()
	if c == nil || c.Transport == nil {
		t.Fatal("TransferClient returned no usable client")
	}
	out := filepath.Join(t.TempDir(), "f")
	if err := ToFile(c, "", srv.URL, out, false, 0); err != nil {
		t.Fatalf("ToFile with a client built under a swapped DefaultTransport: %v", err)
	}
	if got, _ := os.ReadFile(out); string(got) != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
}

// craftedStatusServer answers one request with a raw status line whose reason
// phrase the test chooses. httptest cannot do this: WriteHeader always emits
// the canonical phrase, and the reason phrase is precisely the server-controlled
// half of the line -- net/http hands it back in resp.Status verbatim.
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
			"HTTP/1.1 200 "+reason+"\r\nContent-Length: 3\r\nConnection: close\r\n\r\nabc")
	}()
	return "http://" + ln.Addr().String()
}

// -v prints the response's status LINE, and the reason phrase on it belongs to
// whatever storage host minted the presigned URL. A trace is read most closely
// when something has already gone wrong, so it is the worst place to let an
// escape sequence through.
func TestToFileSanitizesTheVerboseStatusLine(t *testing.T) {
	url := craftedStatusServer(t, "\x1b[31mOK\x1b]0;pwned\x07")
	out := filepath.Join(t.TempDir(), "rows.csv")

	got := captureStderr(t, func() {
		if err := ToFile(&http.Client{}, "GET", url, out, false, 1); err != nil {
			t.Errorf("ToFile: %v", err)
		}
	})

	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("the verbose trace reached the terminal with a live control byte: %q", got)
	}
	if !strings.Contains(got, `\x1b[31mOK\x1b]0;pwned\x07`) {
		t.Errorf("missing the visible escape: %q", got)
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what
// was written. The verbose trace logs to the process stderr directly, so there
// is no writer to inject -- the same shape as internal/client's helper.
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
