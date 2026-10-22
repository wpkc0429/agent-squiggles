# Batch 2026-10-22 (placebo): agent-squiggles v0.2.1

Third batch: 6 tasks × 2 conditions × 3 repetitions (36 runs) with
`gpt-5.6-luna` (medium) and agent-squiggles v0.2.1 (the version string's
extra commits only touch `eval/`). This is the first batch with a **placebo
baseline**: both conditions install the same hooks and run Codex with the
same flags, and in the baseline the hooks are switched off, so the reports
are the only difference. Two tasks are new: `go-noisy-get` (a `Get` method
among many unrelated `.Get(` calls) and `ts-legacy-errors` (about 40 known
type errors make every `tsc` run fail). Tables: [summary.md](summary.md).

## Outcome

- Both conditions solved every task (18/18).
- No cost from the hooks: 36 s vs 35 s per run on average; tokens in 117k vs
  125k, out 2.7k for both.
- A compile or type error reached the checker in **3/18 baseline runs and
  0/18 squiggles runs**. All three baseline cases are `py-charge-currency`.
  With 18 runs per condition this difference alone is not statistically
  significant (one-sided Fisher exact p ≈ 0.11), but this time the mechanism
  can be seen directly in the transcripts.

## Mechanism

agent-squiggles reported new errors 6 times (12 errors, 75 checks). In every
case the model fixed the reported errors before running the checker, and the
first checker run passed:

| Run | What the first edit broke | Reported | Then |
| --- | --- | --- | --- |
| `py-charge-currency-squiggles-1`, `-2` | the tests still called `charge` without `currency` | 2 errors in `tests/test_billing.py` | the model fixed the tests; the first test run passed |
| `ts-price-object-squiggles-1`, `-2` | missed `src/report.ts` (calls through the `fmt` re-export) | 1 error | "The compiler surfaced one additional call site in `src/report.ts`"; fixed before the first `tsc` |
| `ts-legacy-errors-squiggles-1` | missed `src/widgets/badge.ts` (calls through the `dur` re-export) | 1 error | fixed; `tsc` then showed only the known `src/legacy/` errors |
| `go-noisy-get-squiggles-2` | updated three of the callers, not the rest | 5 errors | updated `cmd/shop`, `stock` and `web`; the build passed |

The Python task is the clearest comparison. In all three baseline runs Codex
updated the production callers, ran the tests, saw them fail on the stale
calls in `tests/test_billing.py`, and fixed them. In two of the three
squiggles runs it made exactly the same first edit, was told about the stale
calls, and fixed them before running the tests. (In the third it updated the
tests in its first edit.)

## Reading

- With this model, wrong turns are rare: it usually searches for every caller
  and edits them all at once, which leaves agent-squiggles nothing to report.
  When it does miss callers, agent-squiggles catches them right after the
  edit, and the model fixes them without a failed build, type-check or test
  run in between.
- That saves one checker round when it happens, not wall-clock time on these
  small repositories, where the checker takes a second. This matches the
  measurement reported in openai/codex#8745: fewer wrong turns, not speed.
  The saving would matter more where the checker is slow (a full `tsc` on a
  large project, a long test suite).
- The two new tasks did not trip the model: neither noisy search nor a
  project full of known errors made it leave an error behind, in either
  condition. In `ts-legacy-errors` both conditions handled the known errors
  correctly.
- Rust runs failed on `cargo fmt --check` in both conditions (formatting,
  which agent-squiggles does not check).
