// Package engine is the heart of agent-squiggles: given a tool call that
// may have edited files, it works out which source files changed, asks
// the language servers for diagnostics before and after the change, and
// returns only the diagnostics the change introduced.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wpkc0429/agent-squiggles/internal/bashcmd"
	"github.com/wpkc0429/agent-squiggles/internal/config"
	"github.com/wpkc0429/agent-squiggles/internal/delta"
	"github.com/wpkc0429/agent-squiggles/internal/deps"
	"github.com/wpkc0429/agent-squiggles/internal/langs"
	"github.com/wpkc0429/agent-squiggles/internal/lsp"
	"github.com/wpkc0429/agent-squiggles/internal/patch"
	"github.com/wpkc0429/agent-squiggles/internal/paths"
	"github.com/wpkc0429/agent-squiggles/internal/snapshot"
)

const (
	// maxChangedFiles skips checks for sweeping changes (codegen,
	// formatters over the whole tree) that would take too long.
	maxChangedFiles = 200
	// maxOpenDocs bounds how many documents stay open per server.
	maxOpenDocs = 80
	// preTTL bounds how long an unmatched pre-tool snapshot is kept.
	preTTL = 15 * time.Minute
)

// Tool names as they appear in Codex hook payloads.
const (
	ToolApplyPatch = "apply_patch"
	ToolBash       = "Bash"
)

// Result is the outcome of a check.
type Result struct {
	// New lists diagnostics introduced by the change.
	New []delta.Diag
	// Edited is the set of files the change touched (absolute paths).
	Edited map[string]bool
	// Texts holds the post-change contents of edited files.
	Texts map[string]string
	// Notices are user-facing status messages (installs, timeouts).
	Notices []string
	// Checked counts the files whose diagnostics were compared.
	Checked int
}

// Engine tracks one workspace.
type Engine struct {
	Root string

	cfg     config.Config
	trusted bool
	repo    *snapshot.Repo
	finder  *deps.Finder
	logf    func(string, ...any)

	mu       sync.Mutex // guards lastTree, pre, notices
	lastTree string
	pre      map[string]preState
	notices  []string

	checkMu    sync.Mutex // serializes language-server work
	servers    map[string]*serverEntry
	installing map[string]bool
	warned     map[string]bool
}

type preState struct {
	tree  string
	files map[string]*string // non-git fallback: path -> content (nil = absent)
	at    time.Time
}

type serverEntry struct {
	lang     *langs.Language
	root     string
	srv      *lsp.Server
	err      error
	failedAt time.Time
	// primed is set once the server has settled at least once, so its
	// diagnostics can serve as a baseline without another round trip.
	primed bool
}

type fileChange struct {
	path string // absolute
	lang *langs.Language
	old  *string
	new  *string
}

// New returns an engine for a workspace directory. The root is the git
// top-level directory when dir is inside a repository, otherwise dir.
func New(dir string, cfg config.Config, logf func(string, ...any)) *Engine {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	e := &Engine{
		Root:       dir,
		cfg:        cfg,
		logf:       logf,
		pre:        map[string]preState{},
		servers:    map[string]*serverEntry{},
		installing: map[string]bool{},
		warned:     map[string]bool{},
	}
	repo, err := snapshot.Open(dir, paths.StateDir(), langs.Patterns(langs.All()))
	if err == nil {
		e.repo = repo
		e.Root = repo.Root
	} else if !errors.Is(err, snapshot.ErrNotGit) {
		logf("snapshot: %v", err)
	}
	e.finder = deps.NewFinder(e.Root)
	return e
}

// IsGit reports whether the workspace is a git repository.
func (e *Engine) IsGit() bool { return e.repo != nil }

// Config returns the engine's configuration.
func (e *Engine) Config() config.Config { return e.cfg }

// SetConfig replaces the configuration (after the config file changed).
func (e *Engine) SetConfig(cfg config.Config) {
	e.checkMu.Lock()
	e.cfg = cfg
	e.checkMu.Unlock()
}

// SetTrusted records whether Codex trusts the project, which decides
// whether project-local language server binaries may be used. Servers are
// restarted when it changes.
func (e *Engine) SetTrusted(trusted bool) {
	e.checkMu.Lock()
	changed := e.trusted != trusted
	e.trusted = trusted
	e.checkMu.Unlock()
	if changed {
		e.Close()
	}
}

