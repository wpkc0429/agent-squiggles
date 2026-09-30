//go:build integration

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
	"github.com/wpkc0429/agent-squiggles/internal/delta"
	"github.com/wpkc0429/agent-squiggles/internal/langs"
)

func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	writeAll(t, root, files)
	for _, args := range [][]string{
		{"git", "init", "-q"},
		{"git", "add", "-A"},
		{"git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return root
}

func writeAll(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func requireServer(t *testing.T, l *langs.Language, root string) {
	t.Helper()
	if _, err := langs.Resolve(l, root, config.Language{}, true); err != nil {
		t.Skipf("%s: %v", l.Name, err)
	}
}

type harness struct {
	t    *testing.T
	root string
	e    *Engine
	n    int
}

func newHarness(t *testing.T, root string) *harness {
	cfg := config.Defaults()
	f := false
	cfg.AutoInstall = &f
	e := New(root, cfg, t.Logf)
	e.SetTrusted(true)
	t.Cleanup(e.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e.SessionStart(ctx)
	return &harness{t: t, root: root, e: e}
}

// edit simulates a tool call that writes files (nil content deletes).
func (h *harness) edit(tool, command string, files map[string]*string) *Result {
	h.t.Helper()
	h.n++
	id := "call-" + string(rune('0'+h.n))
	input, _ := json.Marshal(map[string]any{"command": command})
	h.e.PreTool(id, tool, input, h.root)
	for name, body := range files {
		p := filepath.Join(h.root, name)
		if body == nil {
			if err := os.Remove(p); err != nil {
				h.t.Fatal(err)
			}
			continue
		}
		writeAll(h.t, h.root, map[string]string{name: *body})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	res, err := h.e.PostTool(ctx, id, tool, input, h.root)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Logf("check took %v, checked %d files, notices %v", time.Since(start).Round(time.Millisecond), res.Checked, res.Notices)
	for _, d := range res.New {
		h.t.Logf("  new: %s:%d %s", filepath.Base(d.Path), d.Line+1, d.Message)
	}
	return res
}

func str(s string) *string { return &s }

func expect(t *testing.T, got []delta.Diag, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d new diagnostics, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		file, msg, _ := strings.Cut(w, ": ")
		if filepath.Base(got[i].Path) != file || !strings.Contains(got[i].Message, msg) {
			t.Errorf("diag %d = %s: %q, want %s: %q", i, filepath.Base(got[i].Path), got[i].Message, file, msg)
		}
	}
}

func TestGo(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"go.mod":  "module example.com/fx\n\ngo 1.22\n",
		"a/a.go":  "package a\n\nfunc Greet(name string) string { return \"hi \" + name }\n",
		"main.go": "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/fx/a\"\n)\n\nfunc main() { fmt.Println(a.Greet(\"x\")) }\n",
		"bad.go":  "package main\n\nfunc broken() int { return \"s\" }\n",
	})
	requireServer(t, langs.Go, root)
	h := newHarness(t, root)

	// Signature change breaks a caller in another file; the pre-existing
	// error in bad.go must not be reported.
	res := h.edit(ToolApplyPatch, "*** Begin Patch", map[string]*string{
		"a/a.go": str("package a\n\nfunc Greet(name string, n int) string { return \"hi \" + name }\n"),
	})
	expect(t, res.New, "main.go: not enough arguments in call to a.Greet")
	if res.Edited[filepath.Join(root, "main.go")] {
		t.Error("main.go should not be marked as edited")
	}

	// Fixing the caller reports nothing new (warm path).
	res = h.edit(ToolApplyPatch, "*** Begin Patch", map[string]*string{
		"main.go": str("package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/fx/a\"\n)\n\nfunc main() { fmt.Println(a.Greet(\"x\", 1)) }\n"),
	})
	expect(t, res.New)

	// A new file with an error, created by a shell command.
	res = h.edit(ToolBash, "cat > extra.go <<EOF ... EOF", map[string]*string{
		"extra.go": str("package main\n\nfunc extra() { undefinedFn() }\n"),
	})
	expect(t, res.New, "extra.go: undefined: undefinedFn")

	// Deleting a file others depend on.
	res = h.edit(ToolBash, "rm a/a.go", map[string]*string{"a/a.go": nil})
	if len(res.New) == 0 {
		t.Fatal("expected errors after deleting a/a.go")
	}

	// Read-only commands are ignored entirely.
	res = h.edit(ToolBash, "git status && ls", nil)
	expect(t, res.New)
}

func TestGoEditAboveExistingError(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"go.mod":  "module example.com/fx\n\ngo 1.22\n",
		"main.go": "package main\n\nfunc main() {}\n\nfunc broken() int { return \"s\" }\n",
	})
	requireServer(t, langs.Go, root)
	h := newHarness(t, root)
	// Inserting lines above an existing error moves it; it is not new.
	res := h.edit(ToolApplyPatch, "*** Begin Patch", map[string]*string{
		"main.go": str("package main\n\n// Greeting is new.\nconst Greeting = \"hi\"\n\nfunc main() {}\n\nfunc broken() int { return \"s\" }\n"),
	})
	expect(t, res.New)
}

