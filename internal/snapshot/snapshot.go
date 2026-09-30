// Package snapshot records the state of a git working tree as tree
// objects, without touching the user's index, branches, or stash.
//
// agent-squiggles snapshots the tree right before and right after a tool
// call, then diffs the two trees to learn exactly which source files the
// tool changed. This works the same for apply_patch edits and for files
// rewritten by shell commands (sed -i, code generators, formatters).
package snapshot

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ErrNotGit is returned by Open when the directory is not inside a git
// working tree.
var ErrNotGit = errors.New("snapshot: not a git working tree")

// EmptyTree is the well-known hash of git's empty tree.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// defaultExcludes keeps dependency and virtualenv directories out of
// snapshots even when a project forgot to gitignore them.
var defaultExcludes = []string{
	":(exclude,glob)**/node_modules/**",
	":(exclude,glob)**/.venv/**",
	":(exclude,glob)**/venv/**",
	":(exclude,glob)**/site-packages/**",
	":(exclude,glob)**/__pycache__/**",
}

// Change is one file that differs between two snapshots.
type Change struct {
	// Status is "A" (added), "M" (modified), or "D" (deleted).
	Status string
	// Path is relative to the repository root, using forward slashes.
	Path string
}

// Repo snapshots a git working tree using a private index file.
type Repo struct {
	Root      string
	indexPath string
	privIndex string
	patterns  []string

	mu sync.Mutex
}

// Open locates the git working tree containing dir. patterns are git
// pathspecs selecting the files to track (for example "*.go").
// stateDir holds the private index; it is created if needed.
func Open(dir, stateDir string, patterns []string) (*Repo, error) {
	out, err := gitOutput(dir, nil, "rev-parse", "--show-toplevel", "--git-path", "index")
	if err != nil {
		return nil, ErrNotGit
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return nil, ErrNotGit
	}
	root := filepath.Clean(lines[0])
	index := lines[1]
	if !filepath.IsAbs(index) {
		index = filepath.Join(dir, index)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(root))
	return &Repo{
		Root:      root,
		indexPath: index,
		privIndex: filepath.Join(stateDir, "index-"+hex.EncodeToString(sum[:8])),
		patterns:  patterns,
	}, nil
}

func (r *Repo) pathspecs() []string {
	specs := append([]string{}, r.patterns...)
	return append(specs, defaultExcludes...)
}

// Snapshot writes the current state of the tracked files as a tree object
// and returns its hash.
//
// It stages into a private index (seeded from the real one on first use),
// so the user's index is never modified. Keeping the private index between
// calls lets git reuse its stat cache, so unchanged files are not rehashed.
func (r *Repo) Snapshot() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := os.Stat(r.privIndex); errors.Is(err, os.ErrNotExist) {
		if err := copyFile(r.indexPath, r.privIndex); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	tree, err := r.stageAndWrite()
	if err != nil {
		// A corrupt or stale private index should never wedge us: start over
		// from the real index once.
		_ = os.Remove(r.privIndex)
		if err2 := copyFile(r.indexPath, r.privIndex); err2 != nil && !errors.Is(err2, os.ErrNotExist) {
			return "", err
		}
		return r.stageAndWrite()
	}
	return tree, nil
}

// stageAndWrite updates the private index from the working tree and writes
// it as a tree. It avoids "git add <pathspec>", which fails outright when
// any pathspec (say "*.py" in a Go-only repo) matches nothing.
func (r *Repo) stageAndWrite() (string, error) {
	env := []string{"GIT_INDEX_FILE=" + r.privIndex}
	args := append([]string{"ls-files", "-z", "--modified", "--deleted", "--others", "--exclude-standard", "--"}, r.pathspecs()...)
	out, err := gitOutput(r.Root, env, args...)
	if err != nil {
		return "", err
	}
	if list := dedupNul(out); len(list) > 0 {
		cmd := exec.Command("git", "update-index", "-z", "--add", "--remove", "--stdin")
		cmd.Dir = r.Root
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
		cmd.Env = append(cmd.Env, env...)
		cmd.Stdin = bytes.NewReader(list)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git update-index: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	out, err = gitOutput(r.Root, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// dedupNul removes repeated entries from a NUL-separated list (ls-files
// reports a deleted file under both --modified and --deleted).
func dedupNul(b []byte) []byte {
	var out bytes.Buffer
	seen := map[string]bool{}
	for _, f := range bytes.Split(b, []byte{0}) {
		if len(f) == 0 || seen[string(f)] {
			continue
		}
		seen[string(f)] = true
		out.Write(f)
		out.WriteByte(0)
	}
	return out.Bytes()
}

// IndexTree returns a tree for the user's real index, used as a best-effort
// baseline when no snapshot was taken before a tool ran.
func (r *Repo) IndexTree() (string, error) {
	out, err := gitOutput(r.Root, nil, "write-tree")
	if err != nil {
		return EmptyTree, err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveTree resolves a revision (such as "HEAD" or "main") to a tree hash.
func (r *Repo) ResolveTree(rev string) (string, error) {
	out, err := gitOutput(r.Root, nil, "rev-parse", "--verify", "--quiet", rev+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("unknown revision %q", rev)
	}
	return strings.TrimSpace(string(out)), nil
}

// Diff lists tracked files that differ between two trees.
func (r *Repo) Diff(from, to string) ([]Change, error) {
	if from == to {
		return nil, nil
	}
	args := append([]string{"diff-tree", "-r", "--no-renames", "--name-status", "-z", from, to, "--"}, r.pathspecs()...)
	out, err := gitOutput(r.Root, nil, args...)
	if err != nil {
		return nil, err
	}
	fields := bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0})
	var changes []Change
	for i := 0; i+1 < len(fields); i += 2 {
		status := string(fields[i])
		if status == "" {
			continue
		}
		changes = append(changes, Change{Status: status[:1], Path: string(fields[i+1])})
	}
	return changes, nil
}

// ReadFile returns the content of path (relative to the root) in tree.
// ok is false if the file does not exist in that tree.
func (r *Repo) ReadFile(tree, path string) (content []byte, ok bool, err error) {
	if tree == "" || tree == EmptyTree {
		return nil, false, nil
	}
	cmd := exec.Command("git", "cat-file", "blob", tree+":"+filepath.ToSlash(path))
	cmd.Dir = r.Root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if strings.Contains(msg, "does not exist") || strings.Contains(msg, "Not a valid object name") ||
			strings.Contains(msg, "exists on disk, but not in") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("git cat-file %s:%s: %v: %s", tree, path, err, strings.TrimSpace(msg))
	}
	return stdout.Bytes(), true, nil
}

// ListFiles lists tracked and untracked (non-ignored) files matching
// patterns, relative to the root. It reflects the most recent Snapshot.
func (r *Repo) ListFiles(patterns []string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	specs := append(append([]string{}, patterns...), defaultExcludes...)
	env := []string{"GIT_INDEX_FILE=" + r.privIndex}
	args := append([]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, specs...)
	out, err := gitOutput(r.Root, env, args...)
	if err != nil {
		return nil, err
	}
	var files []string
	seen := map[string]bool{}
	for _, f := range bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0}) {
		if len(f) == 0 || seen[string(f)] {
			continue
		}
		seen[string(f)] = true
		files = append(files, string(f))
	}
	return files, nil
}

func gitOutput(dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
