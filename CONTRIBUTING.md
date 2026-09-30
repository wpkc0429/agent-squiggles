# Contributing

Thanks for helping make coding agents less blind.

## Reporting a problem

Open an issue with:

- the output of `agent-squiggles doctor`
- your Codex CLI version (`codex --version`)
- the relevant part of the daemon log (the path is printed by `doctor`)
- what the agent was told, and what you expected it to be told

False positives (an error reported as new that was not caused by the edit)
and false negatives (a real new error that was not reported) are both bugs.
A small repository that reproduces one is the most useful thing you can
attach.

## Development

You need Go (see `go.mod` for the minimum version) and git.

```sh
git clone https://github.com/wpkc0429/agent-squiggles
cd agent-squiggles
go test ./...
```

The integration tests start real language servers:

```sh
go run ./cmd/agent-squiggles servers install go typescript python
go test -tags integration ./...
```

To try a local build with Codex:

```sh
go build -o /tmp/agent-squiggles ./cmd/agent-squiggles
/tmp/agent-squiggles install --project   # writes .codex/hooks.json here
```

## Pull requests

- Keep changes focused, and add or update tests. Bug fixes should come with a
  test that fails without the fix.
- Run `gofmt`, `go vet` and the unit tests before pushing. CI runs the
  integration tests as well.
- New language support needs a `langs` definition, an integration test that
  covers a cross-file break, and an entry in the README table.
- By contributing, you agree that your contributions are licensed under the
  Apache License 2.0.

## Code of conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
