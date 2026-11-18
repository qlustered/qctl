//go:build unix

package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Acquire blocks until it holds an exclusive lock on path, creating the file
// and its parent directories (0700) if needed.
func Acquire(path string) (*Lock, error) {
	return lock(path, syscall.LOCK_EX)
}

// AcquireShared blocks until it holds a shared lock on path, creating the
// file and its parent directories (0700) if needed. Any number of shared
// holders may hold it at once; an exclusive holder excludes them all.
func AcquireShared(path string) (*Lock, error) {
	return lock(path, syscall.LOCK_SH)
}

// TryLock takes an exclusive lock on path without blocking. It returns
// ErrLocked when another holder has it.
func TryLock(path string) (*Lock, error) {
	return lock(path, syscall.LOCK_EX|syscall.LOCK_NB)
}

func lock(path string, how int) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	fd := f.Fd()
	return &Lock{fd: fd, unlock: func() error {
		uerr := syscall.Flock(int(fd), syscall.LOCK_UN)
		cerr := f.Close()
		return errors.Join(uerr, cerr)
	}}, nil
}

// Inheritable clears close-on-exec on the lock's descriptor and returns it. A
// program this process replaces itself with through exec then holds the lock
// until it exits, as does every child that inherits the descriptor.
func (l *Lock) Inheritable() (uintptr, error) {
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, l.fd, syscall.F_SETFD, 0); errno != 0 {
		return 0, errno
	}
	return l.fd, nil
}
