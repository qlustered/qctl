// Package filelock provides advisory locks on files.
package filelock

import "errors"

// ErrLocked is returned by TryLock when another holder has the lock.
var ErrLocked = errors.New("file is locked by another process")

// ErrUnsupported is returned on platforms without advisory file locks.
var ErrUnsupported = errors.New("file locking is not supported on this platform")

// Lock is a held lock. Release it with Unlock.
type Lock struct {
	unlock func() error
	// fd is the locked file's descriptor.
	fd uintptr
}

// Unlock releases the lock.
func (l *Lock) Unlock() error {
	return l.unlock()
}
