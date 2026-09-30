//go:build unix

package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureRuntimeDirTightensPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rt")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SQUIGGLES_RUNTIME_DIR", dir)
	if _, err := EnsureRuntimeDir(); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(dir)
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v, want 0700", fi.Mode().Perm())
	}
}

func TestEnsureRuntimeDirRejectsSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "rt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SQUIGGLES_RUNTIME_DIR", link)
	if _, err := EnsureRuntimeDir(); err == nil {
		t.Fatal("expected a symlinked runtime dir to be rejected")
	}
}
