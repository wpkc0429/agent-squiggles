# agent-squiggles

**Red squiggles for coding agents.**

[![CI](https://github.com/wpkc0429/agent-squiggles/actions/workflows/ci.yml/badge.svg)](https://github.com/wpkc0429/agent-squiggles/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[繁體中文](README.zh-TW.md)

Your editor underlines a mistake the moment you make it. Codex CLI edits files
without that feedback: it changes a function signature, moves on, and finds
out several steps later, if at all, that three callers in other files no
longer compile.

agent-squiggles is a small [Codex hook](https://learn.chatgpt.com/docs/hooks).
After every edit it asks the real language server (gopls, TypeScript,
pyright) what the edit broke. It then hands Codex **only the errors that edit
introduced**, including errors in files the agent never opened.

It is an answer to [openai/codex#8745](https://github.com/openai/codex/issues/8745)
("LSP integration (auto-detect + auto-install)"), the most upvoted open issue
in the Codex repository, built with the hook API Codex already ships.

## What the agent sees

Suppose Codex adds a parameter to `greet.Hello` in `greet/greet.go` without
opening `main.go`. Right after the `apply_patch`, the agent gets:

```text
agent-squiggles: this change introduced 1 new error. Pre-existing problems are not listed.

main.go:10:33: error: not enough arguments in call to greet.Hello; have (string); want (string, bool) [compiler WrongArgCount] (in a file you did not edit)
    fmt.Println(greet.Hello("world"))

Fix these before moving on, unless they are expected mid-refactor.
```

In our end-to-end test with Codex CLI, the agent replied *"The editor reports
one expected caller error in `main.go`, so I'm updating that call"* and fixed
it. It never ran the compiler and never opened `main.go` on its own.

## Why it is different

- **Only what you broke.** agent-squiggles compares diagnostics before and
  after each edit and reports the difference. It maps line numbers with a
  diff, so an old error that merely moved is not reported again. Legacy code
  with 400 existing type errors produces no noise.
- **Cross-file breakage.** When a signature change breaks a caller in
  another file, that caller is reported. Go relies on gopls's
  workspace-wide diagnostics. For TypeScript and Python, agent-squiggles
  finds the files that import the edited one and checks them too.
- **Shell edits count.** `sed -i`, code generators, `mv`, `rm`: anything the
  agent does through `Bash` is caught as well as `apply_patch` edits. Each
  tool call is bracketed with git snapshots written to a private index, so
  your own index, branches and stash are never touched. Read-only commands
  (`ls`, `rg`, `git diff`, …) are skipped.
- **Fast.** A background daemon keeps language servers warm per workspace.
  The pre-edit snapshot takes about 6 ms, and most checks finish in well
  under a second (see [Performance](#performance)).
- **Zero config.** It detects Go, TypeScript/JavaScript and Python. Missing
  language servers are installed into agent-squiggles' own directory, never
  globally.
- **It does not get in the way.** The report is added as context for the
  model. The tool call still succeeds, so the agent never mistakes a
  successful edit for a failed one. If you prefer a hard stop, there is a
  `block` mode.
- **One static binary**, written in Go with no runtime dependencies.

## Install

Requires Codex CLI with hooks support, git, and Linux or macOS (on Windows,
use WSL).

```sh
go install github.com/wpkc0429/agent-squiggles/cmd/agent-squiggles@latest
agent-squiggles install
```

`install` adds three hooks to `~/.codex/hooks.json`, keeping any hooks you
already have. It also offers to install language servers for the languages
it detects in the current repository. Use `--project` to write
`.codex/hooks.json` in the repository instead.

Codex asks you to review new hooks before they run. Start `codex` and run
`/hooks` to trust them.

Check the setup at any time:

```sh
agent-squiggles doctor
```

## Supported languages

| Language | Language server | Notes |
| --- | --- | --- |
| Go | `gopls` | Uses gopls's workspace diagnostics, so breakage in any package of the module is caught. |
| TypeScript / JavaScript | TypeScript 7 native (`tsc --lsp`), or `typescript-language-server` | Projects pinned to TypeScript ≤ 6 are checked with their own `tsserver`. Otherwise the native TypeScript 7 server is used. |
| Python | `pyright` or `basedpyright` | Uses your project's `.venv` / `venv` if present and respects your pyright config. |

Servers are found on `PATH`, in the project (`node_modules`, `.venv`), or in
`~/.local/share/agent-squiggles/servers`. You can install them explicitly:

```sh
agent-squiggles servers install go typescript python
```

## How it works

```text
 Codex                       agent-squiggles hook          daemon (one per workspace)
 ─────                       ────────────────────          ──────────────────────────
 PreToolUse (apply_patch|Bash) ─────────────────────▶ snapshot: git write-tree via a private index
 tool runs, files change
 PostToolUse ───────────────────────────────────────▶ snapshot again, diff the two trees
                                                      for each language server involved:
                                                        1. show the server the old text, collect diagnostics
                                                        2. show it the new text, collect diagnostics
                                                        3. line-map old → new, keep only unmatched new ones
            ◀─── additionalContext: "this change introduced N new errors…"
```

- **Snapshots** use `git ls-files`, `update-index` and `write-tree` against a
  private index file. They record the working tree as tree objects, including
  untracked files that are not ignored.
- **Settling** is language-specific. gopls is asked to diagnose
  synchronously (`gopls.diagnose_files`). TypeScript 7 uses pull diagnostics.
  Other servers are considered done when every checked file has published
  and the stream has gone quiet.
- **Matching** keys each diagnostic by severity, source, code and message.
  Old line numbers are mapped onto the new text with an LCS line diff, so
  errors that only moved stay matched.

A longer write-up is in [docs/design.md](docs/design.md).

## Configuration

Everything works without configuration. To change the defaults, add
`.agent-squiggles.json` to a repository, or
`~/.config/agent-squiggles/config.json` for all of them. Project settings
win.

```json
{
  "severity": "error",
  "maxDiagnostics": 20,
  "maxDependents": 25,
  "bash": true,
  "autoInstall": true,
  "mode": "context",
  "timeoutSeconds": 45,
  "languages": {
    "python": { "settings": { "python": { "analysis": { "typeCheckingMode": "strict" } } } },
    "go": { "disabled": false, "command": ["gopls"] }
  }
}
```

| Key | Default | Meaning |
| --- | --- | --- |
| `severity` | `"error"` | Report `"error"` only, or `"warning"` and above. |
| `maxDiagnostics` | `20` | Maximum diagnostics listed per report. |
| `maxDependents` | `25` | Maximum importing files opened per check (TypeScript, Python). |
| `bash` | `true` | Also check files changed by shell commands. |
| `autoInstall` | `true` | Install missing language servers on first use. |
| `mode` | `"context"` | `"context"` adds a note for the model. `"block"` replaces the tool result with the report. |
| `timeoutSeconds` | `45` | Upper bound for one check, including server startup. |
| `languages.<id>` | | Per-language `disabled`, `command`, and `settings` (merged into the server's settings). |

Set `AGENT_SQUIGGLES_DISABLE=1` to switch everything off for a session.

## Security model

Codex hooks run outside the Codex sandbox with your user's permissions, so
agent-squiggles follows Codex's own project trust decision
(`trust_level = "trusted"` in `~/.codex/config.toml`):

- **Trusted projects** may use their own language server binaries
  (`node_modules/typescript`, `.venv`) and their `.agent-squiggles.json`.
- **Untrusted projects** only get language servers from your `PATH` or the
  agent-squiggles directory. The project's config file is ignored, so a
  repository cannot choose which commands the hook runs or pass settings
  such as gopls `buildFlags`.

`agent-squiggles doctor` shows whether the current project is trusted. The
`check` command treats the repository you run it in as trusted, just as
running its tests would. Everything runs locally: nothing is sent anywhere,
and the network is used only to install missing language servers.

## Use it in CI, too

`check` compares the working tree with a git revision and exits 1 if there
are new problems. It is useful in pre-commit hooks and on pull requests:

```sh
agent-squiggles check --base origin/main
agent-squiggles check --base HEAD --json
```

## Performance

These timings come from the integration tests (small fixtures, Linux). They
measure the full check, from the snapshot diff to the report:

| Server | Typical check after an edit |
| --- | --- |
| TypeScript 7 native | 7–90 ms |
| gopls | 100–600 ms |
| typescript-language-server (TypeScript ≤ 6) | 0.6–1.6 s |
| pyright | 0.6–2 s |

The first check in a session also pays for server startup. The
`SessionStart` hook starts servers in the background so the first edit does
not wait for it.

## Commands

| Command | What it does |
| --- | --- |
| `agent-squiggles install [--project] [--yes] [--no-servers]` | Add the Codex hooks and set up language servers. |
| `agent-squiggles uninstall [--project]` | Remove the hooks (other hooks are kept). |
| `agent-squiggles doctor` | Show hooks, detected languages, servers and daemon status. |
| `agent-squiggles check [--base REV] [--all] [--json]` | Report problems introduced since `REV` and exit 1 if there are any. |
| `agent-squiggles servers install [go\|typescript\|python]` | Install language servers into the agent-squiggles directory. |
| `agent-squiggles stop` | Stop the background daemon for this workspace. |

## Limitations

- Linux and macOS only for now; Windows users can run Codex in WSL.
- Checking shell-command edits needs a git repository. Without git, only
  `apply_patch` edits are checked.
- TypeScript and Python cross-file checks follow relative imports and
  `tsconfig` `paths` aliases, up to `maxDependents` files. Bare package
  imports inside monorepos are not followed yet.
- Snapshots write git objects that nothing references. `git gc` removes them
  on its normal schedule.
- Codex CLI is the only supported agent today. The engine is agent-agnostic
  and adapters for other hook-capable agents are planned.

## Related projects

- [code-yeongyu/codex-lsp](https://github.com/code-yeongyu/codex-lsp)
  reports every LSP error in the files an edit touched and also exposes MCP
  tools (go to definition, rename). agent-squiggles focuses on reporting
  only new errors, including in untouched files and after shell edits.
- [oraios/serena](https://github.com/oraios/serena) and
  [isaacphi/mcp-language-server](https://github.com/isaacphi/mcp-language-server)
  give agents LSP-powered tools they can call on demand.

## Roadmap

- Rust (`rust-analyzer`), PHP and Dart
- Adapters for other agents with hook APIs
- A GitHub Action wrapping `agent-squiggles check`
- Windows support

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).
If you use Codex to work on this repository, [AGENTS.md](AGENTS.md) has the
project conventions.

## License

[Apache License 2.0](LICENSE)
