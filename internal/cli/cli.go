// Package cli implements the agent-squiggles command line.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/wpkc0429/agent-squiggles/internal/bashcmd"
	"github.com/wpkc0429/agent-squiggles/internal/codex"
	"github.com/wpkc0429/agent-squiggles/internal/config"
	"github.com/wpkc0429/agent-squiggles/internal/daemon"
	"github.com/wpkc0429/agent-squiggles/internal/engine"
	"github.com/wpkc0429/agent-squiggles/internal/langs"
	"github.com/wpkc0429/agent-squiggles/internal/paths"
	"github.com/wpkc0429/agent-squiggles/internal/report"
	"github.com/wpkc0429/agent-squiggles/internal/snapshot"
	"github.com/wpkc0429/agent-squiggles/internal/trust"
)

// Version is set at build time with -ldflags "-X ...cli.Version=v1.2.3".
var Version = ""

const usage = `agent-squiggles: red squiggles for coding agents.

After every edit your coding agent makes, agent-squiggles asks the
language server what broke and tells the agent about the errors the edit
introduced, including ones in files it did not touch.

Usage:
  agent-squiggles <command> [flags]

Commands:
  install     add the hooks to Codex and set up language servers
  uninstall   remove the hooks from Codex
  check       report errors introduced since a git revision (default HEAD)
  doctor      show languages, language servers, and hook status
  servers     install language servers: servers install [go|typescript|python]
  stop        stop the background daemon for this workspace
  version     print the version

Run "agent-squiggles <command> -h" for command flags.
`

// Main runs the CLI and returns the process exit code.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	code := 0
	switch cmd {
	case "hook":
		return runHook(rest, stdin, stdout, stderr)
	case "daemon":
		err = runDaemon(rest)
	case "install":
		err = runInstall(rest, stdin, stdout)
	case "uninstall":
		err = runUninstall(rest, stdout)
	case "check":
		code, err = runCheck(rest, stdout, stderr)
	case "doctor":
		err = runDoctor(rest, stdout)
	case "servers":
		err = runServers(rest, stdout)
	case "stop":
		err = runStop(stdout)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "agent-squiggles", version())
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 2
		}
		fmt.Fprintln(stderr, "agent-squiggles:", err)
		return 1
	}
	return code
}

func version() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// workspaceRoot returns the git top-level directory containing dir, or dir.
func workspaceRoot(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err == nil {
		if root := strings.TrimSpace(string(out)); root != "" {
			return filepath.Clean(root)
		}
	}
	return dir
}

// runHook is the Codex hook entry point. It must never make Codex fail:
// every problem is logged and the hook exits 0.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "codex" {
		fmt.Fprintln(stderr, "usage: agent-squiggles hook codex")
		return 0
	}
	if v := os.Getenv("AGENT_SQUIGGLES_DISABLE"); v != "" && v != "0" {
		return 0
	}
	in, err := codex.ReadInput(stdin)
	if err != nil {
		fmt.Fprintln(stderr, "agent-squiggles:", err)
		return 0
	}
	req := daemon.Request{
		Op:        daemon.OpHook,
		Event:     in.HookEventName,
		ToolUseID: in.ToolUseID,
		ToolName:  in.ToolName,
		ToolInput: in.ToolInput,
		Cwd:       in.Cwd,
	}
	timeout := 110 * time.Second
	switch in.HookEventName {
	case daemon.EventSessionStart:
	case daemon.EventPreToolUse, daemon.EventPostToolUse:
		switch in.ToolName {
		case engine.ToolApplyPatch, "Edit", "Write":
		case engine.ToolBash:
			if bashcmd.IsReadOnly(bashcmd.CommandText(in.ToolInput)) {
				return 0
			}
		default:
			return 0
		}
		if in.HookEventName == daemon.EventPreToolUse {
			timeout = 25 * time.Second
		}
	default:
		return 0
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	root := workspaceRoot(cwd)
	resp, err := daemon.Call(root, req, timeout, true)
	if err != nil {
		fmt.Fprintln(stderr, "agent-squiggles:", err)
		return 0
	}
	if resp.Error != "" {
		fmt.Fprintln(stderr, "agent-squiggles:", resp.Error)
	}
	if in.HookEventName == daemon.EventPostToolUse {
		if out := codex.PostToolUseOutput(resp.Mode, resp.Context, resp.Summary, resp.Notices); out != nil {
			stdout.Write(out)
			fmt.Fprintln(stdout)
		}
	}
	return 0
}

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	root := fs.String("root", "", "workspace root")
	idle := fs.Duration("idle", daemon.DefaultIdle, "exit after this much inactivity")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *root == "" {
		return errors.New("daemon: --root is required")
	}
	return daemon.Serve(*root, *idle)
}

