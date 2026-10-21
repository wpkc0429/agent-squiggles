# Batch 2026-10-21: agent-squiggles v0.2.0

First batch: 4 tasks × 2 conditions × 2 repetitions with `gpt-5.6-luna`
(medium), agent-squiggles v0.2.0 (the runs predate the `agent_squiggles`
field in `runs.jsonl`). Two Rust runs are excluded because the eval's Rust
toolchain lacked `rustfmt` (see `excluded.json`). Tables: [summary.md](summary.md).
The failure-kind fields (`type_failures`, `format_failures`,
`other_failures`) were added later by re-reading the saved event streams; see
the [2026-10-22 notes](../2026-10-22-v0.2.1/NOTES.md).

## What the numbers say, and what they do not

Both conditions solved every task. The squiggles condition shows fewer failed
checker runs (0.1 vs 0.4 per run) and more first checks that passed (6/7 vs
4/7), but **most of that difference is not caused by agent-squiggles**:

- agent-squiggles ran 28 checks and reported new errors in only **one** run
  (`rs-parse-result-squiggles-2`, 2 errors). In that run the model fixed both
  before its first `cargo test`, which then passed.
- The Python difference (baseline 2/2 runs with a failed test run, squiggles
  0/2) happened in runs where agent-squiggles reported nothing, so the model
  received no extra information. The model simply updated the tests in its
  first patch in those runs.
- This model usually searched for callers before editing and changed every
  file in one patch, leaving nothing for agent-squiggles to catch. That
  matches the observation in openai/codex#8745 that the case for in-loop
  diagnostics is "fewer wrong turns", which needs tasks where wrong turns
  happen.

## Bugs this batch found

1. **Missed a caller behind a re-export.** In `ts-price-object-squiggles-1`
   Codex missed `src/report.ts`, which calls `formatPrice` through
   `export { formatPrice as fmt }` in `src/index.ts`. agent-squiggles only
   opened direct importers of the edited file, so it reported nothing and the
   model found the error with `tsc`. Fixed in v0.2.1: dependents are followed
   two import hops deep (`TestTypeScriptReExportChain`).
2. **A slow language-server load blocked the agent.** In
   `go-context-param-squiggles-2` gopls' initial load (a fresh, empty Go build
   cache per run) took about two minutes. The warm-up held the engine lock the
   whole time, so a `PostToolUse` check waited 47 s and checked nothing, and
   `PreToolUse` waited too. The run took 238 s instead of about 50 s, which
   also inflates this batch's average time for the squiggles condition. Fixed
   in v0.2.1: the warm-up no longer holds the lock, checks wait at most 10 s
   for a loading server and then skip, `PreToolUse` never waits for language
   servers, and the default check timeout dropped from 45 s to 20 s
   (`TestSlowWarmupDoesNotBlockEdits`).

The next batch re-runs everything with v0.2.1.
