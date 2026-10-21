# Batch 2026-10-22: agent-squiggles v0.2.1

Second batch: the same 4 tasks × 2 conditions × 2 repetitions with
`gpt-5.6-luna` (medium), after the fixes from the first batch. The
agent-squiggles code is v0.2.1; the version string carries `-dirty` and one
extra commit because the eval runner itself had changed. The baseline had no
hooks at all (the placebo baseline came after this batch). The rustfmt
problem from the first batch was fixed before this one; no runs are excluded.
Tables: [summary.md](summary.md).

The batch ran task by task in the foreground (a first attempt in the
background was stopped by the host for low memory before its first run
finished).

## Results

- Both conditions solved every task (8/8 each).
- No cost from the hooks after the v0.2.1 fixes: 33 s vs 34 s per run on
  average and the same token use (112k in, about 2.5k out).
- A compile or type error reached the checker in 3/8 baseline runs
  (`py-charge-currency-baseline-1`, `rs-parse-result-baseline-1`,
  `ts-price-object-baseline-1`, which again missed the caller behind the
  `fmt` re-export) and in 0/8 squiggles runs.

## What that does and does not show

agent-squiggles ran 30 checks in this batch and **reported nothing**: in all
8 squiggles runs, the model's edits introduced no new errors. Because
agent-squiggles only reports after an edit, it cannot have caused the model
to make fewer mistakes in these runs. The 3/8 vs 0/8 difference is either
chance (8 runs per condition is very little) or a difference between the
conditions other than the reports, such as the hook configuration and the
`--dangerously-bypass-hook-trust` flag that only the squiggles condition had.

Taking both batches together, a compile or type error reached the checker in
6/15 baseline runs and 1/15 squiggles runs, but agent-squiggles reported in
only one squiggles run (and missed one error because of a bug since fixed).
That is not evidence that agent-squiggles helps, and with this sample size it
is not evidence that it does not.

Two changes follow for the next batch:

1. **Placebo baseline.** Both conditions get the same hooks and flags; in the
   baseline the hooks are switched off. Any remaining difference is then down
   to agent-squiggles' reports.
2. **Failure kinds.** Failed checker runs are now split into compile or type
   errors, formatting checks (two Rust runs here failed `cargo fmt --check`,
   which agent-squiggles does not look at) and anything else. The fields were
   added to both batches' `runs.jsonl` by re-reading the saved event streams
   with the same classifier; the measured counts did not change.
