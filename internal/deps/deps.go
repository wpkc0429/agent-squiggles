// Package deps finds files that import a changed file.
//
// Language servers such as typescript-language-server and pyright only
// report diagnostics for documents the client has opened. To catch an edit
// that breaks a caller in another file, agent-squiggles opens the files
// that import the edited one. This package finds them with a fast,
// cached, regex-based import scan; it favors speed over completeness.
package deps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind selects the import syntax to scan for.
type Kind string

const (
	TypeScript Kind = "typescript"
	Python     Kind = "python"
)

// maxFileSize skips generated or vendored giants.
const maxFileSize = 1 << 20

var tsExts = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}

var (
	tsImportRe = regexp.MustCompile(`(?m)(?:\bfrom\s*|\bimport\s*\(\s*|\brequire\s*\(\s*|^\s*import\s+|\bexport\s+\*\s*from\s*)['"]([^'"\n]+)['"]`)
	pyImportRe = regexp.MustCompile(`(?m)^[ \t]*(?:from[ \t]+([.\w]+)[ \t]+import[ \t]+(\([^)]*\)|[^\n#]+)|import[ \t]+([^\n#]+))`)
)

type entry struct {
	mod     time.Time
	size    int64
	imports []string
}

// Finder caches parsed imports per file.
type Finder struct {
	Root string

	mu     sync.Mutex
	cache  map[string]entry
	tsPath *tsPaths
	tsRead bool
}

// NewFinder returns a Finder for a workspace root.
func NewFinder(root string) *Finder {
	return &Finder{Root: root, cache: map[string]entry{}}
}

