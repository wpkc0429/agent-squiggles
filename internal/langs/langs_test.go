package langs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wpkc0429/agent-squiggles/internal/config"
)

func touch(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// isolate points server discovery at empty directories and a PATH that
// holds only the given fake tools.
func isolate(t *testing.T, tools ...string) {
	t.Helper()
	t.Setenv("AGENT_SQUIGGLES_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("VIRTUAL_ENV", "")
	bin := t.TempDir()
	for _, tool := range tools {
		touch(t, filepath.Join(bin, tool), "#!/bin/sh\n", 0o755)
	}
	t.Setenv("PATH", bin)
}

func TestProjectTypeScriptNeedsTrust(t *testing.T) {
	isolate(t, "node", "npm")
	root := t.TempDir()
	touch(t, filepath.Join(root, "node_modules", "typescript", "package.json"), `{"version": "7.0.2"}`, 0o644)
	touch(t, filepath.Join(root, "node_modules", ".bin", "tsc"), "#!/bin/sh\n", 0o755)

	s, err := Resolve(TypeScript, root, config.Language{}, true)
	if err != nil || !strings.HasPrefix(s.Command[0], root) {
		t.Fatalf("trusted: got %+v, %v; want the project's tsc", s, err)
	}
	s, err = Resolve(TypeScript, root, config.Language{}, false)
	if err == nil && strings.HasPrefix(s.Command[0], root) {
		t.Fatalf("untrusted project binary was used: %v", s.Command)
	}
	if nie, ok := IsNotInstalled(err); !ok || !nie.Installable {
		t.Fatalf("untrusted: want an installable NotInstalledError, got %v", err)
	}
}

func TestProjectVenvNeedsTrust(t *testing.T) {
	isolate(t, "pyright-langserver")
	root := t.TempDir()
	touch(t, filepath.Join(root, ".venv", "pyvenv.cfg"), "", 0o644)
	touch(t, filepath.Join(root, ".venv", "bin", "basedpyright-langserver"), "#!/bin/sh\n", 0o755)

	s, err := Resolve(Python, root, config.Language{}, true)
	if err != nil || !strings.HasPrefix(s.Command[0], root) || s.Name != "basedpyright" {
		t.Fatalf("trusted: got %+v, %v", s, err)
	}
	py := s.Settings["python"].(map[string]any)
	if py["pythonPath"] != filepath.Join(root, ".venv", "bin", "python") {
		t.Fatalf("pythonPath = %v", py["pythonPath"])
	}
	if py["analysis"].(map[string]any)["typeCheckingMode"] != "standard" {
		t.Fatal("basedpyright without project config should use standard mode")
	}

	s, err = Resolve(Python, root, config.Language{}, false)
	if err != nil || strings.HasPrefix(s.Command[0], root) {
		t.Fatalf("untrusted: got %+v, %v; want the pyright on PATH", s, err)
	}
	if _, set := s.Settings["python"].(map[string]any)["pythonPath"]; set {
		t.Fatal("untrusted project must not set pythonPath to the project's interpreter")
	}
}

func TestGoNotInstalled(t *testing.T) {
	isolate(t)
	_, err := Resolve(Go, t.TempDir(), config.Language{}, true)
	nie, ok := IsNotInstalled(err)
	if !ok || nie.Installable {
		t.Fatalf("want a non-installable NotInstalledError without go on PATH, got %v", err)
	}
}

func TestForPathAndProjectRoot(t *testing.T) {
	if ForPath("a/b.tsx") != TypeScript || ForPath("x.py") != Python || ForPath("m.go") != Go || ForPath("types.d.ts") != nil || ForPath("README.md") != nil {
		t.Fatal("ForPath mismatch")
	}
	ws := t.TempDir()
	touch(t, filepath.Join(ws, "go.work"), "go 1.22\n", 0o644)
	touch(t, filepath.Join(ws, "svc", "go.mod"), "module svc\n", 0o644)
	if got := Go.ProjectRoot(filepath.Join(ws, "svc", "pkg", "x.go"), ws); got != ws {
		t.Fatalf("go.work should win: got %s", got)
	}
	touch(t, filepath.Join(ws, "web", "package.json"), "{}", 0o644)
	if got := TypeScript.ProjectRoot(filepath.Join(ws, "web", "src", "a.ts"), ws); got != filepath.Join(ws, "web") {
		t.Fatalf("nearest package.json: got %s", got)
	}
	if got := Python.ProjectRoot(filepath.Join(ws, "tools", "x.py"), ws); got != ws {
		t.Fatalf("fallback to workspace: got %s", got)
	}
}

func TestRustDetectionAndRoot(t *testing.T) {
	if ForPath("src/main.rs") != Rust || ForPath("crates/core/Cargo.toml") != Rust || ForPath("Cargo.lock") != nil {
		t.Fatal("ForPath mismatch for Rust files")
	}
	ws := t.TempDir()
	touch(t, filepath.Join(ws, "Cargo.toml"), "[workspace]\nmembers = [\"crates/*\"]\n", 0o644)
	touch(t, filepath.Join(ws, "crates", "core", "Cargo.toml"), "[package]\nname = \"core\"\n", 0o644)
	if got := Rust.ProjectRoot(filepath.Join(ws, "crates", "core", "src", "lib.rs"), ws); got != ws {
		t.Fatalf("workspace root should win: got %s", got)
	}
	solo := t.TempDir()
	touch(t, filepath.Join(solo, "tool", "Cargo.toml"), "[package]\nname = \"tool\"\n", 0o644)
	if got := Rust.ProjectRoot(filepath.Join(solo, "tool", "src", "main.rs"), solo); got != filepath.Join(solo, "tool") {
		t.Fatalf("nearest package without a workspace: got %s", got)
	}
	found := false
	for _, p := range Patterns([]*Language{Rust}) {
		found = found || p == "*/Cargo.toml"
	}
	if !found {
		t.Fatal("Patterns should include nested Cargo.toml files")
	}
}

func TestRustNeedsTrust(t *testing.T) {
	isolate(t, "cargo")
	_, err := Resolve(Rust, t.TempDir(), config.Language{}, false)
	var ue *UntrustedError
	if !errors.As(err, &ue) {
		t.Fatalf("untrusted Rust project: got %v, want UntrustedError", err)
	}
	s, err := Resolve(Rust, t.TempDir(), config.Language{}, true)
	if err != nil || !s.Batch || filepath.Base(s.Command[0]) != "cargo" {
		t.Fatalf("trusted Rust project: got %+v, %v", s, err)
	}
	isolate(t)
	_, err = Resolve(Rust, t.TempDir(), config.Language{}, true)
	if nie, ok := IsNotInstalled(err); !ok || nie.Installable {
		t.Fatalf("without cargo: got %v, want a non-installable NotInstalledError", err)
	}
}