func tsRepo(t *testing.T) string {
	return gitRepo(t, map[string]string{
		"package.json":  "{}\n",
		"tsconfig.json": `{"compilerOptions":{"strict":true,"module":"esnext","target":"es2022","moduleResolution":"bundler","noEmit":true}}`,
		"src/a.ts":      "export function greet(name: string): string { return \"hi \" + name; }\n",
		"src/main.ts":   "import { greet } from \"./a\";\nconsole.log(greet(\"x\"));\n",
		"src/bad.ts":    "const n: number = \"s\";\nexport {};\n",
		".gitignore":    "node_modules\n",
	})
}

func runTS(t *testing.T, root string) {
	requireServer(t, langs.TypeScript, root)
	h := newHarness(t, root)
	res := h.edit(ToolApplyPatch, "*** Begin Patch", map[string]*string{
		"src/a.ts": str("export function greet(name: string, n: number): string { return \"hi \" + name; }\n"),
	})
	expect(t, res.New, "main.ts: Expected 2 arguments, but got 1.")

	// An edit to a file with a pre-existing error only reports the new one.
	res = h.edit(ToolApplyPatch, "*** Begin Patch", map[string]*string{
		"src/bad.ts": str("const n: number = \"s\";\nconst m: string = 1;\nexport {};\n"),
	})
	expect(t, res.New, "bad.ts: Type 'number' is not assignable to type 'string'.")

	// Deleting a module breaks its importers.
	res = h.edit(ToolBash, "rm src/a.ts", map[string]*string{"src/a.ts": nil})
	expect(t, res.New, "main.ts: Cannot find module './a'")
}

func TestTypeScriptNative(t *testing.T) {
	runTS(t, tsRepo(t))
}

func TestTypeScriptProjectTSServer(t *testing.T) {
	ts6 := os.Getenv("TS6_DIR") // a directory containing node_modules/typescript@6
	if ts6 == "" {
		t.Skip("TS6_DIR not set")
	}
	root := tsRepo(t)
	if err := os.Symlink(filepath.Join(ts6, "node_modules"), filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	s, err := langs.Resolve(langs.TypeScript, root, config.Language{}, true)
	if err != nil || s.Name != "typescript-language-server" {
		t.Fatalf("expected typescript-language-server, got %+v, %v", s, err)
	}
	runTS(t, root)
}

func TestPython(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"pyproject.toml":  "[project]\nname = \"fx\"\nversion = \"0\"\n",
		"pkg/__init__.py": "",
		"pkg/a.py":        "def greet(name: str) -> str:\n    return \"hi \" + name\n\n\nx: int = \"s\"\n",
		"main.py":         "from pkg.a import greet\n\nprint(greet(\"x\"))\n",
	})
	requireServer(t, langs.Python, root)
	h := newHarness(t, root)
	res := h.edit(ToolApplyPatch, "*** Begin Patch", map[string]*string{
		"pkg/a.py": str("def greet(name: str, n: int) -> str:\n    return \"hi \" + name\n\n\nx: int = \"s\"\n"),
	})
	expect(t, res.New, "main.py: Argument missing for parameter \"n\"")
}

func TestNonGitApplyPatch(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	writeAll(t, root, map[string]string{
		"go.mod":  "module example.com/fx\n\ngo 1.22\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})
	requireServer(t, langs.Go, root)
	h := newHarness(t, root)
	if h.e.IsGit() {
		t.Fatal("temp dir unexpectedly inside a git repo")
	}
	res := h.edit(ToolApplyPatch, "*** Begin Patch\n*** Update File: main.go\n@@\n-func main() {}\n+func main() { nope() }\n*** End Patch", map[string]*string{
		"main.go": str("package main\n\nfunc main() { nope() }\n"),
	})
	expect(t, res.New, "main.go: undefined: nope")
}
