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

- **baseline**: Codex relies on the checker named in `AGENTS.md`. By default
  this is a *placebo* baseline: the same hooks are installed and Codex gets
  the same flags, but the hooks are switched off (`AGENT_SQUIGGLES_DISABLE=1`),
  so they never report anything. Batches up to 2026-10-22 used a baseline with
  no hooks at all (`--no-placebo`).
- **squiggles**: the agent-squiggles hooks are installed and active.
  Everything else is identical: the same prompt, model, reasoning effort,
  `AGENTS.md`, and `workspace-write` sandbox.

For every run it records, from Codex's JSON event stream:

- **checker runs**: build, type-check and test commands Codex ran,
- **failed checker runs**: how many of those failed, i.e. edit → build → fix
  loops, split into compile or type errors (what agent-squiggles reports),
  formatting checks, and anything else,
- **type error reached checker**: runs in which a compile or type error
  survived until Codex ran the checker,
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
| `go-noisy-get` | Go | `Inventory.Get` returns `(Item, error)` | 16 `.Get(` calls, most of them on other types; an interface; a method value passed as a `func` |
| `ts-legacy-errors` | TypeScript | positional args → options object | about 40 known type errors in `src/legacy/` make every `tsc` run fail; a renamed re-export |

For tasks with known, pre-existing errors (`known_errors` in `task.json`), a
checker run that fails only on those counts as a pass, and the task's check
only fails on errors outside them.

## Results so far

| Batch | agent-squiggles | Baseline | Notes |
| --- | --- | --- | --- |
| [2026-10-21](results/2026-10-21/NOTES.md) | v0.2.0 | no hooks | found two bugs (a missed re-export caller, a slow load holding up the agent), fixed in v0.2.1 |
| [2026-10-22-v0.2.1](results/2026-10-22-v0.2.1/NOTES.md) | v0.2.1 | no hooks | no cost from the hooks; agent-squiggles reported nothing, so the difference between conditions was not its doing |
| [2026-10-22-placebo](results/2026-10-22-placebo/NOTES.md) | v0.2.1 | placebo | 36 runs; a type error reached the checker in 3/18 vs 0/18 runs; the 6 reports were each fixed before the first checker run |

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
as the run ends, or when the script is interrupted or terminated. If the
process is killed outright (for example by an out-of-memory killer), it cannot
clean up: delete any `codex-home/auth.json` left under `--workdir`. Each run
uses your Codex quota. Each run starts Codex and a language server or
compiler, so expect a few GB of memory in use while it works.

Results go to `eval/results/<date>/`: `runs.jsonl` (one record per run),
`summary.md`, and the raw event streams in `transcripts/` (not committed).

## Caveats

- Look at whether agent-squiggles actually reported anything (the line under
  the tables, and `squiggles` in `runs.jsonl`). A difference between the
  conditions in runs where it reported nothing cannot be its doing.
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
