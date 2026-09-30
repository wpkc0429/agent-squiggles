// Package daemon keeps language servers warm between hook invocations.
//
// Each hook runs as a short-lived process. Starting gopls or tsserver for
// every edit would take seconds, so the first hook for a workspace spawns
// a background daemon that owns the language servers; later hooks talk to
// it over a Unix socket. The daemon exits after a period of inactivity.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wpkc0429/agent-squiggles/internal/config"
	"github.com/wpkc0429/agent-squiggles/internal/engine"
	"github.com/wpkc0429/agent-squiggles/internal/paths"
	"github.com/wpkc0429/agent-squiggles/internal/report"
	"github.com/wpkc0429/agent-squiggles/internal/trust"
)

// Ops understood by the daemon.
const (
	OpHook   = "hook"
	OpStatus = "status"
	OpStop   = "stop"
)

// Hook events forwarded to the daemon.
const (
	EventSessionStart = "SessionStart"
	EventPreToolUse   = "PreToolUse"
	EventPostToolUse  = "PostToolUse"
)

// Request is sent by a hook process to the daemon.
type Request struct {
	Op        string          `json:"op"`
	Event     string          `json:"event,omitempty"`
	ToolUseID string          `json:"toolUseId,omitempty"`
	ToolName  string          `json:"toolName,omitempty"`
	ToolInput json.RawMessage `json:"toolInput,omitempty"`
	Cwd       string          `json:"cwd,omitempty"`
}

// Response is the daemon's reply.
type Response struct {
	Error string `json:"error,omitempty"`
	// Context is the model-facing report of new diagnostics.
	Context string `json:"context,omitempty"`
	// Summary is a one-line user-facing summary of Context.
	Summary string `json:"summary,omitempty"`
	// Notices are user-facing status messages.
	Notices []string `json:"notices,omitempty"`
	// Mode is the configured output mode ("context" or "block").
	Mode   string         `json:"mode,omitempty"`
	Count  int            `json:"count,omitempty"`
	Status *engine.Status `json:"status,omitempty"`
}

// DefaultIdle is how long a daemon lives without requests.
const DefaultIdle = 30 * time.Minute

// Serve runs the daemon for root until it is idle for idle, stopped, or
// signaled. It returns nil immediately if another daemon owns root.
func Serve(root string, idle time.Duration) error {
	if _, err := paths.EnsureRuntimeDir(); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.LogDir(), 0o755); err != nil {
		return err
	}
	lock, err := acquire(root)
	if lock == nil || err != nil {
		return err
	}
	defer lock.Unlock()

	logFile, err := os.OpenFile(paths.LogPath(root), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	logger := log.New(logFile, "", log.LstdFlags|log.Lmicroseconds)
	logger.Printf("daemon starting for %s (pid %d)", root, os.Getpid())

	sock := paths.SocketPath(root)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer os.Remove(sock)

	trusted := trust.Trusted(root)
	logger.Printf("codex trusts this project: %v", trusted)
	cfg, err := config.Load(root, trusted)
	if err != nil {
		logger.Printf("config: %v (using defaults)", err)
		cfg = config.Defaults()
	}
	d := &daemon{
		root:    root,
		logf:    logger.Printf,
		engine:  engine.New(root, cfg, logger.Printf),
		idle:    idle,
		touched: make(chan struct{}, 1),
		stop:    make(chan struct{}),
		cfgMod:  configStamp(root),
	}
	d.engine.SetTrusted(trusted)
	defer d.engine.Close()

	go d.watch(ln)
	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.handle(conn)
		}()
	}
	wg.Wait()
	logger.Printf("daemon exiting")
	return nil
}

// acquire takes the single-instance lock for root. It returns a nil lock
// (and nil error) when a healthy daemon already serves root. A daemon that
// is shutting down still holds the lock briefly, so keep trying for a
// few seconds before giving up.
func acquire(root string) (*paths.Lock, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		lock, err := paths.TryLock(paths.LockPath(root))
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, paths.ErrLocked) {
			return nil, err
		}
		if conn, derr := dial(root); derr == nil {
			conn.Close()
			return nil, nil
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type daemon struct {
	root    string
	logf    func(string, ...any)
	engine  *engine.Engine
	idle    time.Duration
	touched chan struct{}
	stop    chan struct{}
	once    sync.Once

	cfgMu  sync.Mutex
	cfgMod string
}

// watch closes the listener after inactivity, on stop, or on a signal.
func (d *daemon) watch(ln net.Listener) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	timer := time.NewTimer(d.idle)
	for {
		select {
		case <-d.touched:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(d.idle)
		case <-timer.C:
			d.logf("idle for %v, shutting down", d.idle)
			ln.Close()
			return
		case <-d.stop:
			ln.Close()
			return
		case s := <-sigs:
			d.logf("received %v, shutting down", s)
			ln.Close()
			return
		}
	}
}

