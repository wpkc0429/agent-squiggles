// Package langs defines the supported languages, how to find (or install)
// a language server for each, and how to configure it.
package langs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wpkc0429/agent-squiggles/internal/config"
	"github.com/wpkc0429/agent-squiggles/internal/deps"
	"github.com/wpkc0429/agent-squiggles/internal/lsp"
	"github.com/wpkc0429/agent-squiggles/internal/paths"
)

// Language is a supported language.
type Language struct {
	ID         string
	Name       string
	Extensions []string
	// Markers identify a project root; the nearest one above a file wins.
	Markers []string
	// Deps is the import syntax scanned to find dependents, or "" when the
	// server already reports diagnostics for files that are not open.
	Deps deps.Kind
}

var (
	Go = &Language{
		ID: "go", Name: "Go",
		Extensions: []string{".go"},
		Markers:    []string{"go.work", "go.mod"},
	}
	TypeScript = &Language{
		ID: "typescript", Name: "TypeScript/JavaScript",
		Extensions: []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"},
		Markers:    []string{"tsconfig.json", "jsconfig.json", "package.json"},
		Deps:       deps.TypeScript,
	}
	Python = &Language{
		ID: "python", Name: "Python",
		Extensions: []string{".py", ".pyi"},
		Markers:    []string{"pyrightconfig.json", "pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile"},
		Deps:       deps.Python,
	}
)

// All returns every supported language.
func All() []*Language { return []*Language{Go, TypeScript, Python} }

// ByID returns the language with the given ID, or nil.
func ByID(id string) *Language {
	for _, l := range All() {
		if l.ID == id {
			return l
		}
	}
	return nil
}

// ForPath returns the language of a file, or nil if unsupported.
func ForPath(path string) *Language {
	if strings.HasSuffix(path, ".d.ts") {
		return nil // declaration files are rarely edited and noisy to check
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, l := range All() {
		for _, e := range l.Extensions {
			if e == ext {
				return l
			}
		}
	}
	return nil
}

// Patterns returns git pathspecs matching the languages' files.
func Patterns(ls []*Language) []string {
	var out []string
	for _, l := range ls {
		for _, e := range l.Extensions {
			out = append(out, "*"+e)
		}
	}
	return out
}

// LanguageID returns the LSP languageId for a file.
func (l *Language) LanguageID(path string) string {
	switch l {
	case TypeScript:
		switch strings.ToLower(filepath.Ext(path)) {
		case ".tsx":
			return "typescriptreact"
		case ".js", ".mjs", ".cjs":
			return "javascript"
		case ".jsx":
			return "javascriptreact"
		}
		return "typescript"
	default:
		return l.ID
	}
}

// ProjectRoot returns the nearest directory at or above file's directory
// (but not above workspace) that contains one of the language's markers,
// or workspace if none does.
func (l *Language) ProjectRoot(file, workspace string) string {
	dir := filepath.Dir(file)
	for {
		for _, m := range l.Markers {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				// Prefer go.work over a nested go.mod for Go.
				if l == Go && m == "go.mod" {
					if w := findUp(dir, workspace, "go.work"); w != "" {
						return w
					}
				}
				return dir
			}
		}
		if dir == workspace || !strings.HasPrefix(dir, workspace) {
			return workspace
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return workspace
		}
		dir = parent
	}
}

func findUp(dir, stop, name string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return dir
		}
		if dir == stop {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Server describes how to run a language server for one project root.
type Server struct {
	Name             string
	Command          []string
	InitOptions      any
	Settings         map[string]any
	Settle           lsp.SettleStrategy
	Quiet            time.Duration
	FirstPublishWait time.Duration
}

// Options converts a Server into lsp.Options for a root.
func (s *Server) Options(root string, logf func(string, ...any)) lsp.Options {
	return lsp.Options{
		Name:                  s.Name,
		Command:               s.Command,
		Root:                  root,
		InitializationOptions: s.InitOptions,
		Settings:              s.Settings,
		Settle:                s.Settle,
		Quiet:                 s.Quiet,
		FirstPublishWait:      s.FirstPublishWait,
		Logf:                  logf,
	}
}

// NotInstalledError reports a missing language server.
type NotInstalledError struct {
	Lang *Language
	// Installable reports whether Install can fix this automatically.
	Installable bool
	// Hint is a manual install instruction.
	Hint string
}

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("no %s language server found (%s)", e.Lang.Name, e.Hint)
}

