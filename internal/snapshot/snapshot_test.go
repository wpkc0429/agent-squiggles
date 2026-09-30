package snapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

var testPatterns = []string{"*.go", "*.ts", "*.py"}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) (string, *Repo) {
	t.Helper()
	root := t.TempDir()
	run(t, root, "git", "init", "-q")
	write(t, root, "a.go", "package a\n")
	write(t, root, "web/c.ts", "export const c = 1;\n")
	write(t, root, "README.md", "hello\n")
	write(t, root, ".gitignore", "ignored.go\n")
	run(t, root, "git", "add", "-A")
	run(t, root, "git", "commit", "-q", "-m", "init")
	r, err := Open(filepath.Join(root, "web"), t.TempDir(), testPatterns)
	if err != nil {
		t.Fatal(err)
	}
	return root, r
}

func TestSnapshotDiff(t *testing.T) {
	root, r := newRepo(t)
	resolved, _ := filepath.EvalSymlinks(root)
	if got, _ := filepath.EvalSymlinks(r.Root); got != resolved {
		t.Fatalf("root = %q, want %q", r.Root, resolved)
	}
	statusBefore := run(t, root, "git", "status", "--porcelain")

	t0, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "a.go", "package a\n\nfunc F() {}\n")
	write(t, root, "pkg/b.py", "x = 1\n")
	write(t, root, "ignored.go", "package ignored\n")
	write(t, root, "node_modules/dep/index.ts", "export {};\n")
	write(t, root, "README.md", "changed\n")
	if err := os.Remove(filepath.Join(root, "web/c.ts")); err != nil {
		t.Fatal(err)
	}
	t1, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	changes, err := r.Diff(t0, t1)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	want := []Change{{"M", "a.go"}, {"A", "pkg/b.py"}, {"D", "web/c.ts"}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}

	old, ok, err := r.ReadFile(t0, "a.go")
	if err != nil || !ok || string(old) != "package a\n" {
		t.Fatalf("ReadFile(t0, a.go) = %q, %v, %v", old, ok, err)
	}
	if _, ok, err := r.ReadFile(t0, "pkg/b.py"); ok || err != nil {
		t.Fatalf("ReadFile(t0, pkg/b.py) ok=%v err=%v, want missing", ok, err)
	}

	// The user's index must be untouched.
	write(t, root, "a.go", "package a\n")
	write(t, root, "README.md", "hello\n")
	_ = os.Remove(filepath.Join(root, "pkg/b.py"))
	_ = os.Remove(filepath.Join(root, "ignored.go"))
	_ = os.RemoveAll(filepath.Join(root, "node_modules"))
	write(t, root, "web/c.ts", "export const c = 1;\n")
	if got := run(t, root, "git", "status", "--porcelain"); got != statusBefore {
		t.Fatalf("git status changed:\n%s\nwant:\n%s", got, statusBefore)
	}
}

func TestSnapshotNoChangesSameTree(t *testing.T) {
	_, r := newRepo(t)
	t0, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	t1, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if t0 != t1 {
		t.Fatalf("trees differ without changes: %s vs %s", t0, t1)
	}
}

func TestSnapshotUnmatchedPatterns(t *testing.T) {
	root := t.TempDir()
	run(t, root, "git", "init", "-q")
	write(t, root, "main.go", "package main\n")
	r, err := Open(root, t.TempDir(), []string{"*.go", "*.py", "*.rs"})
	if err != nil {
		t.Fatal(err)
	}
	t0, err := r.Snapshot()
	if err != nil {
		t.Fatalf("snapshot with unmatched patterns: %v", err)
	}
	files, err := r.ListFiles([]string{"*.go"})
	if err != nil || len(files) != 1 || files[0] != "main.go" {
		t.Fatalf("ListFiles = %v, %v", files, err)
	}
	if content, ok, _ := r.ReadFile(t0, "main.go"); !ok || string(content) != "package main\n" {
		t.Fatalf("untracked file missing from snapshot")
	}
}

func TestOpenNotGit(t *testing.T) {
	if _, err := Open(t.TempDir(), t.TempDir(), testPatterns); err != ErrNotGit {
		t.Fatalf("err = %v, want ErrNotGit", err)
	}
}
