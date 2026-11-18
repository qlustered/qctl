//go:build unix

package filelock

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
)

func TestTryLockReportsAHeldLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "x.lock")
	held, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock while held: got %v, want ErrLocked", err)
	}
	if err := held.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	again, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after release: %v", err)
	}
	_ = again.Unlock()
}

func TestSharedLocksCoexistAndExcludeAnExclusiveOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.inuse")
	a, err := AcquireShared(path)
	if err != nil {
		t.Fatalf("AcquireShared: %v", err)
	}
	b, err := AcquireShared(path)
	if err != nil {
		t.Fatalf("second AcquireShared: %v", err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock with two shared holders: got %v, want ErrLocked", err)
	}
	if err := a.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock with one shared holder left: got %v, want ErrLocked", err)
	}
	if err := b.Unlock(); err != nil {
		t.Fatal(err)
	}
	ex, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after both released: %v", err)
	}
	_ = ex.Unlock()
}

func TestInheritableClearsCloseOnExec(t *testing.T) {
	l, err := AcquireShared(filepath.Join(t.TempDir(), "x.inuse"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Unlock() }()
	if flags := fdFlags(t, l.fd); flags&syscall.FD_CLOEXEC == 0 {
		t.Fatalf("a fresh lock's descriptor lacks FD_CLOEXEC (flags %#x)", flags)
	}
	fd, err := l.Inheritable()
	if err != nil {
		t.Fatalf("Inheritable: %v", err)
	}
	if fd != l.fd {
		t.Errorf("Inheritable returned fd %d, the lock holds %d", fd, l.fd)
	}
	if flags := fdFlags(t, fd); flags&syscall.FD_CLOEXEC != 0 {
		t.Errorf("FD_CLOEXEC still set after Inheritable (flags %#x)", flags)
	}
}

func fdFlags(t *testing.T, fd uintptr) uintptr {
	t.Helper()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFD, 0)
	if errno != 0 {
		t.Fatalf("F_GETFD: %v", errno)
	}
	return flags
}
