# Security policy

agent-squiggles runs as a Codex hook with your user's permissions. It
starts language servers, runs git, and, when auto-install is enabled,
installs language servers from the Go module proxy, npm, or PyPI into its
own data directory.

## Reporting a vulnerability

Please do not open a public issue. Report it privately through
[GitHub security advisories](https://github.com/wpkc0429/agent-squiggles/security/advisories/new).
You should get a response within a week.

## Scope

Examples of issues we want to hear about:

- anything that lets repository content (a malicious `tsconfig.json`,
  `.agent-squiggles.json`, file names, and so on) execute commands you did
  not configure
- the daemon socket being reachable by other users
- modification of files outside the agent-squiggles data directory, or of
  the user's git index or refs

## Design notes

Codex hooks run outside the Codex sandbox. To keep a repository from running
code through the hook, agent-squiggles follows Codex's project trust
(`[projects."<root>"] trust_level = "trusted"` in `~/.codex/config.toml`).
For untrusted projects it ignores `.agent-squiggles.json` and does not run
binaries from inside the project (`node_modules`, virtualenvs). Bypasses of
this rule are in scope.
