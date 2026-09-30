package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SettleStrategy decides how the client learns that a server has finished
// computing diagnostics after a change.
type SettleStrategy string

const (
	// SettlePush waits for publishDiagnostics notifications to arrive for
	// every target document and then for the stream to go quiet.
	SettlePush SettleStrategy = "push"
	// SettlePull requests diagnostics per document (textDocument/diagnostic).
	SettlePull SettleStrategy = "pull"
	// SettleGopls runs gopls' synchronous gopls.diagnose_files command.
	SettleGopls SettleStrategy = "gopls"
)

// Options configures a language server session.
type Options struct {
	Name                  string
	Command               []string
	Root                  string
	Env                   []string
	InitializationOptions any
	// Settings is returned for workspace/configuration requests, looked up by
	// dotted section name ("python.analysis" -> settings["python"]["analysis"]).
	Settings map[string]any
	Settle   SettleStrategy
	// Quiet is how long the publish stream must be idle before diagnostics
	// are considered settled (push strategy), or the grace period after a
	// synchronous diagnose call.
	Quiet time.Duration
	// FirstPublishWait bounds how long to wait for a publish for a target
	// document before assuming the server chose not to republish because
	// nothing changed.
	FirstPublishWait time.Duration
	Logf             func(format string, args ...any)
}

// Server is a running language server plus the client-side state that
// agent-squiggles tracks about it.
type Server struct {
	opts Options
	cmd  *exec.Cmd
	conn *Conn

	mu       sync.Mutex
	changed  chan struct{} // closed and replaced whenever state changes
	diags    map[string][]Diagnostic
	pubSeq   map[string]uint64
	seq      uint64
	lastPub  time.Time
	progress map[string]struct{}
	docs     map[string]*Doc
	pullOK   bool
	stderr   *tailBuffer
}

// Doc is an open text document as last sent to the server.
type Doc struct {
	Path       string
	LanguageID string
	Version    int
	Text       string
	LastUse    time.Time
}

// Start launches the server process and performs the initialize handshake.
func Start(ctx context.Context, opts Options) (*Server, error) {
	if len(opts.Command) == 0 {
		return nil, errors.New("lsp: empty server command")
	}
	if opts.Quiet == 0 {
		opts.Quiet = 300 * time.Millisecond
	}
	if opts.FirstPublishWait == 0 {
		opts.FirstPublishWait = 3 * time.Second
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	cmd.Dir = opts.Root
	cmd.Env = append(os.Environ(), opts.Env...)
	setProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	s := &Server{
		opts:     opts,
		cmd:      cmd,
		changed:  make(chan struct{}),
		diags:    map[string][]Diagnostic{},
		pubSeq:   map[string]uint64{},
		progress: map[string]struct{}{},
		docs:     map[string]*Doc{},
		stderr:   newTailBuffer(16 * 1024),
	}
	cmd.Stderr = s.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", opts.Command[0], err)
	}
	s.conn = NewConn(stdout, stdin, s)
	go func() {
		_ = s.conn.Run()
		s.broadcast()
	}()
	go func() { _ = cmd.Wait() }()

	if err := s.initialize(ctx); err != nil {
		s.Kill()
		if tail := strings.TrimSpace(s.stderr.String()); tail != "" {
			err = fmt.Errorf("%w (stderr: %s)", err, lastLines(tail, 5))
		}
		return nil, err
	}
	return s, nil
}

