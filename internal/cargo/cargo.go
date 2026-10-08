// Package cargo checks Rust projects with `cargo check`.
//
// Rust is handled differently from the other languages. rust-analyzer's own
// diagnostics only cover open files and miss much of what rustc reports, and
// its cargo-based diagnostics read files from disk, so they cannot be shown
// an old version of a file. Running cargo directly gives exactly rustc's
// errors across every file and crate in the workspace; the engine keeps the
// previous run's results as the baseline instead of re-checking the old text.
package cargo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/wpkc0429/agent-squiggles/internal/delta"
)

// TargetSubdir is where checks write build output, inside the project's
// target directory. A separate directory keeps cargo from blocking on the
// lock held by the agent's own `cargo build` or `cargo test`.
const TargetSubdir = "agent-squiggles"

// Check runs `cargo check` in root and returns diagnostics keyed by
// absolute path. Only files inside root are kept.
func Check(ctx context.Context, cargo, root string) (map[string][]delta.Diag, error) {
	cmd := exec.CommandContext(ctx, cargo, "check", "--workspace", "--all-targets", "--message-format=json", "--quiet")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"CARGO_TARGET_DIR="+filepath.Join(root, "target", TargetSubdir),
		"CARGO_TERM_COLOR=never",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return nil, fmt.Errorf("run cargo: %w", err)
	}
	return Parse(root, stdout.Bytes(), stderr.Bytes(), err != nil), nil
}

type message struct {
	Reason  string `json:"reason"`
	Message struct {
		Message string `json:"message"`
		Level   string `json:"level"`
		Code    *struct {
			Code string `json:"code"`
		} `json:"code"`
		Spans []struct {
			FileName    string  `json:"file_name"`
			LineStart   int     `json:"line_start"`
			ColumnStart int     `json:"column_start"`
			IsPrimary   bool    `json:"is_primary"`
			Label       *string `json:"label"`
		} `json:"spans"`
	} `json:"message"`
}

// Parse converts cargo's JSON output into diagnostics. failed reports
// whether cargo exited unsuccessfully; when it did without reporting any
// compiler error (a broken Cargo.toml, a dependency that cannot be
// resolved), the error from stderr becomes a diagnostic.
func Parse(root string, stdout, stderr []byte, failed bool) map[string][]delta.Diag {
	out := map[string][]delta.Diag{}
	seen := map[string]bool{}
	add := func(d delta.Diag) {
		key := fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%s\x00%s", d.Path, d.Line, d.Col, d.Severity, d.Code, d.Message)
		if seen[key] {
			return // the same error comes once per target (lib, lib test, ...)
		}
		seen[key] = true
		out[d.Path] = append(out[d.Path], d)
	}
	errorsSeen := false
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Reason != "compiler-message" {
			continue
		}
		var sev int
		switch m.Message.Level {
		case "error":
			sev = 1
		case "warning":
			sev = 2
		default:
			continue // notes, help, "aborting due to" summaries
		}
		for _, s := range m.Message.Spans {
			if !s.IsPrimary || strings.HasPrefix(s.FileName, "<") {
				continue
			}
			path := s.FileName
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			if !inside(root, path) {
				continue // errors in registry dependencies are not ours
			}
			msg := m.Message.Message
			if s.Label != nil && *s.Label != "" {
				msg += ": " + *s.Label
			}
			d := delta.Diag{Path: path, Line: s.LineStart - 1, Col: s.ColumnStart - 1, Severity: sev, Source: "rustc", Message: msg}
			if m.Message.Code != nil {
				d.Code = m.Message.Code.Code
			}
			add(d)
			if sev == 1 {
				errorsSeen = true
			}
			break
		}
	}
	if failed && !errorsSeen {
		if d, ok := stderrError(root, stderr); ok {
			add(d)
		}
	}
	return out
}

var locationRe = regexp.MustCompile(`-->\s+(.+?):(\d+):(\d+)`)

// stderrError extracts the first "error: ..." from cargo's stderr, with its
// "--> file:line:col" location when there is one.
func stderrError(root string, stderr []byte) (delta.Diag, bool) {
	lines := strings.Split(string(stderr), "\n")
	for i, line := range lines {
		msg, ok := strings.CutPrefix(strings.TrimSpace(line), "error: ")
		if !ok || strings.HasPrefix(msg, "could not compile") {
			continue
		}
		d := delta.Diag{Path: filepath.Join(root, "Cargo.toml"), Severity: 1, Source: "cargo", Message: msg}
		for _, next := range lines[i+1 : min(len(lines), i+4)] {
			if m := locationRe.FindStringSubmatch(next); m != nil {
				p := m[1]
				if !filepath.IsAbs(p) {
					p = filepath.Join(root, p)
				}
				if inside(root, p) {
					d.Path = p
					l, _ := strconv.Atoi(m[2])
					c, _ := strconv.Atoi(m[3])
					d.Line, d.Col = l-1, c-1
				}
				break
			}
		}
		return d, true
	}
	return delta.Diag{}, false
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
