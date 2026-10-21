# Evaluation: does agent-squiggles change outcomes?

The question this answers comes from
[openai/codex#8745](https://github.com/openai/codex/issues/8745#issuecomment-3713058579):
Codex can already be told to run a type checker in `AGENTS.md`, so what does
in-loop diagnostics feedback add, measured in outcomes rather than mechanism?

## Method

Each task in [`tasks/`](tasks) is a small repository plus a change request
whose callers are easy to miss with text search alone: an interface the type
must keep satisfying, a renamed re-export, an aliased import, a call through a
module object. Every task repository has an `AGENTS.md` that tells Codex the
exact build, type-check or test command to run before finishing.

`run.py` runs every task under two conditions with `codex exec`:

- **baseline**: no hooks. Codex relies on the checker named in `AGENTS.md`.
- **squiggles**: the agent-squiggles hooks are installed. Everything else is
  identical: the same prompt, model, reasoning effort, `AGENTS.md`, and
  `workspace-write` sandbox.

For every run it records, from Codex's JSON event stream:

- **checker runs**: build, type-check and test commands Codex ran,
- **failed checker runs**: how many of those failed, i.e. edit → build → fix
  loops,
- **first check passed**: whether the first such command already succeeded,
- edits, tokens and wall-clock time,

and then re-runs the task's check command itself to decide **success**.

The two conditions alternate in order, and each run gets a fresh copy of the
repository and a fresh, throwaway `CODEX_HOME`.

| Task | Language | Change | What makes it easy to miss |
| --- | --- | --- | --- |
| `go-context-param` | Go | add `context.Context` to `Store.Get` | an interface the store must keep satisfying; callers in four packages |
| `ts-price-object` | TypeScript | positional args → options object | a caller uses a renamed re-export (`fmt`); another a namespace import |
| `py-charge-currency` | Python | new keyword-only `currency` | callers via `import ... as pay` and `payments.charge(...)` |
| `rs-parse-result` | Rust | `parse` returns `Result` | a caller via `use ... as load`; the binary and an integration test |

## Running it

```sh
python3 eval/run.py --reps 2                    # all tasks, both conditions
python3 eval/run.py --tasks go-context-param    # one task
python3 eval/run.py --summarize --out eval/results/2026-10-21
```

It needs `git`, a logged-in `codex`, Go, Node with TypeScript (the tasks link
`--ts-modules`; by default the copy `agent-squiggles servers install
typescript` puts in place), Python 3.8+ and `cargo`. The script copies your
Codex auth file into each run's throwaway `CODEX_HOME` and deletes it as soon
as the run ends. Each run uses your Codex quota.

Results go to `eval/results/<date>/`: `runs.jsonl` (one record per run),
`summary.md`, and the raw event streams in `transcripts/` (not committed).

## Caveats

- These are small, synthetic repositories, designed so that a missed caller
  is plausible. Real repositories are larger, which makes the checker slower
  and grep noisier, but also gives the model more context.
- One model and one reasoning effort per batch. Model behavior changes
  between versions, and runs are not deterministic, so small samples are
  noisy: look at the per-task rows and the transcripts, not just the totals.
- In the squiggles condition Codex runs with `--dangerously-bypass-hook-trust`
  because a throwaway `CODEX_HOME` cannot have its hooks reviewed
  interactively. This only skips the trust prompt for the hooks installed by
  the script.