// Dependents returns up to limit files among candidates (absolute paths)
// that import any of changed (absolute paths). Files in changed are never
// returned. Results closest to the changed files come first.
func (f *Finder) Dependents(kind Kind, candidates, changed []string, limit int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	changedSet := map[string]bool{}
	for _, c := range changed {
		changedSet[c] = true
	}
	var keys map[string]bool
	switch kind {
	case TypeScript:
		keys = tsModuleKeys(changed)
	case Python:
		keys = f.pyModuleNames(changed)
	default:
		return nil
	}
	if len(keys) == 0 {
		return nil
	}
	var found []string
	for _, c := range candidates {
		if changedSet[c] {
			continue
		}
		imports := f.imports(kind, c)
		for _, imp := range imports {
			if keys[imp] {
				found = append(found, c)
				break
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		return distance(found[i], changed) < distance(found[j], changed)
	})
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	return found
}

// imports returns the normalized module keys a file imports.
func (f *Finder) imports(kind Kind, path string) []string {
	st, err := os.Stat(path)
	if err != nil || st.Size() > maxFileSize {
		return nil
	}
	if e, ok := f.cache[path]; ok && e.mod.Equal(st.ModTime()) && e.size == st.Size() {
		return e.imports
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var imps []string
	switch kind {
	case TypeScript:
		imps = f.tsImports(path, string(data))
	case Python:
		imps = f.pyImports(path, string(data))
	}
	f.cache[path] = entry{mod: st.ModTime(), size: st.Size(), imports: imps}
	return imps
}

// --- TypeScript / JavaScript ---

// tsModuleKeys maps changed files to the extensionless paths an import
// specifier would resolve to ("src/a.ts" -> "src/a"; "src/x/index.ts" ->
// "src/x/index" and "src/x").
func tsModuleKeys(changed []string) map[string]bool {
	keys := map[string]bool{}
	for _, c := range changed {
		k := stripTSExt(c)
		keys[k] = true
		if filepath.Base(k) == "index" {
			keys[filepath.Dir(k)] = true
		}
	}
	return keys
}

func stripTSExt(p string) string {
	if strings.HasSuffix(p, ".d.ts") {
		return strings.TrimSuffix(p, ".d.ts")
	}
	for _, e := range tsExts {
		if strings.HasSuffix(p, e) {
			return strings.TrimSuffix(p, e)
		}
	}
	return p
}

func (f *Finder) tsImports(path, src string) []string {
	dir := filepath.Dir(path)
	var out []string
	for _, m := range tsImportRe.FindAllStringSubmatch(src, -1) {
		spec := m[1]
		var base string
		switch {
		case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || spec == "." || spec == "..":
			base = filepath.Join(dir, spec)
		default:
			base = f.resolveTSAlias(spec)
			if base == "" {
				continue
			}
		}
		out = append(out, stripTSExt(filepath.Clean(base)))
	}
	return out
}

type tsPaths struct {
	baseDir string
	rules   []tsPathRule
}

type tsPathRule struct {
	prefix, suffix string
	targets        []string
}

// resolveTSAlias applies compilerOptions.paths from the root tsconfig or
// jsconfig ("@/*" -> "./src/*"). Only the first target is used.
func (f *Finder) resolveTSAlias(spec string) string {
	if !f.tsRead {
		f.tsRead = true
		f.tsPath = loadTSPaths(f.Root)
	}
	if f.tsPath == nil {
		return ""
	}
	for _, r := range f.tsPath.rules {
		if !strings.HasPrefix(spec, r.prefix) || !strings.HasSuffix(spec, r.suffix) || len(spec) < len(r.prefix)+len(r.suffix) {
			continue
		}
		star := spec[len(r.prefix) : len(spec)-len(r.suffix)]
		if len(r.targets) == 0 {
			continue
		}
		target := strings.Replace(r.targets[0], "*", star, 1)
		return filepath.Join(f.tsPath.baseDir, target)
	}
	return ""
}

func loadTSPaths(root string) *tsPaths {
	for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		var cfg struct {
			CompilerOptions struct {
				BaseURL string              `json:"baseUrl"`
				Paths   map[string][]string `json:"paths"`
			} `json:"compilerOptions"`
		}
		if json.Unmarshal(stripJSONC(data), &cfg) != nil || len(cfg.CompilerOptions.Paths) == 0 {
			continue
		}
		p := &tsPaths{baseDir: filepath.Join(root, cfg.CompilerOptions.BaseURL)}
		for pattern, targets := range cfg.CompilerOptions.Paths {
			prefix, suffix, _ := strings.Cut(pattern, "*")
			p.rules = append(p.rules, tsPathRule{prefix: prefix, suffix: suffix, targets: targets})
		}
		// Longest prefix wins, as in TypeScript.
		sort.Slice(p.rules, func(i, j int) bool { return len(p.rules[i].prefix) > len(p.rules[j].prefix) })
		return p
	}
	return nil
}

// stripJSONC removes comments and trailing commas so tsconfig files parse
// as JSON.
func stripJSONC(data []byte) []byte {
	var out []byte
	inStr := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inStr {
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				i++
				out = append(out, data[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i++
		case c == ',':
			j := i + 1
			for j < len(data) && strings.ContainsRune(" \t\r\n", rune(data[j])) {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// --- Python ---

// pyRoots are the directories module names are computed relative to.
func (f *Finder) pyRoots() []string {
	roots := []string{f.Root}
	if st, err := os.Stat(filepath.Join(f.Root, "src")); err == nil && st.IsDir() {
		roots = append(roots, filepath.Join(f.Root, "src"))
	}
	return roots
}

func (f *Finder) pyModuleNames(changed []string) map[string]bool {
	names := map[string]bool{}
	for _, c := range changed {
		for _, n := range pyModuleName(f.pyRoots(), c) {
			names[n] = true
		}
	}
	return names
}

// pyModuleName returns the dotted module names a file can be imported as.
func pyModuleName(roots []string, path string) []string {
	var out []string
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = strings.TrimSuffix(strings.TrimSuffix(rel, ".py"), ".pyi")
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if parts[len(parts)-1] == "__init__" {
			parts = parts[:len(parts)-1]
		}
		if len(parts) > 0 {
			out = append(out, strings.Join(parts, "."))
		}
	}
	return out
}

func (f *Finder) pyImports(path, src string) []string {
	pkgs := pyModuleName(f.pyRoots(), filepath.Join(filepath.Dir(path), "__init__.py"))
	var out []string
	for _, m := range pyImportRe.FindAllStringSubmatch(src, -1) {
		if m[3] != "" {
			for _, part := range strings.Split(m[3], ",") {
				name := strings.Fields(part)
				if len(name) > 0 {
					out = append(out, name[0])
				}
			}
			continue
		}
		mods := []string{m[1]}
		if strings.HasPrefix(m[1], ".") {
			mods = nil
			for _, pkg := range pkgs {
				if abs := resolveRelative(pkg, m[1]); abs != "" {
					mods = append(mods, abs)
				}
			}
			if len(pkgs) == 0 {
				// A file at the root: "from .x import y" is not valid, but
				// "from . import x" style still resolves to top-level names.
				if abs := resolveRelative("", m[1]); abs != "" {
					mods = append(mods, abs)
				}
			}
		}
		names := strings.Trim(m[2], "() \t\r\n")
		for _, mod := range mods {
			if mod != "" {
				out = append(out, mod)
			}
			for _, part := range strings.Split(names, ",") {
				fields := strings.Fields(part)
				if len(fields) == 0 || fields[0] == "*" {
					continue
				}
				if mod == "" {
					out = append(out, fields[0])
				} else {
					out = append(out, mod+"."+fields[0])
				}
			}
		}
	}
	return out
}

// resolveRelative turns ("pkg.sub", "..mod") into "pkg.mod".
func resolveRelative(pkg, rel string) string {
	level := len(rel) - len(strings.TrimLeft(rel, "."))
	rest := rel[level:]
	var parts []string
	if pkg != "" {
		parts = strings.Split(pkg, ".")
	}
	up := level - 1
	if up > len(parts) {
		return ""
	}
	parts = parts[:len(parts)-up]
	if rest != "" {
		parts = append(parts, rest)
	}
	return strings.Join(parts, ".")
}

// distance ranks a candidate by how far its directory is from the nearest
// changed file.
func distance(path string, changed []string) int {
	best := 1 << 30
	for _, c := range changed {
		rel, err := filepath.Rel(filepath.Dir(c), filepath.Dir(path))
		if err != nil {
			continue
		}
		n := 0
		if rel != "." {
			n = len(strings.Split(rel, string(filepath.Separator)))
		}
		if n < best {
			best = n
		}
	}
	return best
}
