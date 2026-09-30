package deps

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func setup(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var all []string
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		all = append(all, p)
	}
	sort.Strings(all)
	return root, all
}

func rels(root string, paths []string) []string {
	var out []string
	for _, p := range paths {
		r, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(r))
	}
	sort.Strings(out)
	return out
}

func TestTypeScriptDependents(t *testing.T) {
	root, all := setup(t, map[string]string{
		"tsconfig.json": `{
			// comments are allowed
			"compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["./src/*"], }, },
		}`,
		"src/lib/greet.ts":       "export function greet() {}\n",
		"src/lib/index.ts":       "export * from './greet';\n",
		"src/app.ts":             "import { greet } from './lib/greet';\n",
		"src/esm.ts":             "import { greet } from \"./lib/greet.js\";\n",
		"src/alias.tsx":          "import { greet } from '@/lib/greet';\n",
		"src/barrel.ts":          "import { greet } from './lib';\n",
		"src/lazy.ts":            "const m = await import('./lib/greet');\n",
		"src/cjs.js":             "const { greet } = require('../src/lib/greet');\n",
		"src/unrelated.ts":       "import { x } from './other';\nimport React from 'react';\n",
		"src/other.ts":           "export const x = 1;\n",
		"src/nested/deep/use.ts": "import { greet } from '../../lib/greet';\n",
	})
	f := NewFinder(root)
	changed := []string{filepath.Join(root, "src/lib/greet.ts")}
	got := rels(root, f.Dependents(TypeScript, all, changed, 0))
	want := []string{"src/alias.tsx", "src/app.ts", "src/cjs.js", "src/esm.ts", "src/lazy.ts", "src/lib/index.ts", "src/nested/deep/use.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dependents = %v\nwant %v", got, want)
	}

	// index files are importable through their directory.
	changed = []string{filepath.Join(root, "src/lib/index.ts")}
	got = rels(root, f.Dependents(TypeScript, all, changed, 0))
	if !reflect.DeepEqual(got, []string{"src/barrel.ts"}) {
		t.Fatalf("barrel dependents = %v", got)
	}

	// The limit keeps the closest files.
	changed = []string{filepath.Join(root, "src/lib/greet.ts")}
	got = rels(root, f.Dependents(TypeScript, all, changed, 1))
	if !reflect.DeepEqual(got, []string{"src/lib/index.ts"}) {
		t.Fatalf("limited dependents = %v", got)
	}
}

func TestPythonDependents(t *testing.T) {
	root, all := setup(t, map[string]string{
		"pkg/__init__.py":      "",
		"pkg/a.py":             "def greet(): ...\n",
		"pkg/sibling.py":       "from .a import greet\n",
		"pkg/sub/__init__.py":  "",
		"pkg/sub/deep.py":      "from ..a import greet\n",
		"main.py":              "from pkg.a import greet\n",
		"star.py":              "import pkg.a as a\n",
		"from_pkg.py":          "from pkg import (\n    a,\n    sub,\n)\n",
		"unrelated.py":         "import os\nfrom pkg import sub\n",
		"src/proj/__init__.py": "",
		"src/proj/core.py":     "X = 1\n",
		"src/proj/use.py":      "from proj.core import X\n",
	})
	f := NewFinder(root)
	got := rels(root, f.Dependents(Python, all, []string{filepath.Join(root, "pkg/a.py")}, 0))
	want := []string{"from_pkg.py", "main.py", "pkg/sibling.py", "pkg/sub/deep.py", "star.py"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dependents = %v\nwant %v", got, want)
	}
	got = rels(root, f.Dependents(Python, all, []string{filepath.Join(root, "src/proj/core.py")}, 0))
	if !reflect.DeepEqual(got, []string{"src/proj/use.py"}) {
		t.Fatalf("src-layout dependents = %v", got)
	}
}

func TestResolveRelative(t *testing.T) {
	cases := [][3]string{
		{"pkg.sub", ".mod", "pkg.sub.mod"},
		{"pkg.sub", "..mod", "pkg.mod"},
		{"pkg.sub", "..", "pkg"},
		{"pkg", "...x", ""},
	}
	for _, c := range cases {
		if got := resolveRelative(c[0], c[1]); got != c[2] {
			t.Errorf("resolveRelative(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestCacheInvalidation(t *testing.T) {
	root, all := setup(t, map[string]string{
		"a.ts": "export const a = 1;\n",
		"b.ts": "export const b = 2;\n",
	})
	f := NewFinder(root)
	changed := []string{filepath.Join(root, "a.ts")}
	if got := f.Dependents(TypeScript, all, changed, 0); len(got) != 0 {
		t.Fatalf("unexpected dependents %v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "b.ts"), []byte("import { a } from './a';\nexport const b = a;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := rels(root, f.Dependents(TypeScript, all, changed, 0)); !reflect.DeepEqual(got, []string{"b.ts"}) {
		t.Fatalf("after edit dependents = %v", got)
	}
}