// hookCommand is the command line Codex runs for each hook.
func hookCommand() string {
	exe, err := os.Executable()
	if err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
	}
	// Prefer the bare name when PATH resolves to this binary: the hook
	// definition then survives upgrades that move the binary.
	if onPath, err := exec.LookPath("agent-squiggles"); err == nil && exe != "" {
		if real, err := filepath.EvalSymlinks(onPath); err == nil && real == exe {
			return "agent-squiggles " + codex.Marker
		}
	}
	if exe == "" {
		return "agent-squiggles " + codex.Marker
	}
	return codex.ShellQuote(exe) + " " + codex.Marker
}

func runInstall(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	project := fs.Bool("project", false, "write .codex/hooks.json in this repository instead of ~/.codex/hooks.json")
	hooksFile := fs.String("hooks-file", "", "write to this hooks.json file")
	noServers := fs.Bool("no-servers", false, "do not install missing language servers")
	yes := fs.Bool("yes", false, "install missing language servers without asking")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	root := workspaceRoot(cwd)
	path := codex.DefaultHooksPath()
	if *project {
		path = codex.ProjectHooksPath(root)
	}
	if *hooksFile != "" {
		path = *hooksFile
	}
	command := hookCommand()
	if err := codex.Install(path, command); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "✓ Added agent-squiggles hooks to %s\n  command: %s\n", path, command)

	if !*noServers {
		in := bufio.NewReader(stdin)
		trusted := trust.Trusted(root)
		for _, l := range detectLanguages(root) {
			s, err := langs.Resolve(l, root, config.Language{}, trusted)
			if err == nil {
				fmt.Fprintf(stdout, "✓ %s: using %s\n", l.Name, s.Name)
				continue
			}
			nie, ok := langs.IsNotInstalled(err)
			if !ok || !nie.Installable {
				fmt.Fprintf(stdout, "! %s: %v\n", l.Name, err)
				continue
			}
			plan, _ := langs.InstallPlan(l)
			if !*yes {
				fmt.Fprintf(stdout, "? %s: no language server found. Install one now?\n    %s\n  [Y/n] ", l.Name, plan)
				answer, _ := in.ReadString('\n')
				answer = strings.ToLower(strings.TrimSpace(answer))
				if answer != "" && answer != "y" && answer != "yes" {
					fmt.Fprintf(stdout, "  skipped (it will be installed automatically on first use)\n")
					continue
				}
			}
			fmt.Fprintf(stdout, "… installing a %s language server\n", l.Name)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			err = langs.Install(ctx, l, stdout)
			cancel()
			if err != nil {
				fmt.Fprintf(stdout, "! %s: install failed: %v\n", l.Name, err)
			} else {
				fmt.Fprintf(stdout, "✓ %s: installed\n", l.Name)
			}
		}
	}
	fmt.Fprintf(stdout, `
Next: Codex asks you to review new hooks before running them.
Start codex and run /hooks to trust the three agent-squiggles hooks.
`)
	return nil
}

