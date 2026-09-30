package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestServeProtocol(t *testing.T) {
	// Keep sockets short and everything inside the test's temp dirs.
	t.Setenv("AGENT_SQUIGGLES_HOME", t.TempDir())
	runtimeDir, err := os.MkdirTemp("", "as")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	t.Setenv("AGENT_SQUIGGLES_RUNTIME_DIR", runtimeDir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- Serve(root, time.Minute) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := Call(root, Request{Op: OpStatus}, time.Second, false); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, err := Call(root, Request{Op: OpStatus}, time.Second, false)
	if err != nil || resp.Status == nil || resp.Mode != "context" {
		t.Fatalf("status: %+v, %v", resp, err)
	}

	// A second server for the same root must bow out.
	if err := Serve(root, time.Minute); err != nil {
		t.Fatalf("second Serve: %v", err)
	}

	input, _ := json.Marshal(map[string]string{"command": "touch notes.txt"})
	for _, ev := range []string{EventPreToolUse, EventPostToolUse} {
		resp, err := Call(root, Request{Op: OpHook, Event: ev, ToolUseID: "c1", ToolName: "Bash", ToolInput: input, Cwd: root}, 5*time.Second, false)
		if err != nil || resp.Error != "" || resp.Context != "" {
			t.Fatalf("%s: %+v, %v", ev, resp, err)
		}
	}

	resp, err = Call(root, Request{Op: "bogus"}, time.Second, false)
	if err != nil || resp.Error == "" {
		t.Fatalf("expected error for unknown op, got %+v, %v", resp, err)
	}

	if _, err := Call(root, Request{Op: OpStop}, time.Second, false); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, filepath.Base(root))); err == nil {
		t.Fatal("unexpected file left behind")
	}
}
