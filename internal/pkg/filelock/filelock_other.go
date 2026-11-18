//go:build !unix

package filelock

// Acquire returns ErrUnsupported.
func Acquire(string) (*Lock, error) { return nil, ErrUnsupported }

// AcquireShared returns ErrUnsupported.
func AcquireShared(string) (*Lock, error) { return nil, ErrUnsupported }

// TryLock returns ErrUnsupported.
func TryLock(string) (*Lock, error) { return nil, ErrUnsupported }

// Inheritable returns ErrUnsupported.
func (l *Lock) Inheritable() (uintptr, error) { return 0, ErrUnsupported }
