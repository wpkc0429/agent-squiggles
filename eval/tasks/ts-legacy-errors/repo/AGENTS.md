# AGENTS.md

Strict TypeScript. Type-check with `npx tsc -p .` (it does not emit files).

The legacy code in `src/legacy/` has about 40 known type errors that are being
cleaned up separately. Leave those files alone, but do not introduce any new
type errors anywhere.
