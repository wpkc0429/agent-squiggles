# AGENTS.md

Guidance for coding agents (and humans) working on agent-squiggles.

## Project shape

- Go module `github.com/wpkc0429/agent-squiggles`, one binary in
  `cmd/agent-squiggles`, everything else in `internal/`.
- No third-party Go dependencies. Keep it that way unless there is a strong
  reason; the single static binary is a feature.
- `docs/design.md` explains the architecture. Read it before changing
  `internal/engine`, `internal/snapshot` or `internal/lsp`.

## Commands

```sh
go build ./...
go vet ./...
gofmt -l .                          # must print nothing
go test ./...                       # unit tests, no network, no language servers
go test -tags integration ./...     # needs gopls, TypeScript and pyright (see below)
```

Install the language servers used by the integration tests with:

```sh
go run ./cmd/agent-squiggles servers install go typescript python
```

## Conventions

- Unit tests must not need network access or language servers. Anything that
  starts a real server goes in a `_integration_test.go` file with
  `//go:build integration`.
- The hook must never fail a Codex tool call: errors are logged or become a
  `systemMessage`, and `hook codex` always exits 0.
- Never modify the user's git index, HEAD, branches or stash. Snapshots go
  through the private index in `internal/snapshot`.
- Keep the report (`internal/report`) short: it is read by a model on every
  edit, so every line costs tokens.
- Comments explain why, not what. Match the style of the surrounding code.

## Dogfooding

If you use Codex on this repository, install agent-squiggles itself
(`go run ./cmd/agent-squiggles install --project`) so your edits are checked
by the tool you are changing.
