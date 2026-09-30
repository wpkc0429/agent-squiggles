# Design

This document explains how agent-squiggles decides what an edit broke, and
why it is built the way it is. The code lives in `internal/`, one package per
concern; the package comments are the best entry points.

## Goals

1. Tell the agent about errors an edit introduced, **and nothing else**. A
   report that repeats 300 pre-existing errors teaches the agent to ignore
   reports.
2. Include breakage outside the edited files. Most costly mistakes are the
   ones that break a caller the agent did not look at.
3. Cover every way an agent edits files, not only its patch tool.
4. Add as little latency as possible to each tool call, and never make a
   tool call fail because of agent-squiggles.

## Components

```text
cmd/agent-squiggles      entry point
internal/cli             commands (install, check, doctor, hook, daemon, …)
internal/codex           Codex hook payloads, output, hooks.json editing
internal/daemon          per-workspace background process + Unix socket protocol
internal/engine          change detection and the before/after comparison
internal/snapshot        git tree snapshots through a private index
internal/lsp             minimal LSP client (JSON-RPC, documents, diagnostics)
internal/langs           language definitions, server discovery and installation
internal/deps            reverse-import scan for TypeScript and Python
internal/delta           line mapping and diagnostic matching
internal/report          the text the agent reads
internal/bashcmd         read-only shell command detection
internal/patch           apply_patch parsing (used without git)
internal/config          configuration files
```

## One tool call, step by step

1. **PreToolUse** (`apply_patch` or `Bash`). The hook process forwards the
   payload to the workspace daemon, starting it if needed. Unless the command
   is read-only, the daemon records a snapshot `T0`.
2. The tool runs.
3. **PostToolUse**. The daemon records `T1` and diffs `T0..T1`, limited to
   supported file types. The result is the set of changed files with their
   old and new contents.
4. Changed files are grouped by language and project root. Each group gets
   its own language server (`gopls` per module or `go.work`, one TypeScript
   server per project, and so on).
5. For each group:
   - **Phase 1 (before):** make the server's view equal to `T0`. Changed files
     are opened with their old text. Added files are opened as an empty
     placeholder, because they already exist on disk and servers read
     unopened files from disk. For TypeScript and Python, files that import
     the changed ones are opened as well. Wait for diagnostics to settle and
     record them.
   - **Phase 2 (after):** replace each document with its new text (or close
     it if deleted), send `workspace/didChangeWatchedFiles`, wait for
     diagnostics to settle, and record them again.
   - **Delta:** keep the diagnostics in "after" that have no counterpart in
     "before".
6. The daemon renders the report. The hook prints it as `additionalContext`
   for the model, plus a one-line `systemMessage` for the user.

Documents stay open between checks (up to 80 per server, least recently
used first out). In the common case where the agent edits the same file
repeatedly, the server already holds the pre-edit text, so phase 1 costs
nothing.

## Why snapshots instead of parsing the patch

Parsing `apply_patch` would tell us which files the patch touched, but not
what a `sed -i`, a code generator or `git checkout -- file` did. Git already
knows how to record a working tree cheaply:

- A private index (seeded from the real one) keeps git's stat cache, so
  unchanged files are not re-hashed.
- `git ls-files --modified --deleted --others --exclude-standard` lists what
  changed, `git update-index --add --remove --stdin` stages it into the
  private index, and `git write-tree` produces an immutable tree hash.
- `git diff-tree` between two tree hashes is exact, and `git cat-file`
  returns any old content.

We avoid `git add <pathspec>` on purpose: it fails outright when any
pathspec (for example `*.py` in a Go-only repository) matches nothing.

The user's index, `HEAD`, branches and stash are never modified. The only
side effect is unreferenced objects in `.git/objects`, which `git gc` prunes.

Outside a git repository, the engine falls back to parsing `apply_patch` and
reading the touched files before and after. Shell edits are not detected in
that mode.

## Knowing when a server is done

LSP has no standard "diagnostics are up to date" signal, so each server gets
the strategy that works best for it:

| Strategy | Used for | How |
| --- | --- | --- |
| `gopls` | gopls | `workspace/executeCommand gopls.diagnose_files` diagnoses the snapshot synchronously and publishes before replying. gopls skips re-publishing unchanged diagnostics, so waiting for a publish would stall. |
| `pull` | TypeScript 7 native | `textDocument/diagnostic` for every target document. |
| `push` | typescript-language-server, pyright | Wait until every target document has published after the change, no `$/progress` work is active, and the stream has been quiet for 300 ms. |

## Matching diagnostics

Diagnostics have no identity, and an edit above an error moves it to a new
line. `internal/delta`:

1. Builds a line map from old to new text: it trims the common prefix and
   suffix, then computes an LCS on the changed middle (bounded at 4M cells;
   anything larger degrades to "removed").
2. Keys diagnostics by severity, source, code and whitespace-normalized
   message.
3. Matches new diagnostics to old ones with the same key: first on the same
   mapped line, then to the nearest remaining one anywhere in the file. An
   error whose line was merely rewritten is still considered the same error.
4. Reports what is left unmatched.

This is deliberately conservative: when two identical errors exist and one
more appears, exactly one is reported.

## Cross-file breakage

- **Go:** gopls publishes diagnostics for every file in the workspace
  packages, open or not, so a broken caller anywhere shows up in phase 2.
- **TypeScript and Python:** servers only report on open documents. The
  `deps` package finds importers with a cached regex scan: relative imports,
  `tsconfig`/`jsconfig` `paths` aliases, `index` barrels, ESM `.js`
  specifiers, and Python absolute, relative and `src/` layouts. It opens up
  to `maxDependents` of them, closest first.

## The daemon

Language servers take seconds to start and to load a project, so a
per-workspace daemon keeps them warm:

- It listens on a Unix socket in `$XDG_RUNTIME_DIR/agent-squiggles/` (or a
  per-user temp directory) named after a hash of the workspace root.
- A `flock` guarantees one daemon per workspace. A new daemon waits briefly
  for a shutting-down one to release the lock.
- It exits after 30 minutes without requests.
- Hook processes spawn it on demand (`setsid`, output to
  `~/.local/share/agent-squiggles/logs/`). The `SessionStart` hook starts it
  early and warms servers in the background. The hook itself returns
  immediately, because older Codex builds skip `async` hooks.

## Hook output

In the Codex source, a `PostToolUse` result with `decision: "block"` makes
the tool call **fail** and uses the hook's reason as the error. The model
then believes its patch did not apply and may apply it again. So the default
mode returns `hookSpecificOutput.additionalContext`, which Codex records as
a developer message, and leaves the tool result alone. `mode: "block"`
exists for people who want the harder stop.

## Trust

Hooks run outside the Codex sandbox, with the user's full permissions.
Language servers are powerful: a project's `node_modules/.bin/tsc`, a
virtualenv's interpreter (which pyright runs to find search paths), or
gopls `buildFlags` like `-toolexec` all execute code chosen by the
repository. agent-squiggles therefore follows Codex's own trust decision.
`internal/trust` reads `[projects."<root>"] trust_level` from Codex's
`config.toml`, matching the exact project root as Codex does. For untrusted
projects it:

- ignores `.agent-squiggles.json`, which could set server commands and
  settings,
- skips project-local servers (`node_modules/typescript`, `.venv/bin/*`)
  and does not point pyright at the project's interpreter.

The daemon re-reads trust whenever Codex's config changes and restarts its
servers if the answer changed.

## Failure policy

The hook always exits 0. Missing servers, timeouts and daemon errors become
a one-time `systemMessage` for the user or a log line, never a failed tool
call.
