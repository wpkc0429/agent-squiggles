//go:build unix

package paths

import (
	"fmt"
	"os"
	"syscall"
)

// EnsureRuntimeDir creates the socket directory and verifies that only the
// current user can use it. The fallback location is under a shared /tmp,
// where another user could pre-create the directory to impersonate the
// daemon or read hook payloads.
func EnsureRuntimeDir() (string, error) {
	dir := RuntimeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	if int(st.Uid) != os.Getuid() {
		return "", fmt.Errorf("%s is owned by another user; refusing to use it", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", err
		}
	}
	return dir, nil
}