// IsNotInstalled reports whether err is a NotInstalledError.
func IsNotInstalled(err error) (*NotInstalledError, bool) {
	var nie *NotInstalledError
	ok := errors.As(err, &nie)
	return nie, ok
}

// Resolve finds a language server for projectRoot. Binaries shipped
// inside the project (node_modules, virtualenvs) are only used when the
// project is trusted.
func Resolve(l *Language, projectRoot string, lc config.Language, trusted bool) (*Server, error) {
	var (
		s   *Server
		err error
	)
	switch l {
	case Go:
		s, err = resolveGo(lc)
	case TypeScript:
		s, err = resolveTypeScript(projectRoot, lc, trusted)
	case Python:
		s, err = resolvePython(projectRoot, lc, trusted)
	default:
		return nil, fmt.Errorf("unsupported language %q", l.ID)
	}
	if err != nil {
		return nil, err
	}
	if len(lc.Settings) > 0 {
		if s.Settings == nil {
			s.Settings = map[string]any{}
		}
		config.MergeMaps(s.Settings, lc.Settings)
	}
	return s, nil
}

// custom builds a Server from a user-configured command line.
func custom(lc config.Language) *Server {
	s := &Server{Name: filepath.Base(lc.Command[0]), Command: lc.Command, Settle: lsp.SettlePush, Quiet: 300 * time.Millisecond}
	if s.Name == "gopls" {
		s.Settle, s.Quiet = lsp.SettleGopls, 100*time.Millisecond
	}
	return s
}

func resolveGo(lc config.Language) (*Server, error) {
	if len(lc.Command) > 0 {
		return custom(lc), nil
	}
	home, _ := os.UserHomeDir()
	bin := firstExisting(
		lookPath("gopls"),
		filepath.Join(paths.ServersDir(), "go", "bin", "gopls"),
		filepath.Join(home, "go", "bin", "gopls"),
	)
	if bin == "" {
		return nil, &NotInstalledError{Lang: Go, Installable: lookPath("go") != "", Hint: "go install golang.org/x/tools/gopls@latest"}
	}
	return &Server{
		Name:     "gopls",
		Command:  []string{bin},
		Settings: map[string]any{"gopls": map[string]any{}},
		Settle:   lsp.SettleGopls,
		Quiet:    100 * time.Millisecond,
	}, nil
}

// localTypeScript finds the project's own typescript package.
func localTypeScript(projectRoot string) (dir string, major int) {
	d := projectRoot
	for {
		pkg := filepath.Join(d, "node_modules", "typescript")
		if data, err := os.ReadFile(filepath.Join(pkg, "package.json")); err == nil {
			var meta struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(data, &meta) == nil {
				majorStr, _, _ := strings.Cut(meta.Version, ".")
				major, _ = strconv.Atoi(majorStr)
				return pkg, major
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", 0
		}
		d = parent
	}
}

func resolveTypeScript(projectRoot string, lc config.Language, trusted bool) (*Server, error) {
	if len(lc.Command) > 0 {
		return custom(lc), nil
	}
	managed := filepath.Join(paths.ServersDir(), "typescript", "node_modules")
	notInstalled := &NotInstalledError{Lang: TypeScript, Installable: lookPath("npm") != "", Hint: "npm install -g typescript typescript-language-server"}

	native := func(bin, name string) *Server {
		return &Server{Name: name, Command: []string{bin, "--lsp", "--stdio"}, Settle: lsp.SettlePull}
	}
	if pkg, major := localTypeScript(projectRoot); pkg != "" && trusted {
		if major >= 7 {
			// TypeScript 7 (the native port) ships its own language server.
			if bin := firstExisting(filepath.Join(filepath.Dir(pkg), ".bin", "tsc")); bin != "" && lookPath("node") != "" {
				return native(bin, "tsc --lsp (project)"), nil
			}
		} else if _, err := os.Stat(filepath.Join(pkg, "lib", "tsserver.js")); err == nil {
			// Older TypeScript: run typescript-language-server on the project's tsserver.
			tls := firstExisting(lookPath("typescript-language-server"), filepath.Join(managed, ".bin", "typescript-language-server"))
			if tls == "" {
				return nil, notInstalled
			}
			return &Server{
				Name:             "typescript-language-server",
				Command:          []string{tls, "--stdio"},
				InitOptions:      map[string]any{"tsserver": map[string]any{"path": filepath.Join(pkg, "lib")}},
				Settle:           lsp.SettlePush,
				Quiet:            300 * time.Millisecond,
				FirstPublishWait: 3 * time.Second,
			}, nil
		}
	}
	if bin := firstExisting(filepath.Join(managed, ".bin", "tsc")); bin != "" && lookPath("node") != "" {
		return native(bin, "tsc --lsp"), nil
	}
	if bin := lookPath("tsgo"); bin != "" {
		return native(bin, "tsgo --lsp"), nil
	}
	return nil, notInstalled
}

func hasPyrightConfig(projectRoot string) bool {
	if _, err := os.Stat(filepath.Join(projectRoot, "pyrightconfig.json")); err == nil {
		return true
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, "pyproject.toml"))
	return err == nil && (strings.Contains(string(data), "[tool.pyright]") || strings.Contains(string(data), "[tool.basedpyright]"))
}