func (d *daemon) touch() {
	select {
	case d.touched <- struct{}{}:
	default:
	}
}

func (d *daemon) handle(conn net.Conn) {
	defer conn.Close()
	d.touch()
	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: err.Error()})
		return
	}
	resp := d.dispatch(req)
	_ = json.NewEncoder(conn).Encode(resp)
	d.touch()
}

func (d *daemon) dispatch(req Request) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			d.logf("panic handling %s/%s: %v", req.Op, req.Event, r)
			resp = Response{Error: fmt.Sprint(r)}
		}
	}()
	d.reloadConfig()
	cfg := d.engine.Config()
	resp.Mode = cfg.Mode
	switch req.Op {
	case OpStatus:
		st := d.engine.Status()
		resp.Status = &st
	case OpStop:
		d.once.Do(func() { close(d.stop) })
	case OpHook:
		if cfg.Disabled {
			return resp
		}
		switch req.Event {
		case EventSessionStart:
			// Warm up in the background so the hook returns immediately.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				d.engine.SessionStart(ctx)
			}()
		case EventPreToolUse:
			d.engine.PreTool(req.ToolUseID, req.ToolName, req.ToolInput, req.Cwd)
		case EventPostToolUse:
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds)*time.Second)
			defer cancel()
			start := time.Now()
			res, err := d.engine.PostTool(ctx, req.ToolUseID, req.ToolName, req.ToolInput, req.Cwd)
			if err != nil {
				d.logf("post %s %s: %v", req.ToolName, req.ToolUseID, err)
				resp.Error = err.Error()
				return resp
			}
			d.logf("post %s %s: %d new, %d checked in %v", req.ToolName, req.ToolUseID, len(res.New), res.Checked, time.Since(start).Round(time.Millisecond))
			resp.Notices = res.Notices
			resp.Count = len(res.New)
			if len(res.New) > 0 {
				resp.Context = report.Format(res.New, report.Options{
					Root: d.engine.Root, Edited: res.Edited, Max: cfg.MaxDiagnostics, Texts: res.Texts,
				})
				resp.Summary = report.Summary(res.New)
			}
		default:
			resp.Error = "unknown event " + req.Event
		}
	default:
		resp.Error = "unknown op " + req.Op
	}
	return resp
}

// reloadConfig picks up edits to the config files.
func (d *daemon) reloadConfig() {
	stamp := configStamp(d.root)
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	if stamp == d.cfgMod {
		return
	}
	d.cfgMod = stamp
	trusted := trust.Trusted(d.root)
	cfg, err := config.Load(d.root, trusted)
	if err != nil {
		d.logf("config: %v (keeping previous)", err)
		return
	}
	d.logf("config reloaded (trusted: %v)", trusted)
	d.engine.SetConfig(cfg)
	d.engine.SetTrusted(trusted)
}

func configStamp(root string) string {
	var parts []string
	for _, p := range []string{config.UserFile(), filepath.Join(root, config.ProjectFile), trust.ConfigPath()} {
		if st, err := os.Stat(p); err == nil {
			parts = append(parts, fmt.Sprintf("%s:%d:%d", p, st.Size(), st.ModTime().UnixNano()))
		}
	}
	return strings.Join(parts, "|")
}

// Call sends a request to the daemon for root, starting the daemon if it
// is not running (unless spawn is false).
func Call(root string, req Request, timeout time.Duration, spawn bool) (*Response, error) {
	conn, err := dial(root)
	if err != nil {
		if !spawn {
			return nil, err
		}
		if err := start(root); err != nil {
			return nil, fmt.Errorf("start daemon: %w", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			conn, err = dial(root)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("daemon did not come up (see %s): %w", paths.LogPath(root), err)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func dial(root string) (net.Conn, error) {
	if _, err := paths.EnsureRuntimeDir(); err != nil {
		return nil, err
	}
	return net.DialTimeout("unix", paths.SocketPath(root), time.Second)
}

// start launches a detached daemon process for root.
func start(root string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.LogDir(), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(paths.LogPath(root), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.Command(exe, "daemon", "--root", root)
	cmd.Dir = root
	cmd.Stdout = out
	cmd.Stderr = out
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