func (s *Server) initialize(ctx context.Context) error {
	root := s.opts.Root
	rootURI := PathToURI(root)
	params := map[string]any{
		"processId": os.Getpid(),
		"clientInfo": map[string]any{
			"name": "agent-squiggles",
		},
		"rootUri":  rootURI,
		"rootPath": root,
		"workspaceFolders": []map[string]any{
			{"uri": rootURI, "name": filepath.Base(root)},
		},
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"configuration":    true,
				"workspaceFolders": true,
				"didChangeWatchedFiles": map[string]any{
					"dynamicRegistration": true,
				},
			},
			"textDocument": map[string]any{
				"synchronization": map[string]any{
					"didSave": true,
				},
				"publishDiagnostics": map[string]any{
					"versionSupport": true,
				},
			},
			"window": map[string]any{
				"workDoneProgress": true,
			},
			"general": map[string]any{
				"positionEncodings": []string{"utf-16"},
			},
		},
	}
	if s.opts.Settle == SettlePull {
		// Advertising pull support makes some servers (pyright) stop pushing,
		// so only do it when we intend to pull.
		caps := params["capabilities"].(map[string]any)
		caps["textDocument"].(map[string]any)["diagnostic"] = map[string]any{"dynamicRegistration": false}
	}
	if s.opts.InitializationOptions != nil {
		params["initializationOptions"] = s.opts.InitializationOptions
	}
	var result struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := s.conn.Call(ctx, "initialize", params, &result); err != nil {
		return fmt.Errorf("initialize %s: %w", s.opts.Name, err)
	}
	if raw, ok := result.Capabilities["diagnosticProvider"]; ok && string(raw) != "null" && string(raw) != "false" {
		s.pullOK = true
	}
	if err := s.conn.Notify("initialized", map[string]any{}); err != nil {
		return err
	}
	if s.opts.Settings != nil {
		_ = s.conn.Notify("workspace/didChangeConfiguration", map[string]any{"settings": s.opts.Settings})
	}
	return nil
}

// Name returns the configured server name.
func (s *Server) Name() string { return s.opts.Name }

// Root returns the workspace root the server was started in.
func (s *Server) Root() string { return s.opts.Root }

// SupportsPull reports whether the server advertised pull diagnostics.
func (s *Server) SupportsPull() bool { return s.pullOK }

// Alive reports whether the connection to the server is still open.
func (s *Server) Alive() bool { return !isClosed(s.conn.Done()) }

// Stderr returns the tail of the server's stderr output, for debugging.
func (s *Server) Stderr() string { return s.stderr.String() }

// Notify implements Handler.
func (s *Server) Notify(method string, params json.RawMessage) {
	switch method {
	case "textDocument/publishDiagnostics":
		var p struct {
			URI         string       `json:"uri"`
			Diagnostics []Diagnostic `json:"diagnostics"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		path := URIToPath(p.URI)
		if path == "" {
			return
		}
		s.mu.Lock()
		s.seq++
		s.pubSeq[path] = s.seq
		s.lastPub = time.Now()
		if len(p.Diagnostics) == 0 {
			delete(s.diags, path)
		} else {
			s.diags[path] = p.Diagnostics
		}
		s.broadcastLocked()
		s.mu.Unlock()
	case "$/progress":
		var p struct {
			Token json.RawMessage `json:"token"`
			Value struct {
				Kind string `json:"kind"`
			} `json:"value"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		tok := string(p.Token)
		s.mu.Lock()
		switch p.Value.Kind {
		case "begin":
			s.progress[tok] = struct{}{}
		case "end":
			delete(s.progress, tok)
		}
		s.broadcastLocked()
		s.mu.Unlock()
	case "window/logMessage", "window/showMessage":
		var p struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &p) == nil && p.Type <= 2 {
			s.opts.Logf("%s: %s", s.opts.Name, p.Message)
		}
	}
}

// Request implements Handler.
func (s *Server) Request(method string, params json.RawMessage) (any, *ResponseError) {
	switch method {
	case "workspace/configuration":
		var p struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		_ = json.Unmarshal(params, &p)
		out := make([]any, len(p.Items))
		for i, item := range p.Items {
			out[i] = lookupSection(s.opts.Settings, item.Section)
		}
		return out, nil
	case "workspace/workspaceFolders":
		return []map[string]any{{"uri": PathToURI(s.opts.Root), "name": filepath.Base(s.opts.Root)}}, nil
	case "workspace/applyEdit":
		return map[string]any{"applied": false}, nil
	case "window/workDoneProgress/create":
		return nil, nil
	default:
		// registerCapability, the various */refresh requests, and
		// showMessageRequest are all fine to acknowledge with null.
		return nil, nil
	}
}

func lookupSection(settings map[string]any, section string) any {
	if settings == nil {
		return nil
	}
	if section == "" {
		return settings
	}
	var cur any = settings
	for _, part := range strings.Split(section, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[part]
		if !ok {
			return nil
		}
	}
	return cur
}

