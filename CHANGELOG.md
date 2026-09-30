# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [semantic versioning](https://semver.org/).

## [Unreleased]

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
