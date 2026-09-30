//go:build unix

package paths

import (
	"errors"
	"os"
	"syscall"
)

// ErrLocked is returned by TryLock when another process holds the lock.
var ErrLocked = errors.New("lock held by another process")

// Lock is an advisory file lock.
type Lock struct{ f *os.File }

// TryLock acquires an exclusive lock without blocking.
func TryLock(path string) (*Lock, error) {
	return lock(path, syscall.LOCK_EX|syscall.LOCK_NB)
}

// WaitLock acquires an exclusive lock, blocking until it is available.
func WaitLock(path string) (*Lock, error) {
	return lock(path, syscall.LOCK_EX)
}

func lock(path string, how int) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Unlock releases the lock.
func (l *Lock) Unlock() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}