func (s *Server) broadcast() {
	s.mu.Lock()
	s.broadcastLocked()
	s.mu.Unlock()
}

func (s *Server) broadcastLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// Doc returns the open document for path, if any.
func (s *Server) Doc(path string) (Doc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.docs[path]
	if !ok {
		return Doc{}, false
	}
	return *d, true
}

// OpenDocs returns the paths of all open documents.
func (s *Server) OpenDocs() []Doc {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Doc, 0, len(s.docs))
	for _, d := range s.docs {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUse.Before(out[j].LastUse) })
	return out
}

// SetText makes the server's view of path equal to text, opening the
// document if needed. It returns true if anything was sent.
func (s *Server) SetText(path, languageID, text string) (bool, error) {
	s.mu.Lock()
	d, ok := s.docs[path]
	if ok {
		d.LastUse = time.Now()
		if d.Text == text {
			s.mu.Unlock()
			return false, nil
		}
		d.Version++
		d.Text = text
		version := d.Version
		s.mu.Unlock()
		return true, s.conn.Notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": PathToURI(path), "version": version},
			"contentChanges": []map[string]any{{"text": text}},
		})
	}
	s.docs[path] = &Doc{Path: path, LanguageID: languageID, Version: 1, Text: text, LastUse: time.Now()}
	s.mu.Unlock()
	return true, s.conn.Notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        PathToURI(path),
			"languageId": languageID,
			"version":    1,
			"text":       text,
		},
	})
}

// Close closes the document if it is open. It returns true if anything was sent.
func (s *Server) Close(path string) (bool, error) {
	s.mu.Lock()
	_, ok := s.docs[path]
	delete(s.docs, path)
	if ok && s.opts.Settle == SettlePull {
		// Pull servers never push a clearing update for closed documents.
		delete(s.diags, path)
	}
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, s.conn.Notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": PathToURI(path)},
	})
}

// Touch marks an open document as recently used.
func (s *Server) Touch(path string) {
	s.mu.Lock()
	if d, ok := s.docs[path]; ok {
		d.LastUse = time.Now()
	}
	s.mu.Unlock()
}

// FilesChanged tells the server that files changed on disk.
func (s *Server) FilesChanged(events []FileEvent) error {
	if len(events) == 0 {
		return nil
	}
	return s.conn.Notify("workspace/didChangeWatchedFiles", map[string]any{"changes": events})
}

// Mark returns a publish sequence number to pass to Settle.
func (s *Server) Mark() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// Diagnostics returns a copy of the latest known diagnostics, keyed by path.
func (s *Server) Diagnostics() map[string][]Diagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]Diagnostic, len(s.diags))
	for k, v := range s.diags {
		out[k] = append([]Diagnostic(nil), v...)
	}
	return out
}

// Busy reports whether the server has work-done progress in flight.
func (s *Server) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.progress) > 0
}

// WaitIdle waits until no progress is active and the publish stream has
// been quiet for the configured period. It returns false on timeout.
func (s *Server) WaitIdle(ctx context.Context, minWait time.Duration) bool {
	start := time.Now()
	for {
		s.mu.Lock()
		busy := len(s.progress) > 0
		last := s.lastPub
		ch := s.changed
		s.mu.Unlock()
		if !s.Alive() {
			return false
		}
		now := time.Now()
		if last.Before(start) {
			last = start
		}
		quietFor := now.Sub(last)
		if !busy && quietFor >= s.opts.Quiet && now.Sub(start) >= minWait {
			return true
		}
		wait := s.opts.Quiet - quietFor
		if wait < 20*time.Millisecond || busy {
			wait = 50 * time.Millisecond
		}
		if minLeft := minWait - now.Sub(start); minLeft > wait {
			wait = minLeft
		}
		select {
		case <-ctx.Done():
			return false
		case <-ch:
		case <-time.After(wait):
		}
	}
}

// Settle waits until diagnostics for targets reflect every change sent
// after mark. It returns false if it gave up because of the context.
func (s *Server) Settle(ctx context.Context, targets []string, mark uint64) bool {
	switch {
	case s.opts.Settle == SettleGopls:
		return s.settleGopls(ctx, targets)
	case s.opts.Settle == SettlePull && s.pullOK:
		return s.settlePull(ctx, targets)
	default:
		return s.settlePush(ctx, targets, mark)
	}
}