// SessionStart records a baseline snapshot and starts language servers
// for the languages the workspace root declares, so the first check is
// fast.
func (e *Engine) SessionStart(ctx context.Context) {
	if e.repo != nil {
		if tree, err := e.repo.Snapshot(); err == nil {
			e.mu.Lock()
			e.lastTree = tree
			e.mu.Unlock()
		}
	}
	for _, l := range langs.All() {
		if e.cfg.Languages[l.ID].Disabled {
			continue
		}
		for _, m := range l.Markers {
			if _, err := os.Stat(filepath.Join(e.Root, m)); err == nil {
				e.warm(ctx, l)
				break
			}
		}
	}
}

func (e *Engine) warm(ctx context.Context, l *langs.Language) {
	e.checkMu.Lock()
	defer e.checkMu.Unlock()
	entry, err := e.server(ctx, l, e.Root)
	if err != nil {
		return
	}
	if l == langs.Go {
		// Trigger gopls' initial workspace load and diagnosis now rather
		// than during the first edit.
		if files := e.listFiles(l); len(files) > 0 && entry.srv.Settle(ctx, files[:1], 0) {
			entry.primed = true
		}
	}
}

// PreTool records the state of the workspace before a tool runs.
func (e *Engine) PreTool(id, tool string, input json.RawMessage, cwd string) {
	if !e.relevant(tool, input) {
		return
	}
	st := preState{at: time.Now()}
	if e.repo != nil {
		tree, err := e.repo.Snapshot()
		if err != nil {
			e.logf("pre snapshot: %v", err)
			return
		}
		st.tree = tree
	} else if tool == ToolApplyPatch {
		st.files = map[string]*string{}
		for _, p := range patch.Paths(patch.Parse(patch.CommandText(input))) {
			abs := resolve(cwd, p)
			st.files[abs] = readFile(abs)
		}
	} else {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, v := range e.pre {
		if time.Since(v.at) > preTTL {
			delete(e.pre, k)
		}
	}
	e.pre[id] = st
}

// relevant reports whether a tool call can change source files.
func (e *Engine) relevant(tool string, input json.RawMessage) bool {
	switch tool {
	case ToolApplyPatch, "Edit", "Write":
		return true
	case ToolBash:
		return e.cfg.BashEnabled() && !bashcmd.IsReadOnly(bashcmd.CommandText(input))
	}
	return false
}

// PostTool checks the changes made by a tool call.
func (e *Engine) PostTool(ctx context.Context, id, tool string, input json.RawMessage, cwd string) (*Result, error) {
	if !e.relevant(tool, input) {
		return &Result{}, nil
	}
	changes, err := e.collectChanges(id, tool, input, cwd)
	if err != nil {
		return nil, err
	}
	return e.check(ctx, changes)
}

// CheckSince reports diagnostics introduced since a git revision (the
// "check" command). With all set, every diagnostic in the changed files
// and their dependents is reported.
func (e *Engine) CheckSince(ctx context.Context, rev string, all bool) (*Result, error) {
	if e.repo == nil {
		return nil, errors.New("check needs a git repository")
	}
	base := snapshot.EmptyTree
	if rev != "" {
		t, err := e.repo.ResolveTree(rev)
		if err != nil {
			return nil, err
		}
		base = t
	}
	cur, err := e.repo.Snapshot()
	if err != nil {
		return nil, err
	}
	changes, err := e.treeChanges(base, cur)
	if err != nil {
		return nil, err
	}
	if all {
		for i := range changes {
			changes[i].old = nil
		}
	}
	return e.check(ctx, changes)
}

func (e *Engine) collectChanges(id, tool string, input json.RawMessage, cwd string) ([]fileChange, error) {
	e.mu.Lock()
	st, hasPre := e.pre[id]
	delete(e.pre, id)
	last := e.lastTree
	e.mu.Unlock()

	if e.repo == nil {
		if !hasPre {
			return nil, nil
		}
		var out []fileChange
		for p, old := range st.files {
			cur := readFile(p)
			if equalPtr(old, cur) {
				continue
			}
			if l := langs.ForPath(p); l != nil {
				out = append(out, fileChange{path: p, lang: l, old: old, new: cur})
			}
		}
		return out, nil
	}

	cur, err := e.repo.Snapshot()
	if err != nil {
		return nil, err
	}
	base := st.tree
	if !hasPre || base == "" {
		base = last
	}
	if base == "" {
		// No snapshot before this tool (the daemon just started): fall
		// back to the index, which is usually close to the pre-edit state.
		base, _ = e.repo.IndexTree()
	}
	e.mu.Lock()
	e.lastTree = cur
	e.mu.Unlock()
	return e.treeChanges(base, cur)
}

func (e *Engine) treeChanges(base, cur string) ([]fileChange, error) {
	diff, err := e.repo.Diff(base, cur)
	if err != nil {
		return nil, err
	}
	var out []fileChange
	for _, c := range diff {
		l := langs.ForPath(c.Path)
		if l == nil {
			continue
		}
		fc := fileChange{path: filepath.Join(e.Root, filepath.FromSlash(c.Path)), lang: l}
		if c.Status != "A" {
			data, ok, err := e.repo.ReadFile(base, c.Path)
			if err != nil {
				return nil, err
			}
			if ok {
				s := string(data)
				fc.old = &s
			}
		}
		if c.Status != "D" {
			data, ok, err := e.repo.ReadFile(cur, c.Path)
			if err != nil {
				return nil, err
			}
			if ok {
				s := string(data)
				fc.new = &s
			}
		}
		out = append(out, fc)
	}
	return out, nil
}

func (e *Engine) check(ctx context.Context, changes []fileChange) (*Result, error) {
	res := &Result{Edited: map[string]bool{}, Texts: map[string]string{}}
	var filtered []fileChange
	for _, c := range changes {
		if e.cfg.Languages[c.lang.ID].Disabled || strings.Contains(c.path, string(filepath.Separator)+"node_modules"+string(filepath.Separator)) {
			continue
		}
		filtered = append(filtered, c)
	}
	res.Notices = e.takeNotices()
	if len(filtered) == 0 {
		return res, nil
	}
	if len(filtered) > maxChangedFiles {
		res.Notices = append(res.Notices, fmt.Sprintf("agent-squiggles: skipped checking %d changed files (limit %d)", len(filtered), maxChangedFiles))
		return res, nil
	}
	for _, c := range filtered {
		res.Edited[c.path] = true
		if c.new != nil {
			res.Texts[c.path] = *c.new
		}
	}

	groups := map[string][]fileChange{}
	var keys []string
	for _, c := range filtered {
		root := c.lang.ProjectRoot(c.path, e.Root)
		k := c.lang.ID + "\x00" + root
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], c)
	}
	sort.Strings(keys)

	e.checkMu.Lock()
	defer e.checkMu.Unlock()
	for _, k := range keys {
		group := groups[k]
		_, root, _ := strings.Cut(k, "\x00")
		newDiags, checked, notice, err := e.checkGroup(ctx, group[0].lang, root, group)
		if notice != "" {
			res.Notices = append(res.Notices, notice)
		}
		if err != nil {
			e.logf("check %s in %s: %v", group[0].lang.ID, root, err)
			continue
		}
		res.Checked += checked
		res.New = append(res.New, newDiags...)
	}
	delta.Sort(res.New)
	return res, nil
}

