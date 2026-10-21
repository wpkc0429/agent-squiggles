# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [semantic versioning](https://semver.org/).

## [Unreleased]

## [0.2.1] - 2026-10-21

Both fixes come from the first evaluation batch (`eval/results/2026-10-21`).

### Fixed

- Callers behind a re-export were missed: TypeScript and Python dependents
  are now followed two import hops deep, so a file that imports a barrel
  (`index.ts`, a package `__init__.py`) re-exporting the edited module is
  checked.
- A slow language-server load could hold up the agent for minutes: the
  warm-up no longer holds the engine lock, a check waits at most 10 seconds
  for a loading server and is then skipped, and `PreToolUse` never waits for
  language servers.

### Changed

- The default `timeoutSeconds` is 20 (was 45).

### Added

- `eval/`: a reproducible evaluation that runs Codex on the same tasks with
  and without agent-squiggles.

## [0.2.0] - 2026-10-08

### Added

- Rust support through `cargo check`. It reports rustc's own errors across
  every crate in the Cargo workspace, including broken `Cargo.toml` files.
  The previous run's results serve as the baseline, so consecutive edits
  cost one `cargo check` each. Builds go to `target/agent-squiggles`. Rust
  is only checked in projects Codex trusts, because `cargo check` runs build
  scripts and procedural macros.

### Changed

- The report now says its errors come from "the compiler or language
  server".

## [0.1.0] - 2026-09-30

First release.

### Added

- Codex hooks (`SessionStart`, `PreToolUse`, `PostToolUse`) that report only
  the diagnostics an edit introduced, including in files the edit did not
  touch.
- Change detection for `apply_patch` and shell commands through git
  snapshots in a private index.
- Language support: Go (gopls), TypeScript/JavaScript (TypeScript 7 native
  or typescript-language-server), Python (pyright, basedpyright).
- Per-workspace daemon that keeps language servers warm.
- Automatic installation of missing language servers into the
  agent-squiggles data directory.
- Commands: `install`, `uninstall`, `check`, `doctor`, `servers install`,
  `stop`, `version`.
- Configuration through `.agent-squiggles.json` and
  `~/.config/agent-squiggles/config.json`.
- Security: project-local language servers and project config are only used
  for projects Codex trusts, and the daemon socket directory must be private.

[Unreleased]: https://github.com/wpkc0429/agent-squiggles/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/wpkc0429/agent-squiggles/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/wpkc0429/agent-squiggles/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/wpkc0429/agent-squiggles/releases/tag/v0.1.0
