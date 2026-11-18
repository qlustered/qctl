package testutil

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
)

var stdoutMu sync.Mutex

// CaptureStdout runs fn while process stdout points at a temporary file.
// Use it when a test must observe process stdout. Calls are serialized because
// stdout is global.
func CaptureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	stdoutMu.Lock()
	defer stdoutMu.Unlock()

	capture, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	defer capture.Close()

	original := os.Stdout
	os.Stdout = capture
	defer func() { os.Stdout = original }()

	runErr := fn()
	os.Stdout = original

	if _, err := capture.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind stdout capture: %v", err)
	}
	out, err := io.ReadAll(capture)
	if err != nil {
		t.Fatalf("read stdout capture: %v", err)
	}
	return string(out), runErr
}

// LockedBuffer is a bytes.Buffer that several goroutines can write to.
type LockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *LockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *LockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