func runUninstall(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	project := fs.Bool("project", false, "edit .codex/hooks.json in this repository")
	hooksFile := fs.String("hooks-file", "", "edit this hooks.json file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	path := codex.DefaultHooksPath()
	if *project {
		path = codex.ProjectHooksPath(workspaceRoot(cwd))
	}
	if *hooksFile != "" {
		path = *hooksFile
	}
	n, err := codex.Uninstall(path)
	if err != nil {
		return err
	}
	if n == 0 {
		fmt.Fprintf(stdout, "No agent-squiggles hooks in %s\n", path)
		return nil
	}
	fmt.Fprintf(stdout, "✓ Removed %d agent-squiggles hooks from %s\n", n, path)
	_ = runStop(io.Discard)
	return nil
}

func runCheck(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	base := fs.String("base", "HEAD", "git revision to compare against")
	all := fs.Bool("all", false, "report every diagnostic in changed files and their dependents, not just new ones")
	asJSON := fs.Bool("json", false, "print diagnostics as JSON")
	verbose := fs.Bool("verbose", false, "log language server activity to stderr")
	timeout := fs.Duration("timeout", 2*time.Minute, "give up after this long")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: agent-squiggles check [flags]\n\nReports errors introduced in the working tree since --base. Exits 1 if there are any.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	cwd, _ := os.Getwd()
	root := workspaceRoot(cwd)
	// Running check is an explicit request in this repository, like
	// running its tests, so the project is treated as trusted.
	cfg, err := config.Load(root, true)
	if err != nil {
		return 2, err
	}
	off := false
	cfg.AutoInstall = &off
	logf := func(string, ...any) {}
	if *verbose {
		logf = func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }
	}
	e := engine.New(root, cfg, logf)
	e.SetTrusted(true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	res, err := e.CheckSince(ctx, *base, *all)
	if err != nil {
		return 2, err
	}
	for _, n := range res.Notices {
		fmt.Fprintln(stderr, n)
	}
	if *asJSON {
		type item struct {
			File     string `json:"file"`
			Line     int    `json:"line"`
			Column   int    `json:"column"`
			Severity string `json:"severity"`
			Source   string `json:"source,omitempty"`
			Code     string `json:"code,omitempty"`
			Message  string `json:"message"`
		}
		items := []item{}
		for _, d := range res.New {
			rel, _ := filepath.Rel(root, d.Path)
			sev := "error"
			if d.Severity > 1 {
				sev = "warning"
			}
			items = append(items, item{filepath.ToSlash(rel), d.Line + 1, d.Col + 1, sev, d.Source, d.Code, d.Message})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(items)
	} else if len(res.New) > 0 {
		fmt.Fprintln(stdout, report.Format(res.New, report.Options{Root: root, Edited: res.Edited, Texts: res.Texts}))
	} else {
		fmt.Fprintf(stdout, "✓ No new problems since %s (%d files checked)\n", *base, res.Checked)
	}
	if len(res.New) > 0 {
		return 1, nil
	}
	return 0, nil
}

// detectLanguages lists the supported languages used in the workspace.
func detectLanguages(root string) []*langs.Language {
	var out []*langs.Language
	repo, _ := snapshot.Open(root, paths.StateDir(), langs.Patterns(langs.All()))
	for _, l := range langs.All() {
		found := false
		for _, m := range l.Markers {
			if _, err := os.Stat(filepath.Join(root, m)); err == nil {
				found = true
				break
			}
		}
		if !found && repo != nil {
			if files, err := repo.ListFiles(langs.Patterns([]*langs.Language{l})); err == nil && len(files) > 0 {
				found = true
			}
		}
		if found {
			out = append(out, l)
		}
	}
	return out
}

func runServers(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "install" {
		return errors.New("usage: agent-squiggles servers install [go|typescript|python ...]")
	}
	var targets []*langs.Language
	for _, id := range args[1:] {
		l := langs.ByID(id)
		if l == nil {
			return fmt.Errorf("unknown language %q (want go, typescript, or python)", id)
		}
		targets = append(targets, l)
	}
	if len(targets) == 0 {
		cwd, _ := os.Getwd()
		targets = detectLanguages(workspaceRoot(cwd))
		if len(targets) == 0 {
			return errors.New("no supported languages detected here; name them explicitly")
		}
	}
	for _, l := range targets {
		fmt.Fprintf(stdout, "… installing a %s language server into %s\n", l.Name, paths.ServersDir())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		err := langs.Install(ctx, l, stdout)
		cancel()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "✓ %s ready\n", l.Name)
	}
	return nil
}

func runStop(stdout io.Writer) error {
	cwd, _ := os.Getwd()
	root := workspaceRoot(cwd)
	if _, err := daemon.Call(root, daemon.Request{Op: daemon.OpStop}, 5*time.Second, false); err != nil {
		fmt.Fprintln(stdout, "No daemon running for", root)
		return nil
	}
	fmt.Fprintln(stdout, "✓ Stopped the daemon for", root)
	return nil
}

func runDoctor(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	root := workspaceRoot(cwd)
	ok := func(good bool) string {
		if good {
			return "✓"
		}
		return "✗"
	}
	fmt.Fprintf(stdout, "agent-squiggles %s (%s/%s)\n\n", version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(stdout, "Workspace  %s\n", root)
	_, gitErr := snapshot.Open(root, paths.StateDir(), nil)
	fmt.Fprintf(stdout, "  %s git repository (needed to check edits made by shell commands)\n", ok(gitErr == nil))
	trusted := trust.Trusted(root)
	fmt.Fprintf(stdout, "  %s trusted in Codex", ok(trusted))
	if !trusted {
		fmt.Fprint(stdout, " (project-local servers and .agent-squiggles.json are ignored until you trust it in Codex)")
	}
	fmt.Fprintln(stdout)
	if _, err := config.Load(root, trusted); err != nil {
		fmt.Fprintf(stdout, "  ✗ config: %v\n", err)
	}

	fmt.Fprintln(stdout, "\nCodex")
	if out, err := exec.Command("codex", "--version").Output(); err == nil {
		fmt.Fprintf(stdout, "  ✓ %s\n", strings.TrimSpace(string(out)))
	} else {
		fmt.Fprintln(stdout, "  ✗ codex not found on PATH")
	}
	user, proj := codex.DefaultHooksPath(), codex.ProjectHooksPath(root)
	fmt.Fprintf(stdout, "  %s hooks in %s\n", ok(codex.Installed(user)), user)
	if codex.Installed(proj) {
		fmt.Fprintf(stdout, "  ✓ hooks in %s\n", proj)
	}
	if !codex.Installed(user) && !codex.Installed(proj) {
		fmt.Fprintln(stdout, "    run: agent-squiggles install")
	}

	fmt.Fprintln(stdout, "\nLanguages")
	detected := map[*langs.Language]bool{}
	for _, l := range detectLanguages(root) {
		detected[l] = true
	}
	for _, l := range langs.All() {
		label := "not used here"
		if detected[l] {
			label = "used here"
		}
		s, err := langs.Resolve(l, root, config.Language{}, trusted)
		switch {
		case err == nil:
			fmt.Fprintf(stdout, "  ✓ %-22s %s: %s\n", l.Name, label, strings.Join(s.Command, " "))
		default:
			fmt.Fprintf(stdout, "  ✗ %-22s %s: %v\n", l.Name, label, err)
			if nie, isNI := langs.IsNotInstalled(err); isNI && nie.Installable {
				fmt.Fprintf(stdout, "    run: agent-squiggles servers install %s\n", l.ID)
			}
		}
	}

	fmt.Fprintln(stdout, "\nDaemon")
	resp, err := daemon.Call(root, daemon.Request{Op: daemon.OpStatus}, 5*time.Second, false)
	if err != nil || resp.Status == nil {
		fmt.Fprintln(stdout, "  not running (starts on the first hook)")
	} else {
		fmt.Fprintf(stdout, "  ✓ running, log: %s\n", paths.LogPath(root))
		for _, s := range resp.Status.Servers {
			fmt.Fprintf(stdout, "    %s\n", s)
		}
	}
	return nil
}
