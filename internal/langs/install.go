package langs

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/wpkc0429/agent-squiggles/internal/paths"
)

// InstallPlan describes how Install would set up a language server.
func InstallPlan(l *Language) (string, error) {
	steps, err := installSteps(l)
	if err != nil {
		return "", err
	}
	var out string
	for i, s := range steps {
		if i > 0 {
			out += " && "
		}
		out += s.describe()
	}
	return out, nil
}

// Install installs a language server for l into the agent-squiggles data
// directory. It never touches global package installations. Concurrent
// installs of the same language (from several daemons) are serialized.
func Install(ctx context.Context, l *Language, log io.Writer) error {
	steps, err := installSteps(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.ServersDir(), 0o755); err != nil {
		return err
	}
	lock, err := paths.WaitLock(filepath.Join(paths.ServersDir(), ".install-"+l.ID+".lock"))
	if err != nil {
		return err
	}
	defer lock.Unlock()
	// Another process may have finished the install while we waited.
	if ManagedInstalled(l) {
		return nil
	}
	for _, s := range steps {
		fmt.Fprintf(log, "$ %s\n", s.describe())
		cmd := exec.CommandContext(ctx, s.argv[0], s.argv[1:]...)
		cmd.Env = append(os.Environ(), s.env...)
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", s.describe(), err)
		}
	}
	return nil
}

// ManagedInstalled reports whether Install has already set up l.
func ManagedInstalled(l *Language) bool {
	dir := paths.ServersDir()
	switch l {
	case Go:
		return firstExisting(filepath.Join(dir, "go", "bin", "gopls")) != ""
	case TypeScript:
		bin := filepath.Join(dir, "typescript", "node_modules", ".bin")
		return firstExisting(filepath.Join(bin, "tsc")) != "" && firstExisting(filepath.Join(bin, "typescript-language-server")) != ""
	case Python:
		return firstExisting(
			filepath.Join(dir, "pyright", "node_modules", ".bin", "pyright-langserver"),
			filepath.Join(dir, "basedpyright", "bin", "basedpyright-langserver"),
		) != ""
	}
	return false
}

type step struct {
	env  []string
	argv []string
}

func (s step) describe() string {
	out := ""
	for _, e := range s.env {
		out += e + " "
	}
	for i, a := range s.argv {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func npmStep(prefix string, pkgs ...string) step {
	argv := []string{"npm", "install", "--prefix", prefix, "--no-audit", "--no-fund", "--loglevel=error"}
	return step{argv: append(argv, pkgs...)}
}

func installSteps(l *Language) ([]step, error) {
	dir := paths.ServersDir()
	switch l {
	case Go:
		if lookPath("go") == "" {
			return nil, fmt.Errorf("installing gopls needs the go command on PATH")
		}
		return []step{{
			env:  []string{"GOBIN=" + filepath.Join(dir, "go", "bin")},
			argv: []string{"go", "install", "golang.org/x/tools/gopls@latest"},
		}}, nil
	case TypeScript:
		if lookPath("npm") == "" {
			return nil, fmt.Errorf("installing TypeScript tooling needs npm on PATH")
		}
		return []step{npmStep(filepath.Join(dir, "typescript"), "typescript@latest", "typescript-language-server@latest")}, nil
	case Python:
		if lookPath("npm") != "" {
			return []step{npmStep(filepath.Join(dir, "pyright"), "pyright@latest")}, nil
		}
		py := lookPath("python3")
		if py == "" {
			return nil, fmt.Errorf("installing a Python language server needs npm or python3 on PATH")
		}
		venv := filepath.Join(dir, "basedpyright")
		return []step{
			{argv: []string{py, "-m", "venv", venv}},
			{argv: []string{filepath.Join(venv, "bin", "pip"), "install", "--quiet", "basedpyright"}},
		}, nil
	}
	return nil, fmt.Errorf("unsupported language %q", l.ID)
}
