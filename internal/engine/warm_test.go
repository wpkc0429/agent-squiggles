package engine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wpkc0429/agent-squiggles/internal/config"
	"github.com/wpkc0429/agent-squiggles/internal/lsp"
)

// fakeLSP is a language server whose gopls.diagnose_files never finishes
// in time, like gopls loading a big module on a cold build cache.
type fakeLSP struct{ delay time.Duration }

func (fakeLSP) Notify(method string, _ json.RawMessage) {
	if method == "exit" {
		os.Exit(0)
	}
}

func (f fakeLSP) Request(method string, _ json.RawMessage) (any, *lsp.ResponseError) {
	switch method {
	case "initialize":
		return map[string]any{"capabilities": map[string]any{}}, nil
	case "workspace/executeCommand":
		time.Sleep(f.delay)
	}
	return nil, nil
}

// TestHelperLSP is not a test: it is the fake server process, started by
// the test below through a symlink named gopls.
func TestHelperLSP(t *testing.T) {
	d := os.Getenv("AGENT_SQUIGGLES_FAKE_LSP_DELAY")
	if d == "" {
		t.Skip("helper process")
	}
	delay, _ := time.ParseDuration(d)
	_ = lsp.NewConn(os.Stdin, os.Stdout, fakeLSP{delay}).Run()
	os.Exit(0)
}

// A slow initial load must not hold up the agent: in an evaluation run, a
// cold gopls load kept PreToolUse and PostToolUse waiting for minutes.
func TestSlowWarmupDoesNotBlockEdits(t *testing.T) {
	defer func(old time.Duration) { warmWait = old }(warmWait)
	warmWait = 300 * time.Millisecond
	t.Setenv("AGENT_SQUIGGLES_FAKE_LSP_DELAY", "4s")
	t.Setenv("AGENT_SQUIGGLES_HOME", t.TempDir())

	bin := filepath.Join(t.TempDir(), "gopls")
	if err := os.Symlink(os.Args[0], bin); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	for name, body := range map[string]string{"go.mod": "module x\n\ngo 1.22\n", "main.go": "package main\n\nfunc main() {}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	cfg := config.Defaults()
	off := false
	cfg.AutoInstall = &off
	cfg.Languages["go"] = config.Language{Command: []string{bin, "-test.run=^TestHelperLSP$"}}
	e := New(root, cfg, t.Logf)
	e.SetTrusted(true)
	t.Cleanup(e.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go e.SessionStart(ctx) // the daemon also warms up in the background
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.checkMu.Lock()
		warming := false
		for _, entry := range e.servers {
			warming = warming || entry.warming != nil
		}
		e.checkMu.Unlock()
		if warming {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("warm-up never started")
		}
		time.Sleep(20 * time.Millisecond)
	}

	input, _ := json.Marshal(map[string]string{"command": "*** Begin Patch"})
	start := time.Now()
	e.PreTool("c1", ToolApplyPatch, input, root)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("PreToolUse waited %v for the warm-up", d)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() { nope() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	res, err := e.PostTool(ctx, "c1", ToolApplyPatch, input, root)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("PostToolUse waited %v for the warm-up", d)
	}
	if !strings.Contains(strings.Join(res.Notices, " "), "still loading") {
		t.Fatalf("expected a still-loading notice, got %v", res.Notices)
	}
}