// checkGroup runs the before/after comparison for changes handled by one
// language server.
func (e *Engine) checkGroup(ctx context.Context, l *langs.Language, root string, changes []fileChange) ([]delta.Diag, int, string, error) {
	entry, err := e.server(ctx, l, root)
	if err != nil {
		return nil, 0, e.serverNotice(l, err), err
	}
	srv := entry.srv
	changed := map[string]bool{}
	var changedPaths []string
	for _, c := range changes {
		changed[c.path] = true
		changedPaths = append(changedPaths, c.path)
	}

	var dependents []string
	if l.Deps != "" {
		dependents = e.finder.Dependents(l.Deps, e.listFiles(l), changedPaths, e.cfg.MaxDependents)
	}
	targets := append(append([]string{}, changedPaths...), dependents...)

	// Phase 1: make the server see the pre-change state.
	mark := srv.Mark()
	sent := false
	for _, d := range srv.OpenDocs() {
		if changed[d.Path] {
			continue
		}
		// Keep documents opened by earlier checks in sync with the disk.
		cur := readFile(d.Path)
		var s bool
		if cur == nil {
			s, _ = srv.Close(d.Path)
		} else {
			s, _ = srv.SetText(d.Path, d.LanguageID, *cur)
		}
		sent = sent || s
	}
	for _, c := range changes {
		// Added files already exist on disk, and servers read unopened
		// files from disk, so hide them behind an empty placeholder.
		text := placeholder(l, c.new)
		if c.old != nil {
			text = *c.old
		}
		s, err := srv.SetText(c.path, l.LanguageID(c.path), text)
		if err != nil {
			return nil, 0, "", err
		}
		sent = sent || s
	}
	for _, d := range dependents {
		if cur := readFile(d); cur != nil {
			s, err := srv.SetText(d, l.LanguageID(d), *cur)
			if err != nil {
				return nil, 0, "", err
			}
			sent = sent || s
		}
	}
	if sent || !entry.primed {
		if !srv.Settle(ctx, targets, mark) {
			return nil, 0, "agent-squiggles: " + srv.Name() + " did not finish in time; skipped this check", ctx.Err()
		}
		entry.primed = true
	}
	before := e.collect(srv)

	// Phase 2: apply the change.
	mark = srv.Mark()
	var events []lsp.FileEvent
	maps := map[string]delta.LineMap{}
	for _, c := range changes {
		uri := lsp.PathToURI(c.path)
		switch {
		case c.new == nil:
			if _, err := srv.Close(c.path); err != nil {
				return nil, 0, "", err
			}
			events = append(events, lsp.FileEvent{URI: uri, Type: lsp.FileDeleted})
		default:
			if _, err := srv.SetText(c.path, l.LanguageID(c.path), *c.new); err != nil {
				return nil, 0, "", err
			}
			typ := lsp.FileChanged
			if c.old == nil {
				typ = lsp.FileCreated
			} else {
				maps[c.path] = delta.MapLines(*c.old, *c.new)
			}
			events = append(events, lsp.FileEvent{URI: uri, Type: typ})
		}
	}
	_ = srv.FilesChanged(events)
	if !srv.Settle(ctx, targets, mark) {
		return nil, 0, "agent-squiggles: " + srv.Name() + " did not finish in time; skipped this check", ctx.Err()
	}
	after := e.collect(srv)
	e.evict(srv, targets)

	return delta.New(before, after, maps), len(targets), "", nil
}

