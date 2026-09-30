//go:build !unix

package paths

import "os"

// EnsureRuntimeDir creates the socket directory.
func EnsureRuntimeDir() (string, error) {
	dir := RuntimeDir()
	return dir, os.MkdirAll(dir, 0o700)
}