func (s *Server) settleGopls(ctx context.Context, targets []string) bool {
	// diagnose_files diagnoses the whole snapshot containing the files, so
	// any file gopls can read will do; missing files would make it fail.
	uris := make([]string, 0, len(targets))
	for _, t := range targets {
		if _, open := s.Doc(t); open {
			uris = append(uris, PathToURI(t))
		} else if _, err := os.Stat(t); err == nil {
			uris = append(uris, PathToURI(t))
		}
	}
	if len(uris) == 0 {
		// Every target is gone (say, a deleted file): anchor on another
		// Go file in the workspace instead.
		if anchor := s.anyGoFile(); anchor != "" {
			uris = append(uris, PathToURI(anchor))
		} else {
			return s.WaitIdle(ctx, 0)
		}
	}
	err := s.conn.Call(ctx, "workspace/executeCommand", map[string]any{
		"command":   "gopls.diagnose_files",
		"arguments": []any{map[string]any{"Files": uris}},
	}, nil)
	if err != nil {
		s.opts.Logf("%s: diagnose_files: %v", s.opts.Name, err)
		return s.settlePush(ctx, targets, 0)
	}
	return s.WaitIdle(ctx, 0)
}

// anyGoFile returns an open Go document, or the first Go file found under
// the root.
func (s *Server) anyGoFile() string {
	for _, d := range s.OpenDocs() {
		if strings.HasSuffix(d.Path, ".go") {
			if _, err := os.Stat(d.Path); err == nil {
				return d.Path
			}
		}
	}
	var found string
	_ = filepath.WalkDir(s.opts.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() && p != s.opts.Root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func (s *Server) settlePull(ctx context.Context, targets []string) bool {
	for _, path := range targets {
		if _, open := s.Doc(path); !open {
			continue
		}
		var report struct {
			Kind  string       `json:"kind"`
			Items []Diagnostic `json:"items"`
		}
		err := s.conn.Call(ctx, "textDocument/diagnostic", map[string]any{
			"textDocument": map[string]any{"uri": PathToURI(path)},
		}, &report)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			s.opts.Logf("%s: pull diagnostics for %s: %v", s.opts.Name, path, err)
			continue
		}
		if report.Kind == "unchanged" {
			continue
		}
		s.mu.Lock()
		s.seq++
		s.pubSeq[path] = s.seq
		if len(report.Items) == 0 {
			delete(s.diags, path)
		} else {
			s.diags[path] = report.Items
		}
		s.mu.Unlock()
	}
	return true
}

func (s *Server) settlePush(ctx context.Context, targets []string, mark uint64) bool {
	start := time.Now()
	for {
		s.mu.Lock()
		missing := 0
		for _, t := range targets {
			if _, open := s.docs[t]; !open {
				continue
			}
			if s.pubSeq[t] <= mark {
				missing++
			}
		}
		busy := len(s.progress) > 0
		last := s.lastPub
		ch := s.changed
		s.mu.Unlock()
		if !s.Alive() {
			return false
		}
		now := time.Now()
		if last.Before(start) {
			last = start
		}
		quietFor := now.Sub(last)
		waitedLongEnough := missing == 0 || now.Sub(start) >= s.opts.FirstPublishWait
		if !busy && waitedLongEnough && quietFor >= s.opts.Quiet {
			return true
		}
		wait := 50 * time.Millisecond
		if !busy && missing == 0 {
			wait = s.opts.Quiet - quietFor
			if wait < 10*time.Millisecond {
				wait = 10 * time.Millisecond
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-ch:
		case <-time.After(wait):
		}
	}
}

// Shutdown asks the server to exit and kills it if it does not.
func (s *Server) Shutdown(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if s.Alive() {
		_ = s.conn.Call(ctx, "shutdown", nil, nil)
		_ = s.conn.Notify("exit", nil)
	}
	select {
	case <-s.conn.Done():
	case <-ctx.Done():
	}
	s.Kill()
}

// Kill terminates the server process group.
func (s *Server) Kill() {
	if s.cmd != nil && s.cmd.Process != nil {
		killProcessGroup(s.cmd)
	}
}

type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append([]byte(nil), t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