// collect converts the server's diagnostics into delta form, keeping only
// files inside the workspace and severities the config asks for.
func (e *Engine) collect(srv *lsp.Server) map[string][]delta.Diag {
	maxSev := e.cfg.MaxSeverity()
	out := map[string][]delta.Diag{}
	prefix := e.Root + string(filepath.Separator)
	for path, ds := range srv.Diagnostics() {
		if !strings.HasPrefix(path, prefix) || strings.Contains(path, string(filepath.Separator)+"node_modules"+string(filepath.Separator)) {
			continue
		}
		for _, d := range ds {
			sev := d.EffectiveSeverity()
			if sev > maxSev {
				continue
			}
			out[path] = append(out[path], delta.Diag{
				Path:     path,
				Line:     d.Range.Start.Line,
				Col:      d.Range.Start.Character,
				Severity: sev,
				Code:     d.CodeString(),
				Source:   d.Source,
				Message:  d.Message,
			})
		}
	}
	return out
}

// evict closes the least recently used documents beyond the cap.
func (e *Engine) evict(srv *lsp.Server, keep []string) {
	docs := srv.OpenDocs()
	if len(docs) <= maxOpenDocs {
		return
	}
	keepSet := map[string]bool{}
	for _, k := range keep {
		keepSet[k] = true
	}
	for _, d := range docs[:len(docs)-maxOpenDocs] {
		if !keepSet[d.Path] {
			_, _ = srv.Close(d.Path)
		}
	}
}

// server returns a running server for (l, root), starting it if needed.
// Missing servers are installed in the background when allowed.
func (e *Engine) server(ctx context.Context, l *langs.Language, root string) (*serverEntry, error) {
	key := l.ID + "\x00" + root
	entry := e.servers[key]
	if entry != nil && entry.srv != nil && entry.srv.Alive() {
		return entry, nil
	}
	if entry != nil && entry.err != nil && time.Since(entry.failedAt) < time.Minute {
		return nil, entry.err
	}
	spec, err := langs.Resolve(l, root, e.cfg.Languages[l.ID], e.trusted)
	if err != nil {
		if nie, ok := langs.IsNotInstalled(err); ok && nie.Installable && e.cfg.AutoInstallEnabled() {
			e.startInstall(l)
			return nil, errInstalling{l}
		}
		e.servers[key] = &serverEntry{lang: l, root: root, err: err, failedAt: time.Now()}
		return nil, err
	}
	e.logf("starting %s for %s", spec.Name, root)
	srv, err := lsp.Start(ctx, spec.Options(root, e.logf))
	if err != nil {
		e.servers[key] = &serverEntry{lang: l, root: root, err: err, failedAt: time.Now()}
		return nil, err
	}
	entry = &serverEntry{lang: l, root: root, srv: srv}
	e.servers[key] = entry
	return entry, nil
}

