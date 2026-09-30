//go:build integration

package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func managedBin(t *testing.T, rel, name string) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local/share/agent-squiggles/servers", rel)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if lp, err := exec.LookPath(name); err == nil {
		return lp
	}
	t.Skipf("%s not installed", name)
	return ""
}

func writeFiles(t *testing.T, root string, files map[string]string) {
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

func dump(t *testing.T, label string, s *Server, start time.Time) {
	t.Logf("--- %s (%v)", label, time.Since(start).Round(time.Millisecond))
	for path, ds := range s.Diagnostics() {
		for _, d := range ds {
			t.Logf("  %s:%d:%d sev=%d %s: %s", filepath.Base(path), d.Range.Start.Line+1, d.Range.Start.Character+1, d.Severity, d.Source, d.Message)
		}
	}
}

// runSettle opens changed (and deps) with their original text, then applies
// newText to changed and checks that a diagnostic containing want shows up
// in wantFile once the server has settled.
func runSettle(t *testing.T, opts Options, files map[string]string, changed, langID string, deps []string, newText, wantFile, want string) {
	root := t.TempDir()
	writeFiles(t, root, files)
	opts.Root = root
	opts.Logf = t.Logf
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	s, err := Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	t.Logf("started in %v pull=%v", time.Since(start).Round(time.Millisecond), s.SupportsPull())

	changedPath := filepath.Join(root, changed)
	targets := []string{changedPath}
	start = time.Now()
	mark := s.Mark()
	if _, err := s.SetText(changedPath, langID, files[changed]); err != nil {
		t.Fatal(err)
	}
	for _, d := range deps {
		p := filepath.Join(root, d)
		targets = append(targets, p)
		if _, err := s.SetText(p, langID, files[d]); err != nil {
			t.Fatal(err)
		}
	}
	ok := s.Settle(ctx, targets, mark)
	dump(t, "baseline settled="+boolStr(ok), s, start)

	start = time.Now()
	mark = s.Mark()
	if err := os.WriteFile(changedPath, []byte(newText), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetText(changedPath, langID, newText); err != nil {
		t.Fatal(err)
	}
	ok = s.Settle(ctx, targets, mark)
	dump(t, "after settled="+boolStr(ok), s, start)
	if !ok {
		t.Fatal("server did not settle")
	}
	for _, d := range s.Diagnostics()[filepath.Join(root, wantFile)] {
		if strings.Contains(d.Message, want) {
			return
		}
	}
	t.Fatalf("no diagnostic containing %q in %s", want, wantFile)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestSettleGopls(t *testing.T) {
	bin := managedBin(t, "go/bin/gopls", "gopls")
	runSettle(t, Options{Name: "gopls", Command: []string{bin}, Settle: SettleGopls, Quiet: 100 * time.Millisecond},
		map[string]string{
			"go.mod":  "module example.com/fx\n\ngo 1.22\n",
			"a/a.go":  "package a\n\nfunc Greet(name string) string { return \"hi \" + name }\n",
			"main.go": "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/fx/a\"\n)\n\nfunc main() { fmt.Println(a.Greet(\"x\")) }\n",
			"bad.go":  "package main\n\nfunc broken() int { return \"s\" }\n",
		}, "a/a.go", "go", nil,
		"package a\n\nfunc Greet(name string, n int) string { return \"hi \" + name }\n",
		"main.go", "not enough arguments")
}

func tsFiles() map[string]string {
	return map[string]string{
		"package.json":  "{}\n",
		"tsconfig.json": `{"compilerOptions":{"strict":true,"module":"esnext","target":"es2022","moduleResolution":"bundler","noEmit":true}}`,
		"src/a.ts":      "export function greet(name: string): string { return \"hi \" + name; }\n",
		"src/main.ts":   "import { greet } from \"./a\";\nconsole.log(greet(\"x\"));\n",
		"src/bad.ts":    "const n: number = \"s\";\nexport {};\n",
	}
}

const tsNew = "export function greet(name: string, n: number): string { return \"hi \" + name; }\n"

func TestSettlePushTypeScriptLanguageServer(t *testing.T) {
	bin := managedBin(t, "typescript/node_modules/.bin/typescript-language-server", "typescript-language-server")
	ts6 := os.Getenv("TS6_LIB")
	if ts6 == "" {
		t.Skip("TS6_LIB not set")
	}
	runSettle(t, Options{
		Name: "tsls", Command: []string{bin, "--stdio"}, Settle: SettlePush,
		InitializationOptions: map[string]any{"tsserver": map[string]any{"path": ts6}},
	}, tsFiles(), "src/a.ts", "typescript", []string{"src/main.ts"}, tsNew, "src/main.ts", "Expected 2 arguments")
}

func TestSettlePullTypeScriptNative(t *testing.T) {
	bin := managedBin(t, "typescript/node_modules/.bin/tsc", "tsc")
	runSettle(t, Options{Name: "tsgo", Command: []string{bin, "--lsp", "--stdio"}, Settle: SettlePull},
		tsFiles(), "src/a.ts", "typescript", []string{"src/main.ts"}, tsNew, "src/main.ts", "Expected 2 arguments")
}

func pyFiles() map[string]string {
	return map[string]string{
		"pyproject.toml":  "[project]\nname = \"fx\"\nversion = \"0\"\n",
		"pkg/__init__.py": "",
		"pkg/a.py":        "def greet(name: str) -> str:\n    return \"hi \" + name\n",
		"main.py":         "from pkg.a import greet\n\nprint(greet(\"x\"))\n",
		"bad.py":          "x: int = \"s\"\n",
	}
}

func pySettings() map[string]any {
	return map[string]any{"python": map[string]any{"analysis": map[string]any{"diagnosticMode": "openFilesOnly", "typeCheckingMode": "standard"}}}
}

func TestSettlePushPyright(t *testing.T) {
	bin := managedBin(t, "pyright/node_modules/.bin/pyright-langserver", "pyright-langserver")
	runSettle(t, Options{Name: "pyright", Command: []string{bin, "--stdio"}, Settle: SettlePush, Settings: pySettings()},
		pyFiles(), "pkg/a.py", "python", []string{"main.py"},
		"def greet(name: str, n: int) -> str:\n    return \"hi \" + name\n", "main.py", "Argument missing")
}
