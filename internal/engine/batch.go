package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wpkc0429/agent-squiggles/internal/cargo"
	"github.com/wpkc0429/agent-squiggles/internal/config"
	"github.com/wpkc0429/agent-squiggles/internal/delta"
	"github.com/wpkc0429/agent-squiggles/internal/langs"
)

// Batch languages (Rust) are checked by running a command over the whole
// project. That command reads files from disk, so it cannot be shown the
// pre-edit text the way a language server can. Instead the engine keeps the
// results of the last run together with the snapshot tree they belong to:
// if that tree is the pre-edit tree, it is the baseline. PreToolUse re-runs
// the check whenever the cached results are stale, so in the common case
// (one edit after another) each edit costs exactly one run.

// batchState is the result of one check run.
type batchState struct {
	tree  string
	diags map[string][]delta.Diag
}

// checkTrees carries the snapshot trees around a check.
type checkTrees struct {
	base, cur string
	// since is set for the "check --base" command, where no baseline run
	// exists for the base revision.
	since bool
	all   bool
}

var errStale = errors.New("files changed while the check was running")

// batchRoots returns the known project roots for a batch language,
// discovering them on first use. The caller holds batchMu.
func (e *Engine) batchRoots(l *langs.Language) []string {
	if roots, ok := e.batchRootList[l.ID]; ok {
		return roots
	}
	seen := map[string]bool{}
	var roots []string
	for _, f := range e.listFiles(l) {
		if !isMarker(l, f) {
			continue
		}
		if r := l.ProjectRoot(f, e.Root); !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}
	e.batchRootList[l.ID] = roots
	return roots
}

func (e *Engine) addBatchRoot(l *langs.Language, root string) {
	for _, r := range e.batchRoots(l) {
		if r == root {
			return
		}
	}
	e.batchRootList[l.ID] = append(e.batchRootList[l.ID], root)
}

func isMarker(l *langs.Language, path string) bool {
	for _, m := range l.Markers {
		if filepath.Base(path) == m {
			return true
		}
	}
	return false
}

// refreshBatch makes sure every batch project has results for tree, so
// the next PostToolUse has a baseline. It is a no-op when they already do,
// and it never waits for language-server work.
func (e *Engine) refreshBatch(tree string) {
	if tree == "" || e.repo == nil {
		return
	}
	cfg, trusted := e.opts()
	if !trusted {
		return
	}
	e.batchMu.Lock()
	defer e.batchMu.Unlock()
	for _, l := range langs.All() {
		if !l.Batch || cfg.Languages[l.ID].Disabled {
			continue
		}
		for _, root := range e.batchRoots(l) {
			if st := e.batch[root]; st != nil && st.tree == tree {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds)*time.Second)
			if _, err := e.runBatch(ctx, cfg, trusted, l, root, tree); err != nil {
				e.logf("baseline %s check in %s: %v", l.ID, root, err)
			}
			cancel()
		}
	}
}

// runBatch checks root and records the result as the state of tree. The
// caller holds batchMu.
func (e *Engine) runBatch(ctx context.Context, cfg config.Config, trusted bool, l *langs.Language, root, tree string) (map[string][]delta.Diag, error) {
	spec, err := langs.Resolve(l, root, cfg.Languages[l.ID], trusted)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	diags, err := cargo.Check(ctx, spec.Command[0], root)
	if err != nil {
		delete(e.batch, root)
		return nil, err
	}
	// Results only describe tree if nothing changed while cargo ran.
	if now, err := e.repo.Snapshot(); err != nil || now != tree {
		delete(e.batch, root)
		return nil, errStale
	}
	e.batch[root] = &batchState{tree: tree, diags: diags}
	e.logf("%s in %s: %d files with diagnostics in %v", spec.Name, root, len(diags), time.Since(start).Round(time.Millisecond))
	return diags, nil
}

// checkBatch compares a batch project's diagnostics before and after a
// change. The caller holds checkMu (which makes e.cfg and e.trusted safe to
// read); batch state is guarded by batchMu.
func (e *Engine) checkBatch(ctx context.Context, l *langs.Language, root string, changes []fileChange, trees checkTrees) ([]delta.Diag, int, string, error) {
	e.batchMu.Lock()
	defer e.batchMu.Unlock()
	if e.repo == nil {
		return nil, 0, e.once("nogit:"+l.ID, "agent-squiggles: "+l.Name+" checks need a git repository"), nil
	}
	spec, err := langs.Resolve(l, root, e.cfg.Languages[l.ID], e.trusted)
	if err != nil {
		return nil, 0, e.serverNotice(l, err), err
	}
	if trees.since {
		if !trees.all {
			return nil, 0, e.once("since:"+l.ID, "agent-squiggles: "+l.Name+" cannot be compared with a past revision; use --all to list its errors in the changed files"), nil
		}
		diags, err := cargo.Check(ctx, spec.Command[0], root)
		if err != nil {
			return nil, 0, "", err
		}
		var out []delta.Diag
		for _, c := range changes {
			out = append(out, e.bySeverity(diags)[c.path]...)
		}
		return out, len(changes), "", nil
	}

	e.addBatchRoot(l, root)
	before := e.batch[root]
	after, err := e.runBatch(ctx, e.cfg, e.trusted, l, root, trees.cur)
	if err != nil {
		return nil, 0, "", fmt.Errorf("%s: %w", spec.Name, err)
	}
	if before == nil || before.tree != trees.base {
		// No results for the pre-edit state (the project was new to us, or
		// the baseline run did not finish in time). The run just made is the
		// baseline for the next edit.
		return nil, len(changes), e.once("baseline:"+root, "agent-squiggles: recorded a "+l.Name+" baseline; checks start with the next edit"), nil
	}
	maps := map[string]delta.LineMap{}
	for _, c := range changes {
		if c.old != nil && c.new != nil {
			maps[c.path] = delta.MapLines(*c.old, *c.new)
		}
	}
	return delta.New(e.bySeverity(before.diags), e.bySeverity(after), maps), len(changes), "", nil
}

// bySeverity drops diagnostics below the configured severity.
func (e *Engine) bySeverity(in map[string][]delta.Diag) map[string][]delta.Diag {
	maxSev := e.cfg.MaxSeverity()
	out := map[string][]delta.Diag{}
	for path, ds := range in {
		for _, d := range ds {
			if d.Severity <= maxSev {
				out[path] = append(out[path], d)
			}
		}
	}
	return out
}

// once returns msg the first time key is seen and "" afterwards.
func (e *Engine) once(key, msg string) string {
	if e.warned[key] {
		return ""
	}
	e.warned[key] = true
	return msg
}