type errInstalling struct{ lang *langs.Language }

func (e errInstalling) Error() string { return "installing " + e.lang.Name + " language server" }

func (e *Engine) serverNotice(l *langs.Language, err error) string {
	var inst errInstalling
	if errors.As(err, &inst) {
		key := "installing:" + l.ID
		if e.warned[key] {
			return ""
		}
		e.warned[key] = true
		return "agent-squiggles: installing a " + l.Name + " language server in the background; checks start once it is ready"
	}
	key := "err:" + l.ID
	if e.warned[key] {
		return ""
	}
	e.warned[key] = true
	return "agent-squiggles: " + err.Error()
}

func (e *Engine) startInstall(l *langs.Language) {
	if e.installing[l.ID] {
		return
	}
	e.installing[l.ID] = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		e.logf("installing %s language server", l.Name)
		err := langs.Install(ctx, l, logWriter{e.logf})
		e.checkMu.Lock()
		e.installing[l.ID] = false
		delete(e.warned, "installing:"+l.ID)
		for k, entry := range e.servers {
			if entry.lang == l && entry.srv == nil {
				delete(e.servers, k)
			}
		}
		e.checkMu.Unlock()
		msg := "agent-squiggles: installed a " + l.Name + " language server"
		if err != nil {
			msg = "agent-squiggles: could not install a " + l.Name + " language server: " + err.Error()
		}
		e.logf("%s", msg)
		e.mu.Lock()
		e.notices = append(e.notices, msg)
		e.mu.Unlock()
	}()
}

func (e *Engine) takeNotices() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.notices
	e.notices = nil
	return n
}

// Status describes the engine for "doctor" and debugging.
type Status struct {
	Root    string   `json:"root"`
	Git     bool     `json:"git"`
	Servers []string `json:"servers"`
}

// Status reports the running servers.
func (e *Engine) Status() Status {
	e.checkMu.Lock()
	defer e.checkMu.Unlock()
	st := Status{Root: e.Root, Git: e.repo != nil}
	for _, entry := range e.servers {
		switch {
		case entry.srv != nil && entry.srv.Alive():
			st.Servers = append(st.Servers, fmt.Sprintf("%s: %s (%s)", entry.lang.ID, entry.srv.Name(), entry.root))
		case entry.err != nil:
			st.Servers = append(st.Servers, fmt.Sprintf("%s: error: %v", entry.lang.ID, entry.err))
		}
	}
	sort.Strings(st.Servers)
	return st
}

// Close shuts down all language servers.
func (e *Engine) Close() {
	e.checkMu.Lock()
	defer e.checkMu.Unlock()
	var wg sync.WaitGroup
	for _, entry := range e.servers {
		if entry.srv != nil {
			wg.Add(1)
			go func(s *lsp.Server) {
				defer wg.Done()
				s.Shutdown(context.Background())
			}(entry.srv)
		}
	}
	wg.Wait()
	e.servers = map[string]*serverEntry{}
}

// listFiles returns absolute paths of the workspace's files for l.
func (e *Engine) listFiles(l *langs.Language) []string {
	if e.repo != nil {
		rels, err := e.repo.ListFiles(langs.Patterns([]*langs.Language{l}))
		if err == nil {
			out := make([]string, 0, len(rels))
			for _, r := range rels {
				out = append(out, filepath.Join(e.Root, filepath.FromSlash(r)))
			}
			return out
		}
		e.logf("list files: %v", err)
	}
	return walkFiles(e.Root, l, 20000)
}

func walkFiles(root string, l *langs.Language, limit int) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".venv", "venv", "__pycache__", "dist", "build", "vendor":
				if p != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if langs.ForPath(p) == l {
			out = append(out, p)
			if len(out) >= limit {
				return io.EOF
			}
		}
		return nil
	})
	return out
}

// placeholder is the stand-in text for a file that did not exist before
// the change: empty, except that Go needs a package clause to stay in the
// same package.
func placeholder(l *langs.Language, text *string) string {
	if l != langs.Go || text == nil {
		return ""
	}
	for _, line := range strings.Split(*text, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "package" {
			return "package " + f[1] + "\n"
		}
	}
	return ""
}

func resolve(cwd, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(cwd, p)
}

func readFile(p string) *string {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	s := string(data)
	return &s
}

func equalPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

type logWriter struct{ logf func(string, ...any) }

func (w logWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			w.logf("install: %s", line)
		}
	}
	return len(p), nil
}