// venvDir returns the project's virtualenv directory, if any.
func venvDir(projectRoot string) string {
	for _, name := range []string{".venv", "venv", ".env", "env"} {
		d := filepath.Join(projectRoot, name)
		if _, err := os.Stat(filepath.Join(d, "pyvenv.cfg")); err == nil {
			return d
		}
	}
	if v := os.Getenv("VIRTUAL_ENV"); v != "" {
		return v
	}
	return ""
}

func resolvePython(projectRoot string, lc config.Language, trusted bool) (*Server, error) {
	venv := ""
	if trusted {
		venv = venvDir(projectRoot)
	}
	var bin, name string
	if len(lc.Command) > 0 {
		bin, name = lc.Command[0], filepath.Base(lc.Command[0])
	} else {
		var candidates [][2]string
		if venv != "" {
			candidates = append(candidates,
				[2]string{filepath.Join(venv, "bin", "basedpyright-langserver"), "basedpyright"},
				[2]string{filepath.Join(venv, "bin", "pyright-langserver"), "pyright"})
		}
		candidates = append(candidates,
			[2]string{lookPath("basedpyright-langserver"), "basedpyright"},
			[2]string{lookPath("pyright-langserver"), "pyright"},
			[2]string{filepath.Join(paths.ServersDir(), "pyright", "node_modules", ".bin", "pyright-langserver"), "pyright"},
			[2]string{filepath.Join(paths.ServersDir(), "basedpyright", "bin", "basedpyright-langserver"), "basedpyright"})
		for _, c := range candidates {
			if c[0] != "" && firstExisting(c[0]) != "" {
				bin, name = c[0], c[1]
				break
			}
		}
		if bin == "" {
			return nil, &NotInstalledError{Lang: Python, Installable: lookPath("npm") != "" || lookPath("python3") != "", Hint: "npm install -g pyright, or pip install basedpyright"}
		}
	}
	analysis := map[string]any{"diagnosticMode": "openFilesOnly"}
	if strings.Contains(name, "basedpyright") && !hasPyrightConfig(projectRoot) {
		// basedpyright defaults to a much stricter mode than pyright.
		analysis["typeCheckingMode"] = "standard"
	}
	python := map[string]any{"analysis": analysis}
	if venv != "" {
		python["pythonPath"] = filepath.Join(venv, "bin", "python")
	}
	command := []string{bin, "--stdio"}
	if len(lc.Command) > 0 {
		command = lc.Command
	}
	return &Server{
		Name:     name,
		Command:  command,
		Settings: map[string]any{"python": python, "basedpyright": map[string]any{"analysis": analysis}},
		Settle:   lsp.SettlePush,
		Quiet:    300 * time.Millisecond,
	}, nil
}

func lookPath(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func firstExisting(candidates ...string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}
