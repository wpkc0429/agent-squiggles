//go:build !unix

package paths

import (
	"errors"
	"os"
)

// ErrLocked is returned by TryLock when another process holds the lock.
var ErrLocked = errors.New("lock held by another process")

// Lock is an exclusive lock file (best effort on non-Unix systems).
type Lock struct{ path string }

// TryLock acquires the lock by creating the file exclusively.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrLocked
		}
		return nil, err
	}
	f.Close()
	return &Lock{path: path}, nil
}

// WaitLock is TryLock on platforms without advisory locks.
func WaitLock(path string) (*Lock, error) { return TryLock(path) }

// Unlock releases the lock.
func (l *Lock) Unlock() {
	if l != nil {
		_ = os.Remove(l.path)
	}
}
